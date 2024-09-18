package utils

import (
    "flag"
    "fmt"
    "os"
    "bytes"
    "strings"
    "net/http"
    "path/filepath"
    "encoding/json"

    _ "net/http/pprof"

    "github.com/prometheus/client_golang/prometheus/promhttp"

    zmq "github.com/pebbe/zmq4"
    log "github.com/sirupsen/logrus"
)

const zeroMQPort = 5555
const logRelativePath = "LOG_RELATIVE_PATH"
const baseAppPath = "BASE_APP_PATH"
const defaultAppPath = "/app"
const defaultLogRelativePath = "log"

// get environment variable value
//
// Params:
// - key: env var name
// - defaultValue: default value to use if key not cound
//
// Returns:
// - env var value
func getEnvVar(key, defaultValue string) string {
    value, exists := os.LookupEnv(key)
    if !exists {
        return defaultValue
    }
    return value
}


var appName string

/*
 *decided not to go with embed here, to allow dynmic load of the file from container, for easier config update
//go:embed config.json
var embeddedConfigFile embed.FS
*/
var commMethodMap = map[string]zmq.Type{
    "push": zmq.PUSH,
    "pull": zmq.PULL,
}

var logVerMap = map[string]log.Level{
    "debug": log.DebugLevel,
    "info": log.InfoLevel,
    "error": log.ErrorLevel,
}

// Print app version and close the app when -v flag is received
//
// Params:
// - verison: app version string
func HandleVersionFlag(version *string) {
    versionFlag := flag.Bool("v", false, "Print version and exit")
    flag.Parse()

    if *versionFlag {
        fmt.Printf("Version: %s\n", *version)
        os.Exit(0)
    }
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

// Load app config according to config struct
//
// Params:
// -fileName - app file name in appPath
// -validateFunc - config validation functino callback(can be nil)
//
// Return:
// - config struct
func LoadConfig[T any](fileName string, validateFunc func(*T) error) (*T, error) {
    appPath := getEnvVar(baseAppPath, defaultAppPath)

    configFilePath := filepath.Join(appPath, fileName)
    log.Printf("Loading config from file: %s", configFilePath)

    /*
     * replace embed for easier config load
    configFile, err := embeddedConfigFile.ReadFile(configFilePath)
    if err != nil {
        return nil, fmt.Errorf("failed to open config file: %s", err)
    }
    */
    configFile, err := os.ReadFile(configFilePath)
    if err != nil {
        return nil, fmt.Errorf("failed to open config file: %s", err)
    }

    // Unmarshal the JSON configuration.
    var config T
    err = json.Unmarshal(configFile, &config)
    if err != nil {
        return nil, fmt.Errorf("failed decoding config file: %s", err)
    }

    // Validate the configuration.
    if validateFunc != nil {
        err := validateFunc(&config)
        if err != nil {
            return nil, fmt.Errorf("validation failed: %s", err)
        }
    }

    log.Printf("Loaded configuration: %+v\n", config)
    return &config, nil
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

func SetAppName(name string) {
    appName = name
}

// Connect to ZMQ as producer / consumer
//
// Params:
// - commMethos - ZMQ comminucation method (supported: push/pull)
// - hostName - for client - ZMQ host name to connect to
//
// Return:
// - socket to ZMQ
func ConnectToMQ(commMethod string, hostName *string) (*zmq.Socket, error) {
    log.Info("Connecting to ZMQueue")

    commType, ok := commMethodMap[strings.ToLower(commMethod)]
    if (!ok) {
        return nil, fmt.Errorf("failed to create get ZeroMQ communication type[%s]", commMethod)
    }

    socket, err := zmq.NewSocket(commType)
    if (nil != err) {
        return nil, fmt.Errorf("failed to create ZeroMQ socket: %s", err)
    }

    if (nil != hostName) {
        socketAddr := fmt.Sprintf("tcp://%s:%d", *hostName, zeroMQPort)
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
// Serialize msg to bytearray
func SerializeTaskMsg(msg *TaskMsg) ([]byte, error) {
    var buf bytes.Buffer
    enc := json.NewEncoder(&buf)
    err := enc.Encode(msg)
    if err != nil {
        return nil, err
    }
    return buf.Bytes(), nil
}

// Deserialize bytearray to msg
func DeserializeTaskMsg(data []byte) (*TaskMsg, error) {
    var msg TaskMsg
    err := json.Unmarshal(data, &msg)
    if (nil != err) {
        return &msg, err
    }
    return &msg, nil
}

// Register and run prometheus(blocking)
//
// Pamars:
// - config - monitoring configurations
// - registerCallbacks - callbacks func to register orinetheus data
func RunPrometheusServer(config *MontioringConfig, registerCallbacks func()) {
    registerCallbacks()

    log.Info("Starting prometheus server")
    http.Handle(config.PrometheusEndPoint, promhttp.Handler())
    err := http.ListenAndServe(fmt.Sprintf(":%d", config.PrometheusPort), nil)
    if (nil != err) {
        log.Fatalf("Failed to start Prometheus server: %v", err)
    }
}

// Register and run pprof server(blocking)
//
// Pamars:
// - config - monitoring configurations
func RunPprofServer(config *MontioringConfig) {
    log.Infof("Starting pprof on :%d", config.ProfilingPort)
    log.Warning(http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", config.ProfilingPort), nil))
}
