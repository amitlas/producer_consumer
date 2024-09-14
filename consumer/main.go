package main

import (
    "fmt"
    "time"
    "embed"
    "sync"
    "context"

    "prod_cons/common"
    "prod_cons/db"

    "golang.org/x/time/rate"

    zmq "github.com/pebbe/zmq4"
    log "github.com/sirupsen/logrus"
    _ "github.com/lib/pq" // PostgreSQL driver
)

//go:embed config.json
var embeddedConfigFile embed.FS

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


func validateConfig(config *Config) error {
    if config.RateLimit <= 0 {
        return fmt.Errorf("invalid config: invalid rate limit: [%d]", config.RateLimit)
    }

    return nil
}


func consumeFromZMQ(socket *zmq.Socket, limiter *rate.Limiter) {
    for {
        limiter.Wait(context.Background())

        msg, err := socket.RecvBytes(0)
        if (nil != err) {
            log.Errorf("Error receiving message from ZeroMQ: %v", err)
            continue
        }

        log.Debug("recied msg from zeroMQ")
        log.Info("++++recied msg from zeroMQ")
        taskReadingQueue <- msg
    }
}

func convertToProcessingQueueChan(dbTask *db.GetTaskByIDUpdateStateRow) *db.Task {
    return &db.Task{
        ID:        dbTask.ID,
        State:     dbTask.State,
        Value:     dbTask.Value,
    }
}

func dbReaderAction(queries *db.Queries, taskReadingQueue <-chan []byte, taskProcessingQueue chan<- *db.Task, wg *sync.WaitGroup) {
    defer wg.Done()

    for msg := range taskReadingQueue {
        taskMsg, err := utils.DeserializeTaskMsg(msg)
        if (nil != err) {
            log.Errorf("Error deserializing message from ZeroMQ: %v", err)
            continue
        }

        log.Debugf("Received Task ID: %d", taskMsg.ID)
        log.Infof("++++Received Task ID: %d", taskMsg.ID)

        ctx := context.Background()
        task, err := queries.GetTaskByIDUpdateState(ctx, db.GetTaskByIDUpdateStateParams{
            ID:			int32(taskMsg.ID),
            State:		db.TaskStateRunning,
        })
        if (nil != err) {
            log.Errorf("Error fetching task %d from DB: %v", taskMsg.ID, err)
            continue
        }

        log.Debugf("Fetched Task from DB: %+v", task)
        log.Infof("++++Fetched Task from DB: %+v", task)


        // Pass the task to the next stage
        taskProcessingQueue <- convertToProcessingQueueChan(&task)
    }
}

func startDBreaders(queries *db.Queries, numWorkers int, wg *sync.WaitGroup) {
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go dbReaderAction(queries, taskReadingQueue, taskProcessingQueue, wg)
    }
}

func dbFinishAction(queries *db.Queries, taskFinishingQueue <-chan int32, taskProcessingQueue <-chan *db.Task, wg *sync.WaitGroup) {
    defer wg.Done()

    for task_id := range taskFinishingQueue {
        ctx := context.Background()
        err := queries.UpdateTaskToState(ctx, db.UpdateTaskToStateParams{
            ID:			int32(task_id),
            State:		db.TaskStateComplete,
        })
        if (nil != err) {
            log.Errorf("Failed updating task %d to finished: %v", task_id, err)
            continue
        }

        log.Debugf("Updated DB task %d to finished", task_id)
    }
}

func startTaskFinishWorkers(queries *db.Queries, numWorkers int, wg *sync.WaitGroup) {
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go dbFinishAction(queries, taskFinishingQueue, taskProcessingQueue, wg)
    }
}

func run_task(task *db.Task, taskFinishingQueue chan<- int32) {
    log.Debugf("started working on task %d", task.ID)
    time.Sleep(time.Duration(task.Value) * time.Millisecond)
    log.Debugf("finished working on task %d", task.ID)

    taskFinishingQueue <- task.ID
}

func TaskHandleAction(taskProcessingQueue <-chan *db.Task, taskFinishingQueue chan<- int32, wg *sync.WaitGroup) {
    defer wg.Done()
    for task := range taskProcessingQueue {
        run_task(task, taskFinishingQueue)
    }
}

func startTaskHandlingWorkers(numWorkers int, wg *sync.WaitGroup) {
    for i:=0; i<numWorkers; i++ {
        wg.Add(1)
        go TaskHandleAction(taskProcessingQueue, taskFinishingQueue, wg)
    }

}

func main() {
    utils.HandleVersionFlag(&version)

    config, err := utils.LoadConfig[Config]("config.json", validateConfig)
    if (nil != err) {
        log.Fatalf("Failed loading config: %s", err)
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
    startDBreaders(queries, numDBReaders, &wg)
    startTaskHandlingWorkers(numActionWorkers, &wg)
    startTaskFinishWorkers(queries, numTaskFinishers, &wg)

    limiter := rate.NewLimiter(rate.Limit(config.RateLimit), 1/*sec*/)
    go consumeFromZMQ(consumer, limiter)

    wg.Wait()
}
