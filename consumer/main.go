package main

import (
    "fmt"
    "time"
    "sync"
    "context"
//    "net/http"

    "prod_cons/common"
    "prod_cons/db"

    "golang.org/x/time/rate"
 //   "github.com/prometheus/client_golang/prometheus"
  //  "github.com/prometheus/client_golang/prometheus/promhttp"

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
var taskFinishingQueue = make(chan int32, taskFinishingMsgBuffSize)



//todo:
//For each incoming task, have a final log which describes the tasks content and the calculated
//total sum for that task’s type. - final log - task is done, content, whats clculated total sum? kept by consumer? use
//mtx



//prometheus consumer
    //Track the number of tasks being processed and done in prometheus metrics
    //Track the number of tasks per task type
    //Track the total sum of the “value” field for each task type.







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
        limiter.Wait(context.Background())

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
        ID:        dbTask.ID,
        State:     dbTask.State,
        Value:     dbTask.Value,
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
        if (nil != err) {
            log.Errorf("Error fetching task %d from DB: %v", taskMsg.ID, err)
            continue
        }

        log.Debugf("Fetched Task from DB: %+v", task)
        // Pass the task to the next stage
        taskProcessingQueue <- convertToProcessingQueueChan(&task)
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
func dbFinishAction(queries *db.Queries, taskFinishingQueue <-chan int32, taskProcessingQueue <-chan *db.Task, wg *sync.WaitGroup) {
    defer wg.Done()

    for task_id := range taskFinishingQueue {
        ctx := context.Background()
        err := queries.UpdateTaskToState(ctx, db.UpdateTaskToStateParams{
            ID:         int32(task_id),
            State:      db.TaskStateComplete,
        })
        if (nil != err) {
            log.Errorf("Failed updating task %d to finished: %v", task_id, err)
            continue
        }

        log.Infof("Updated DB task %d to finished", task_id)
    }
}

// start the task finish workers queue
// this is responsible to update db once task is done
func startTaskFinishWorkers(queries *db.Queries, numWorkers int, wg *sync.WaitGroup) {
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go dbFinishAction(queries, taskFinishingQueue, taskProcessingQueue, wg)
    }
}

// run task
func run_task(task *db.Task, taskFinishingQueue chan<- int32) {
    log.Debugf("started working on task %d", task.ID)
    time.Sleep(time.Duration(task.Value) * time.Millisecond)
    log.Debugf("finished working on task %d", task.ID)
}

// read task to process from taskProcessingQueue and handle it, when task is done, pass its id to taskFinishingQueue
func TaskHandleAction(taskProcessingQueue <-chan *db.Task, taskFinishingQueue chan<- int32, wg *sync.WaitGroup) {
    defer wg.Done()
    for task := range taskProcessingQueue {
        log.Debugf("handler queue working on task %d", task.ID)
        run_task(task, taskFinishingQueue)
        taskFinishingQueue <- task.ID
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

    wg.Wait()
}
