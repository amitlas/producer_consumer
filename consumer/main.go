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
func consumeFromZMQ(socket *zmq.Socket, limiter *rate.Limiter) {
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
func dbReaderAction(queries *db.Queries, taskReadingQueue <-chan []byte, taskProcessingQueue chan<- *db.Task, wg *sync.WaitGroup) {
    defer wg.Done()

    for msg := range taskReadingQueue {
        taskMsg, err := utils.DeserializeTaskMsg(msg)
        if (nil != err) {
            log.Errorf("Error deserializing message from ZeroMQ: %v", err)
            continue
        }

        log.Debugf("Received Task ID: %d", taskMsg.ID)
        ctx := context.Background()
        task, err := queries.GetTaskByIDUpdateState(ctx, db.GetTaskByIDUpdateStateParams{
            ID:         int32(taskMsg.ID),
            State:      db.TaskStateRunning,
        })
        log.Errorf("+++read task: %d, type: %d", task.ID, task.Type)
        if (nil != err) {
            log.Errorf("Error fetching task %d from DB: %v", taskMsg.ID, err)
            continue
        }

        // Pass the task to the next stage
        taskProcessingQueue <-convertToProcessingQueueChan(&task)
    }
}


// start the task DB reading workers queue
// this is responsible to fetch task data from db, according to task id received from taskReadingQueue
// and pass task data to taskProcessingQueue
func startDBreaders(queries *db.Queries, numWorkers int, wg *sync.WaitGroup) {
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go dbReaderAction(queries, taskReadingQueue, taskProcessingQueue, wg)
    }
}

// task finisher worker logic
// update the db task state to completed
func dbFinishAction(queries *db.Queries, taskFinishingQueue <-chan *db.Task, wg *sync.WaitGroup) {
    defer wg.Done()

    for task := range taskFinishingQueue {
        ctx := context.Background()
        err := queries.UpdateTaskToState(ctx, db.UpdateTaskToStateParams{
            ID:         int32(task.ID),
            State:      db.TaskStateComplete,
        })
        if (nil != err) {
            log.Errorf("Failed updating task %d to finished: %v", task.ID, err)
            continue
        }

        // atomic actions

        log.Infof("Updated DB task %d to finished", task.ID)
    }
}

// start the task finish workers queue
// this is responsible to update db once task is done
func startTaskFinishWorkers(queries *db.Queries, numWorkers int, wg *sync.WaitGroup) {
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go dbFinishAction(queries, taskFinishingQueue, wg)
    }
}

// run task
func run_task(task *db.Task, taskFinishingQueue chan<- *db.Task) {
    log.Debugf("started working on task %d", task.ID)
    time.Sleep(time.Duration(task.Value) * time.Millisecond)
    log.Debugf("finished working on task %d", task.ID)
}

// read task to process from taskProcessingQueue and handle it, when task is done, pass its id to taskFinishingQueue
func TaskHandleAction(taskProcessingQueue <-chan *db.Task, taskFinishingQueue chan<- *db.Task, wg *sync.WaitGroup) {
    defer wg.Done()
    for task := range taskProcessingQueue {
        log.Debugf("handler queue working on task %d", task.ID)
        log.Errorf("++working on tsk %d ,type : %d", task.ID,task.Type)

        // atomic actions
        totalProcessedValueByType.WithLabelValues(fmt.Sprintf("%d", task.Type)).Add(float64(task.Value))
        totalProcessedTasksByType.WithLabelValues(fmt.Sprintf("%d", task.Type)).Inc()
        log.Errorf("++update total value by type for task %d with %d, new val: %f", task.Type, task.Value, totalProcessedValueByType.WithLabelValues(fmt.Sprintf("%d", task.Type)))

        run_task(task, taskFinishingQueue)
        taskFinishingQueue <- task
    }
}

// start the task handlers workers pool
// this is responsible to run the task
func startTaskHandlingWorkers(numWorkers int, wg *sync.WaitGroup) {
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go TaskHandleAction(taskProcessingQueue, taskFinishingQueue, wg)
    }

}

func main() {
    utils.HandleVersionFlag(&version)
    utils.SetAppName("consumer")

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

    log.Info("Connecting to Consumer queue")
    consumer, err := utils.ConnectToMQ(config.ZeroMQComm, &config.ZMQHostName)
    if (nil != err) {
        log.Fatalf("Failed connection to consumer: %s", err)
    }
    defer consumer.Close()

    queries := db.New(dbConn)

    var wg sync.WaitGroup
    // start worker threads
    startDBreaders(queries, numDBReaders, &wg)
    startTaskHandlingWorkers(numActionWorkers, &wg)
    startTaskFinishWorkers(queries, numTaskFinishers, &wg)

    // set rate limiter for msg handling
    limiter := rate.NewLimiter(rate.Limit(config.RateLimit), 1/*sec*/)

    // handle requests
    go consumeFromZMQ(consumer, limiter)

    // goroutine to handle prometheus server
    go utils.RunPrometheusServer(&config.MonitoringConfig, registerPromethesDataCallback)

    // goroutine to handle pprof server
    go utils.RunPprofServer(&config.MonitoringConfig)

    wg.Wait()
}
