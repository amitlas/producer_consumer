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

    "github.com/prometheus/client_golang/prometheus"
    "github.com/golang-migrate/migrate/v4"
    "github.com/golang-migrate/migrate/v4/database/postgres"
    "github.com/golang-migrate/migrate/v4/source/iofs"

    log "github.com/sirupsen/logrus"
    _ "github.com/lib/pq" // PostgreSQL driver
    zmq "github.com/pebbe/zmq4"

)

var version = "development"
const taskTypeRange = 10
const taskValueRange = 100
const migrationsPath = "migrations"
//const errorLimit = 20

//go:embed migrations/*.sql
var migrationFiles embed.FS

var (
    tasksCounter = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "tasks_created_total",
            Help: "Total number of tasks created by the producer.",
        },
    )
)

// CB func to register prometheus data
func registerPromethesDataCallback() {
    log.Debug("Register prometheus tasksCounter counter")
    prometheus.MustRegister(tasksCounter)
}

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
    const DBName = "postgres"
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

    tasksCounter.Inc()
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
    timeout := 200 * time.Millisecond

    for attempt := 1; attempt <= retries; attempt++ {
        // Create a channel to signal when the send operation is done
        maxAttemtReached := make(chan error, 1)

        go func() {
            _, err := producer.SendBytes(msgBytes, 0)
            maxAttemtReached <- err
        }()

        // wait for either success/failure/timeout
        select {
            case err := <-maxAttemtReached:
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

func msgCreationLogic(workerID int, producer *zmq.Socket, config *Config, queries *db.Queries, stopTaskWorkers chan bool) {
        taskParams := &db.CreateTaskAndGetBacklogParams{
            Type:           int32(rand.Intn(taskTypeRange)),
            Value:          int32(rand.Intn(taskValueRange)),
            State:          db.TaskStatePending,
            CreationTime:   float64(time.Now().UTC().UnixMicro())/1e6, // get mirosec resolution
        }

        log.Debugf("[worker %d]creating task and writing to db", workerID)
        taskID, backlogCount, err := createTaskAndGetBacklog(queries, taskParams)
        if (nil != err) {
            log.Errorf("[worker %d]Failed to create task: %v, dropping task", workerID, err)
            continue
        } else if (backlogCount >= config.MaxBacklog) {
            log.Infof("Backlog reached its limit[%d], producer is closing", workerID, config.MaxBacklog)
            close(stopTaskWorkers)
            return
        }

        log.Infof("[worker %d]task %d created", workerID, taskID)
        log.Debugf("[worker %d]current backlog: %d", workerID, backlogCount)

        msg := &utils.TaskMsg {
            ID: taskID,
        }

        log.Debugf("[worker %d]serializing task %d to zmq", workerID, taskID)
        msgBytes, err := utils.SerializeTaskMsg(msg)
        if (nil != err) {
            log.Errorf("[worker %d]Failed to serialize msg[taskID %d]: %s, dropping", workerID, taskID, err)
            continue
        }

        log.Debugf("[worker %d]sending task %d to zmq", workerID, taskID)
        err = sendWithTimeout(producer, msgBytes)
        if (nil != err) {
            log.Errorf("[worker %d]Failed sending task %d to zeroMQ: %s, dropping", workerID, taskID, err)
            continue;
        }

        log.Debugf("[worker %d]Task %d has been created and sent, current backlog: %d", workerID, taskID, backlogCount)
}

// main task creation logic
func runTaskCreationLoop(workerID int, producer *zmq.Socket, config *Config, queries *db.Queries, ticker *time.Ticker, stopTaskWorkers chan bool, wg *sync.WaitGroup) {
    defer wg.Done()

    log.Infof("Starting worker %d main loop", workerID)
    for {
        select {
        case <-stopTaskWorkers:
            log.Info("Worker %d received termination signal", workerID)
            return
        case <-ticker.C:
            msgCreationLogic(workerID, producer, config, queries, stopTaskWorkers)
        }
    }
}

// Tasks routines dispatcher
func runTaskWorkers(producer *zmq.Socket, config *Config, queries *db.Queries, appWG *sync.WaitGroup, terminate chan struct{}) {
    defer appWGappWG.Done()

    log.Infof("Starting wrokers dispatcher, limitng to %d messages per sec", config.MsgProdRate)
    ticker := time.NewTicker(time.Second / time.Duration(config.MsgProdRate))
    defer ticker.Stop()
    stopTaskWorkers := make(chan bool)
    workersWG := sync.WaitGroup

    for i := 0; i < numWorker; i++ {
        workersWG.Add(1)
        go runTaskCreationLoop(i, producer *zmq.Socket, config *Config, queries *db.Queries, ticker, stopTaskWorkers, &workersWG)
    }

    workersWG.Wait()
    close(terminate)
    log.Debugf("Producer running finished")
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

    terminateApp := make(chan struct{})

    // goroutine to handle main loop logic
    wg.Add(1)
    go runTaskWorkers(producer, config, queries, &wg, terminateApp)

    // goroutine to handle prometheus server
    wg.Add(1)
    go utils.RunPrometheusServer(&config.MonitoringConfig, registerPromethesDataCallback, &wg, terminateApp)

    // goroutine to handle pprof server
    wg.Add(1)
    go utils.RunPprofServer(&config.MonitoringConfig, &wg, terminateApp)

    wg.Wait()

    log.Info("Producer has finished")
}
