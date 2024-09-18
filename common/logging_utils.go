package utils

import (
    "fmt"
    "os"
    "strings"
    "path/filepath"

    log "github.com/sirupsen/logrus"
)

const logRelativePath = "LOG_RELATIVE_PATH"
const baseAppPath = "BASE_APP_PATH"
const defaultAppPath = "/app"
const defaultLogRelativePath = "log"

var appName string
var logVerMap = map[string]log.Level{
    "debug": log.DebugLevel,
    "info": log.InfoLevel,
    "error": log.ErrorLevel,
}

func SetAppName(name string) {
    appName = name
}

// Set app logging level
//
// Params:
// -lvl - request level(supported: debug/info/error)
func SetLoggingLevel(lvl string) error {
    level, ok := logVerMap[strings.ToLower(lvl)]
    if (!ok) {
        return fmt.Errorf("invalid log level[%s] requeted", lvl)
    }

    log.SetFormatter(&log.TextFormatter{})
    log.SetLevel(level)
    return nil
}

type loggerFuncCB func() (func(), error)

// Set app logging to console
func setLoggingConsole() (func(), error) {
    log.SetOutput(os.Stdout)
    log.SetFormatter(&log.TextFormatter{
        FullTimestamp: true,
    })

    return nil, nil
}

// Set app logging to file
//
// Return:
// cleanup cb to call on app termination
func setLoggingFile() (func(), error) {
    appPath := getEnvVar(baseAppPath, defaultAppPath)
    logsRelPath := getEnvVar(logRelativePath, defaultLogRelativePath)

    path := filepath.Join(appPath, logsRelPath, appName)
    logFile, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
    if err != nil {
        return nil, err
    }

    log.SetOutput(logFile)
    log.SetFormatter(&log.TextFormatter{
        FullTimestamp: true,
    })

    cleanup := func() {
        if err := logFile.Close(); err != nil {
            log.Errorf("Failed to close log file: %v", err)
        }
    }

    return cleanup, nil
}

var logOutputMap = map[string]loggerFuncCB{
    "console": setLoggingConsole,
    "file":    setLoggingFile,
}

// Set logging output
//
// Params:
// output - logs output method (supported: file/console)
//
// Return:
// cleanup cb to call on app termination
func SetLoggingOutput(output string) (func(), error) {
    cb, ok := logOutputMap[output]
    if !ok {
        return nil, fmt.Errorf("invalid log output type[%s] requested", output)
    }

    cleanupCB, err := cb()
    if err != nil {
        return nil, err
    }

    return cleanupCB, nil
}
