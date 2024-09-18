package utils

import (
    "flag"
    "fmt"
    "os"
    "bytes"
    "strings"
    "path/filepath"
    "encoding/json"

    zmq "github.com/pebbe/zmq4"
    log "github.com/sirupsen/logrus"
)

const zeroMQPort = 5555

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

/*
 *decided not to go with embed here, to allow dynmic load of the file from container, for easier config update
//go:embed config.json
var embeddedConfigFile embed.FS
*/
var commMethodMap = map[string]zmq.Type{
    "push": zmq.PUSH,
    "pull": zmq.PULL,
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
