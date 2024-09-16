package main

import (
    "fmt"
    "embed"
    "time"
    "math/rand"
    "context"
    "database/sql"

    "prod_cons/common"
    "prod_cons/db"

    log "github.com/sirupsen/logrus"
    _ "github.com/lib/pq" // PostgreSQL driver
    zmq "github.com/pebbe/zmq4"
    "github.com/golang-migrate/migrate/v4"
    "github.com/golang-migrate/migrate/v4/database/postgres"
    "github.com/golang-migrate/migrate/v4/source/iofs"

)


//go:embed migrations/*.sql
var migrationFiles embed.FS

var version = "development"
const taskTypeRange = 10
const taskValueRange = 100
const errorLimit = 20
const migrationsPath = "migrations"

// validate config CB func to be called after load
//
// Params:
// - config - config struct to verify
func validateConfig(config *Config) error {
    if config.MsgProdRate < 0 {
        return fmt.Errorf("invalid config: negative MsgProdRate")
    }

    return nil
}

// run DB migrations up after starting the vm
//
// Params:
//  - DB handler
func runDBMigrations(db *sql.DB) error {
    const DBName = "postgress"
    const srcName = "iofs"

    log.Info("Running DB Migrations")
    driver, err := iofs.New(migrationFiles, migrationsPath)
    if (nil != err) {
        return fmt.Errorf("failed to create iofs migration source: %v", err)
    }

    migrationDriver, err := postgres.WithInstance(db, &postgres.Config{})
    if (nil != err) {
        return fmt.Errorf("failed to create migration driver: %v", err)
    }
    migrateInstance, err := migrate.NewWithInstance(srcName, driver, DBName, migrationDriver)
    if (nil != err) {
        return fmt.Errorf("failed to create migrate instance: %v", err)
    }

    err = migrateInstance.Up()
    if (nil != err) && err != migrate.ErrNoChange {
        return fmt.Errorf("migration failed: %v", err)
    }

    log.Info("Migrations applied successfully!")

    return nil
}


// create task in DB and return current pending tasks count
// not sending as transaction, but rather query with read and update, basically used backlog count as
// a 'soft limit', didnt use transactions to reduce consumers clogging
//
// params:
// - queries: db sql queries
// - taskParams: taske params to set
//
// return:
// - new task id
// - current pending tasks backlog count
func createTaskAndGetBacklog(queries *db.Queries, taskParams *db.CreateTaskAndGetBacklogParams) (int, int, error) {
    result, err := queries.CreateTaskAndGetBacklog(
        context.Background(),
        *taskParams,
    )

    if (nil != err) {
        return 0, 0, err
    }

    return int(result.TaskID), int(result.BacklogCount), nil
}

// send byte array on socket, allow 5 retries with 1sec sleep if failed before
// dropping this data
//
// params:
// - producer - producer socket to send on
// - msgBytes - bytearray of data
func sendWithTimeout(producer *zmq.Socket, msgBytes []byte) error {
    retries := 5
    timeout := 1 * time.Second

    for attempt := 1; attempt <= retries; attempt++ {
        // Create a channel to signal when the send operation is done
        done := make(chan error, 1)

        go func() {
            _, err := producer.SendBytes(msgBytes, 0)
            done <- err
        }()

        // wait for either success/failure/timeout
        select {
            case err := <-done:
                if err == nil {
                    return nil // Success
                }
                fmt.Printf("Attempt %d/%d failed: %s\n", attempt, retries, err)

            case <-time.After(timeout):
                fmt.Printf("Attempt %d/%d timed out\n", attempt, retries)
        }
    }

    return fmt.Errorf("failed to send message after %d attempts", retries)
}


func main() {
    utils.HandleVersionFlag(&version)
    utils.SetAppName("producer")

    config, err := utils.LoadConfig[Config]("config.json", validateConfig)
    if (nil != err) {
        log.Fatalf("Failed loading config: %s", err)
    }

    err = utils.SetLoggingLevel(config.Logging.Level)
    if (nil != err) {
        log.Fatalf("Failed to set logging: %s", err)
    }

    cleanupCB, err := utils.SetLoggingOutput(config.Logging.Output)
    if (nil != err) {
        log.Fatalf("Failed to set logging: %s", err)
    }
    if (nil != cleanupCB) {
        defer cleanupCB()
    }

    dbConn, err := utils.ConnectToDB(&config.DBConnConfig)
    if (nil != err) {
        log.Fatalf("Failed connecting to DB: %s", err)
    }
    defer dbConn.Close()

    producer, err := utils.ConnectToMQ(config.ZeroMQComm, nil)
    if (nil != err) {
        log.Fatalf("Failed connection to producer: %s", err)
    }
    defer producer.Close()

    queries := db.New(dbConn)
    err = runDBMigrations(dbConn)
    if (nil != err) {
        log.Fatalf("Failed running DB migrations: %s", err)
    }

    ticker := time.NewTicker(time.Second / time.Duration(config.MsgProdRate))

    defer ticker.Stop()
    errCnt := 0

    log.Debug("Starting main loop, limitng to %d messages per sec", config.MsgProdRate)
    for range ticker.C {
        taskParams := &db.CreateTaskAndGetBacklogParams{
            Type:           int32(rand.Intn(taskTypeRange)),
            Value:          int32(rand.Intn(taskValueRange)),
            State:          db.TaskStatePending,
            CreationTime:   float64(time.Now().UTC().Unix()),
        }

        log.Debug("creating task and writing to db")
        taskID, backlogCount, err := createTaskAndGetBacklog(queries, taskParams)
        if (nil != err) {
            errCnt += 1
            log.Errorf("Failed to create task: %v, dropping task", err)
            if (errCnt > errorLimit) {
                log.Fatalf("Producer reached max error limit[%d], closing the app", errorLimit)
            }
            continue
        }

        log.Infof("task %d created", taskID)
        log.Debugf("current backlog: %d", backlogCount)

        msg := &utils.TaskMsg {
            ID: taskID,
        }

        log.Debugf("serializing task %d to zmq", taskID)
        msgBytes, err := utils.SerializeTaskMsg(msg)
        if (nil != err) {
            errCnt += 1
            log.Errorf("Failed to serialize msg[taskID %d]: %s, dropping", taskID, err)
            if (errCnt > errorLimit) {
                log.Fatalf("Producer reached max error limit[%d], closing the app", errorLimit)
            }
            continue
        }

        log.Debugf("sending task %d to zmq", taskID)
        err = sendWithTimeout(producer, msgBytes)
        if (nil != err) {
            errCnt += 1
            log.Errorf("Failed sending task %d to zeroMQ: %s, dropping", taskID, err)
            if (errCnt > errorLimit) {
                log.Fatalf("Producer reached max error limit[%d], closing the app", errorLimit)
            }
            continue
        }

        log.Debugf("Task %d has been created and sent, current backlog: %d", taskID, backlogCount)
        if (backlogCount >= config.MaxBacklog) {
            log.Infof("Backlog reached its limit[%d], producer is closing", config.MaxBacklog)
            break
        }
    }

    log.Info("Producer has finished")
}
