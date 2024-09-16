package utils

import (
    "flag"
    "fmt"
    "os"
    "embed"
    "bytes"
    "strings"

    "encoding/json"

    zmq "github.com/pebbe/zmq4"
    log "github.com/sirupsen/logrus"
)

const zeroMQPort = 5555
const logsPath string = "/app/logs/"

var appName string
//go:embed config.json
var embeddedConfigFile embed.FS
var commMethodMap = map[string]zmq.Type{
    "push": zmq.PUSH,
    "pull": zmq.PULL,
}

var logVerMap = map[string]log.Level{
    "debug": log.DebugLevel,
    "info": log.InfoLevel,
    "error": log.ErrorLevel,
}

func HandleVersionFlag(version *string) {
    versionFlag := flag.Bool("v", false, "Print version and exit")
    flag.Parse()

    if *versionFlag {
        fmt.Printf("Version: %s\n", *version)
        os.Exit(0)
    }
}

func LoadConfig[T any](configFileName string, validateFunc func(*T) error) (*T, error) {
    log.Printf("Loading config from file: %s", configFileName)
    configFile, err := embeddedConfigFile.ReadFile(configFileName)
    if err != nil {
        return nil, fmt.Errorf("failed to open config file: %s", err)
    }

    var config T
    err = json.Unmarshal(configFile, &config)
    if err != nil {
        return nil, fmt.Errorf("failed decoding config file: %s", err)
    }

    if (nil != validateFunc) {
        err := validateFunc(&config)
        if (nil != err) {
            return nil, fmt.Errorf("validation failed: %s", err)
        }
    }

    log.Printf("Loaded configuration: %+v\n", config)
    return &config, nil
}

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

func setLoggingConsole() (func(), error) {
    log.SetOutput(os.Stdout)
    log.SetFormatter(&log.TextFormatter{
        FullTimestamp: true,
    })

    return nil, nil
}

func setLoggingFile() (func(), error) {
    path := fmt.Sprintf("%s/%s.log", logsPath, appName)
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

func SetAppName(name string) {
    appName = name
}

func ConnectToMQ(comm string, serverName *string) (*zmq.Socket, error) {
    commType, ok := commMethodMap[strings.ToLower(comm)]
    if (!ok) {
        return nil, fmt.Errorf("failed to create get ZeroMQ communication type[%s]", comm)
    }

    socket, err := zmq.NewSocket(commType)
    if (nil != err) {
        return nil, fmt.Errorf("failed to create ZeroMQ socket: %s", err)
    }

    if (nil != serverName) {
        socketAddr := fmt.Sprintf("tcp://%s:%d", *serverName, zeroMQPort)
        err = socket.Connect(socketAddr)
    } else {
        socketAddr := fmt.Sprintf("tcp://*:%d", zeroMQPort)
        err = socket.Bind(socketAddr)
    }
    if (nil != err) {
        return nil, fmt.Errorf("zmq cannot bind to port %d...", zeroMQPort)
    }

    log.Infof("ZMQ is running on port %d", zeroMQPort)
    return socket, nil
}

func SerializeTaskMsg(msg *TaskMsg) ([]byte, error) {
    var buf bytes.Buffer
    enc := json.NewEncoder(&buf)
    err := enc.Encode(msg)
    if err != nil {
        return nil, err
    }
    return buf.Bytes(), nil
}

func DeserializeTaskMsg(data []byte) (*TaskMsg, error) {
    var msg TaskMsg
    err := json.Unmarshal(data, &msg)
    if (nil != err) {
        return &msg, err
    }
    return &msg, nil
}

