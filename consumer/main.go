package main

import (
    "fmt"
    "time"
    "sync"
    "context"

    "prod_cons/common"
    "prod_cons/db"

    "golang.org/x/time/rate"
    "github.com/prometheus/client_golang/prometheus"

    zmq "github.com/pebbe/zmq4"
    log "github.com/sirupsen/logrus"
    _ "github.com/lib/pq" // PostgreSQL driver
)

const taskReadingMsgBuffSize = 100
const taskProcessingMsgBuffSize = 100
const taskFinishingMsgBuffSize = 100
const numDBReaders = 100
const numActionWorkers = 100
const numTaskFinishers = 100

var version = "development"

var taskReadingQueue = make(chan []byte, taskReadingMsgBuffSize)
var taskProcessingQueue = make(chan *db.Task, taskProcessingMsgBuffSize)
var taskFinishingQueue = make(chan *db.Task, taskFinishingMsgBuffSize)
var onceTerminateApp sync.Once

var (
    totalProcessedValueByType = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "total_processed_value_by_type",
            Help: "Total number of tasks value processed by the consumer per task type.",
        },
        []string{"type"},
    )
)

var (
    totalProcessedTasksByType = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "total_processed_tasks_by_type",
            Help: "Total number of tasks processed by the consumer per task type.",
        },
        []string{"type"},
    )
)

// CB func to register prometheus data
func registerPromethesDataCallback() {
    log.Debug("Register prometheus tasksCounter counter")
    prometheus.MustRegister(totalProcessedValueByType)
    prometheus.MustRegister(totalProcessedTasksByType)
}

// configuration validator callback
func validateConfig(config *Config) error {
    if config.RateLimit <= 0 {
        return fmt.Errorf("invalid config: invalid rate limit: [%d]", config.RateLimit)
    }

    return nil
}

// read data from ZMQ, pass it to worker for msg parsing(deserializing) and release consumer
// queue to keep pulling data. pass to worker via taskReadingQueue
func processMsgsWorker(socket *zmq.Socket, limiter *rate.Limiter) {
    for {
        err := limiter.Wait(context.Background())
        if (nil != err) {
            log.Errorf("Error waiting on limiter: %v", err)
            continue
        }

        msg, err := socket.RecvBytes(0)
        if (nil != err) {
            log.Errorf("Error receiving message from ZeroMQ: %v", err)
            continue
        }

        log.Debug("recied msg from zeroMQ")
        taskReadingQueue <- msg
    }
}

// convert DB format to local task data format
func convertToProcessingQueueChan(dbTask *db.GetTaskByIDUpdateStateRow) *db.Task {
    return &db.Task{
        ID:             dbTask.ID,
        Type:           dbTask.Type,
        State:          dbTask.State,
        Value:          dbTask.Value,
        CreationTime:   dbTask.CreationTime,
        LastUpdateTime: dbTask.LastUpdateTime,
    }
}

// deserialize msg form zmq bytearray(via taskReadingQueue) to msg id and get data from db
// after fetching task data pass it to taskProcessingQueue for task handling
func dbReaderAction(workerID int, msg []byte, queries *db.Queries, taskProcessingQueue chan<- *db.Task) {
        taskMsg, err := utils.DeserializeTaskMsg(msg)
        if (nil != err) {
            log.Errorf("[dbReaderWorker: %d]Error deserializing message from ZeroMQ: %v", workerID, err)
            return
        }

        log.Debugf("[dbReaderWorker: %d]Received Task ID: %d", workerID, taskMsg.ID)
        ctx := context.Background()
        task, err := queries.GetTaskByIDUpdateState(ctx, db.GetTaskByIDUpdateStateParams{
            ID:         int32(taskMsg.ID),
            State:      db.TaskStateRunning,
        })
        if (nil != err) {
            log.Errorf("[dbReaderWorker: %d]Error fetching task %d from DB: %v", workerID, taskMsg.ID, err)
            return
        }

        // Pass the task to the next stage
        taskProcessingQueue <-convertToProcessingQueueChan(&task)
}

// reader action wrapper to handler terminate app
func dbReaderActionWrapper(workerID int, queries *db.Queries, taskReadingQueue <-chan []byte, taskProcessingQueue chan<- *db.Task, wg *sync.WaitGroup, terminateApp chan struct{}) {
    log.Errorf("+++starting db reader %d", workerID)
    defer wg.Done()

    for {
        select {
        case msg, ok := <-taskReadingQueue:
            if !ok {
                log.Infof("[dbReaderWorker: %d]taskReadingQueue is not available, closing handler", workerID)
                log.Errorf("+++failed reding reading quueue return workerc %d", workerID)
                return
            }
            dbReaderAction(workerID, msg, queries, taskProcessingQueue)
            log.Errorf("+++finished reading action on orkerc %d", workerID)
        case <-terminateApp:
            log.Errorf("+++terimante app terminating db reader %d", workerID)
            log.Infof("[dbReaderWorker: %d]Received terminateApp signal, closing handler.", workerID)
            return
        }
    }
}

// start the task DB reading workers queue
// this is responsible to fetch task data from db, according to task id received from taskReadingQueue
// and pass task data to taskProcessingQueue
func startDBreaders(queries *db.Queries, numWorkers int, terminateApp chan struct{}) *sync.WaitGroup {
    log.Debug("Starting DB readers workers")

    log.Error("+++starting db regers")
    var wg sync.WaitGroup
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go dbReaderActionWrapper(i, queries, taskReadingQueue, taskProcessingQueue, &wg, terminateApp)
    }

    return &wg
}

// task finisher worker logic
// update the db task state to completed

func dbFinishAction(workerID int, task *db.Task, queries *db.Queries) {
    ctx := context.Background()
    err := queries.UpdateTaskToState(ctx, db.UpdateTaskToStateParams{
        ID:         int32(task.ID),
        State:      db.TaskStateComplete,
    })
    if (nil != err) {
        log.Errorf("[taskFinishWorker %d]Failed updating task %d to finished: %v", workerID,  task.ID, err)
        return
    }

    log.Infof("[taskFinishWorker %d]Updated DB task %d to finished", workerID, task.ID)
}

// finish action wrapper to stop on terminate app
func dbFinishActionWrapper(workerID int, queries *db.Queries, taskFinishingQueue <-chan *db.Task, wg *sync.WaitGroup, terminateApp chan struct{}) {
    defer wg.Done()

    for {
        select {
        case task, ok := <-taskFinishingQueue:
            if !ok {
                log.Infof("[taskFinishWorker %d]taskFinishingQueue is not available, closing handler", workerID)
                return
            }
            dbFinishAction(workerID, task, queries)
        case <-terminateApp:
            log.Infof("[taskFinishWorker %d]Received terminateApp signal, closing handler.", workerID)
            return
        }
    }
}

// start the task finish workers queue
// this is responsible to update db once task is done
func startTaskFinishWorkers(queries *db.Queries, numWorkers int, terminateApp chan struct{}) *sync.WaitGroup {
    log.Debug("Starting task finisher workers")
    var wg sync.WaitGroup

    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go dbFinishActionWrapper(i, queries, taskFinishingQueue, &wg, terminateApp)
    }
    return &wg
}

// run task
func run_task(task *db.Task) {
    log.Debugf("started working on task %d", task.ID)
    time.Sleep(time.Duration(task.Value) * time.Millisecond)
    log.Debugf("finished working on task %d", task.ID)
}

// read task to process from taskProcessingQueue and handle it, when task is done, pass its id to taskFinishingQueue
func TaskHandleAction(workerID int, task *db.Task, taskFinishingQueue chan<- *db.Task) {
    log.Debugf("[taskhandlerworker :%d]handler queue working on task %d", workerID, task.ID)

    // atomic actions
    totalProcessedValueByType.WithLabelValues(fmt.Sprintf("%d", task.Type)).Add(float64(task.Value))
    totalProcessedTasksByType.WithLabelValues(fmt.Sprintf("%d", task.Type)).Inc()

    run_task(task)
    taskFinishingQueue <- task
}

// task handler wrapper to stop on terminate app
func TaskHandleActionWrapper(workerID int, taskProcessingQueue <-chan *db.Task, taskFinishingQueue chan<- *db.Task, wg *sync.WaitGroup, terminateApp chan struct{}) {
    defer wg.Done()

    for {
        select {
        case task, ok := <-taskProcessingQueue:
            if !ok {
                log.Infof("[taskhandlerworker :%d]taskProcessingQueue is not available, closing handler", workerID)
                return
            }
            TaskHandleAction(workerID, task, taskFinishingQueue)
        case <-terminateApp:
            log.Infof("[taskhandlerworker :%d]Received terminateApp signal, closing handler.", workerID)
            return
        }
    }
}

// start the task handlers workers pool
// this is responsible to run the task
func startTaskHandlingWorkers(numWorkers int, terminateApp chan struct{}) *sync.WaitGroup{
    log.Debug("Starting task handlers workers")
    var wg sync.WaitGroup
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go TaskHandleActionWrapper(i, taskProcessingQueue, taskFinishingQueue, &wg, terminateApp)
    }
    return &wg
}

func closeTerminateAppChannel(id string, ch chan struct{}) {
    onceTerminateApp.Do(func() {
        log.Infof("[%s]Marking terminate app channel", id)
        close(ch)
    })
}

func main() {
    utils.HandleVersionFlag(&version)
    utils.SetAppName("consumer")

    config, err := utils.LoadConfig[Config]("config.json", validateConfig)
    if (nil != err) {
        log.Fatalf("Failed loading config: %s", err)
    }

    log.Errorf("+++set log level")
    err = utils.SetLoggingLevel(config.Logging.Level)
    if (nil != err) {
        log.Fatalf("Failed to set logging: %s", err)
    }

    log.Errorf("+++set log outpu")
    cleanupCB, err := utils.SetLoggingOutput(config.Logging.Output)
    if (nil != err) {
        log.Fatalf("Failed to set logging: %s", err)
    }
    if (nil != cleanupCB) {
        defer cleanupCB()
    }

    log.Errorf("+++conntec to db")
    dbConn, err := utils.ConnectToDB(&config.DBConnConfig)
    if (nil != err) {
        log.Fatalf("Failed connecting to DB: %s", err)
    }
    defer dbConn.Close()

    log.Errorf("+++open zmq")
    log.Info("Connecting to Consumer queue")
    consumer, err := utils.ConnectToMQ(config.ZeroMQComm, &config.ZMQHostName)
    if (nil != err) {
        log.Fatalf("Failed connection to consumer: %s", err)
    }
    defer consumer.Close()

    log.Errorf("+++create queries")
    queries := db.New(dbConn)

    var serversWG sync.WaitGroup
    // not very useful, as i dont have defined terminating scenario, but available for use

    log.Errorf("+++create terminate chanel")
    terminateApp := make(chan struct{})

    log.Errorf("+++create threadpools")
    // start worker threads
    readersWG := startDBreaders(queries, numDBReaders, terminateApp)
    handlersWG := startTaskHandlingWorkers(numActionWorkers, terminateApp)
    workersWG := startTaskFinishWorkers(queries, numTaskFinishers, terminateApp)

    // set rate limiter for msg handling
    limiter := rate.NewLimiter(rate.Limit(config.RateLimit), 1/*sec*/)
log.Error("+++running msg workers")
    // handle requests
    go processMsgsWorker(consumer, limiter)

log.Error("+++prom server")
    // goroutine to handle prometheus server
    go utils.RunPrometheusServer(&config.MonitoringConfig, registerPromethesDataCallback, &serversWG, terminateApp)

log.Error("+++runnign pprof server server")
    // goroutine to handle pprof server
    go utils.RunPprofServer(&config.MonitoringConfig, &serversWG, terminateApp)

    workersDone := make(chan string)

    workersWGs := map[string]*sync.WaitGroup{
        "readersWG":  readersWG,
        "handlersWG": handlersWG,
        "workersWG":  workersWG,
    }

    // Launch a goroutine to wait for each WaitGroup
    for id, wg := range workersWGs {
        go func(id string, wg *sync.WaitGroup) {
            wg.Wait()
            workersDone <- id // Send the identifier of the finished WaitGroup
        }(id, wg)
    }

    <-workersDone
    serversWG.Wait()

}
