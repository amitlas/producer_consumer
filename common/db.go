package utils

import (
    "time"
    "fmt"

    "database/sql"

    log "github.com/sirupsen/logrus"
)

const SQLName = "postgres"
const maxRetriesDBConn = 60

// Connect to DB with retires, separated by 1sec, for 60sec
//
// params:
// - config - DB connection config
//
// return:
// - DB connection handler
func ConnectToDB(config *DBConfig) (*sql.DB, error) {
    log.Info("Connecting to DB")
    connCmd := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
    config.Host, config.Port, config.User, config.Password, config.DBName)

    sleepSec := 1
    maxRetries := maxRetriesDBConn / sleepSec
    var db *sql.DB
    var err error

    for i := 0; i < maxRetries; i++ {
        db, err = sql.Open(SQLName, connCmd)
        if err == nil {
            err = db.Ping()
            if err == nil {
                log.Println("Connected to DB successfully")
                break
            }
        }

        log.Printf("Failed to connect to DB[%d/%d]: %v. Retrying in %d seconds...", i, maxRetries, err, sleepSec)
        time.Sleep(time.Duration(sleepSec) * time.Second)
    }

    if (nil != err) {
        return nil, fmt.Errorf("failed to open connection to DB: %s", err)
    }

    return db, nil
}
