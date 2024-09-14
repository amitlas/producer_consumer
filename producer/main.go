package main
/*
todo:
-write config file
-logging - debug level + json/console
-version command line arg
-move stuff to common
-fix the builder to be separate
-tests
-profiling port + handling
-grafana
-prometheus
*/


import (
	"fmt"
	"os"
	"time"
	"bytes"
	"encoding/gob"
	"math/rand"
	"database/sql"
	"encoding/json"

        log "github.com/sirupsen/logrus"
	_ "github.com/lib/pq" // PostgreSQL driver
	zmq "github.com/pebbe/zmq4"
)

type Task struct {
	ID            int
	Type          int
	Value         int
	State         string
	CreationTime  float64
	LastUpdateTime float64
}

type DBConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
}

type Config struct {
	ZeroMQComm		string		`json:"zero_mq_protocol"`
	MaxBacklog		int		`json:"max_backlog"`
	LoggingLevel		string		`json:"logging_level"`
	LoggingType		string		`json:"logging_type"`
	MsgProdRate		int		`json:"msg_prod_rate"`
	DBConnConfig		DBConfig	`json:"db_conn_config"`
	//ProfilingPort
	//PrometheusPort
	//PrometheusEndpoint
}

type TaskMsg struct {
	ID int
}

var commMethodMap = map[string]zmq.Type{
	"push": zmq.PUSH,
}


const configFileName = "/app/config.json"
const ZeroMQPort = 5555
const SQLName = "postgres"
const taskTypeRange = 10
const taskValueRange = 100
const errorLimit = 20
const taskStatePending = "pending"
const taskStateProcessing = "processing"
const taskStateDone = "done"

func loadConfig() (*Config, error) {
	// Read config.json
	configFile, err := os.Open(configFileName)
	if (nil != err ) {
		return nil, fmt.Errorf("failed to open config file: %s", err)
	}
	defer configFile.Close()

	// Parse the config.json
	var config Config
	decoder := json.NewDecoder(configFile)
	err = decoder.Decode(&config)
	if (nil != err) {
		return nil, fmt.Errorf("failed decoding config file: %s", err)
	}

	if (config.MsgProdRate < 0) {
		return nil, fmt.Errorf("invalid config")
	}

	log.Debugf("Loaded configuration: %+v\n", config)

	return &config, nil
}

func connectToMQ(config *Config) (*zmq.Socket, error) {
	commType, ok := commMethodMap[config.ZeroMQComm]
	if (!ok) {
		return nil, fmt.Errorf("unsupported ZeroMQ comm method: %s", config.ZeroMQComm)
	}

	producer, err := zmq.NewSocket(commType)
	if (nil != err) {
		return nil, fmt.Errorf("failed to create ZeroMQ socket: %s", err)
	}

	// Bind the to TCP port ZeroMQPort
	err = producer.Bind(fmt.Sprintf("tcp://*:%d", ZeroMQPort))
	if (nil != err) {
		return nil, fmt.Errorf("producer is running on port %d...", ZeroMQPort)
	}

	log.Infof("Publisher is running on port %d", ZeroMQPort)
	return producer, nil
}

func connectToDB(config *DBConfig) (*sql.DB, error) {
	log.Info("Connecting to DB")
	connCmd := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Host, config.Port, config.User, config.Password, config.DBName)


	sleepSec := 1
	maxRetries := 60 / sleepSec
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

	log.Info("DB connected successfully")
	return db, nil
}

func initTasksTable(db *sql.DB) error {
	log.Info("Create DB Table")

	// Create the enum type if it doesn't exist
	createEnumSQL := fmt.Sprintf(`
		DO $$ BEGIN
		    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'task_state') THEN
			CREATE TYPE task_state AS ENUM ('%s', '%s', '%s');
		    END IF;
		END $$;`, taskStatePending, taskStateProcessing, taskStateDone)

	_, err := db.Exec(createEnumSQL)
	if (nil != err) {
		return fmt.Errorf("failed to create task_state enum: %s", err)
	}

	// Create the table if it doesn't exist
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS tasks (
		id SERIAL PRIMARY KEY,
		type INT NOT NULL CHECK (type >= 0 AND type <= 9),
		value INT NOT NULL CHECK (value >= 0 AND value <= 99),
		state task_state NOT NULL DEFAULT 'pending',
		creation_time DOUBLE PRECISION NOT NULL,
		last_update_time DOUBLE PRECISION NOT NULL
	);`

	_, err = db.Exec(createTableSQL)
	if (nil != err) {
		return fmt.Errorf("failed to create tasks table: %v", err)
	}

	log.Info("Table 'tasks' created or already exists")
	return nil
}

func SerializeTaskMsg(msg *TaskMsg) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(msg)
	if (nil != err) {
		return nil, fmt.Errorf("failed to encode task msg: %s", err)
	}

	return buf.Bytes(), nil
}

// used backlog count as a 'soft limit', didnt use transactions to reduce consumers clogging
func createTaskAndGetBacklog(db *sql.DB, task *Task) (int, int, error) {
	query := `
		WITH inserted_task AS (
			INSERT INTO tasks (type, value, state, creation_time, last_update_time)
			VALUES ($1, $2, $3::task_state, $4, $5)
			RETURNING id
		)
		SELECT 
			(SELECT id FROM inserted_task) AS task_id,
			(SELECT COUNT(*) FROM tasks WHERE state = $6) AS backlog_count
	`

	task.LastUpdateTime = float64(time.Now().UTC().Unix())

	var taskID, backlogCount int
	err := db.QueryRow(
		query,
		task.Type,
		task.Value,
		task.State,
		task.CreationTime,
		task.LastUpdateTime,
		taskStatePending,
	).Scan(&taskID, &backlogCount)

	if (nil != err) {
		return 0, 0, err
	}

	return taskID, backlogCount, nil
}

func sendWithTimeout(producer *zmq.Socket, msgBytes []byte) error {
	retries := 5
	timeout := 1 * time.Second

	for attempt := 1; attempt <= retries; attempt++ {
		// Create a channel to signal when the send operation is done
		done := make(chan error, 1)

		go func() {
			_, err := producer.SendBytes(msgBytes, 0)
			done <- err
		}()

		select {
		case err := <-done:
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


func main() {

	log.Info("Loading config")
	config, err := loadConfig()
	if (nil != err) {
		log.Fatalf("Failed loading config: %s", err)
	}

	log.Info("Connecting to Producer API")
	producer, err := connectToMQ(config)
	if (nil != err) {
		log.Fatalf("Failed connection to producer: %s", err)
	}
	defer producer.Close()

	db, err := connectToDB(&config.DBConnConfig)
	if (nil != err) {
		log.Fatalf("Failed connecting to DB: %s", err)
	}
	defer db.Close()

	err = initTasksTable(db)
	if (nil != err) {
		log.Fatalf("Failed creating DB task table: %s", err)
	}

	ticker := time.NewTicker(time.Second / time.Duration(config.MsgProdRate))

	defer ticker.Stop()
	errCnt := 0

	log.Info("++++Starting ticker")
	for range ticker.C {
		task := &Task{
			Type:		rand.Intn(taskTypeRange),
			Value:		rand.Intn(taskValueRange),
			State:		taskStatePending,
			CreationTime:	float64(time.Now().UTC().Unix()),
		}

		log.Info("++++creating task and writing to db")
		taskID, backlogCount, err := createTaskAndGetBacklog(db, task)
		if (nil != err) {
			errCnt += 1
			log.Errorf("Failed to create task: %v, dropping", err)
			if (errCnt > errorLimit) {
				log.Fatalf("Producer reached max error limit[%d], closing the app", errorLimit)
			}
			continue
		}
		log.Infof("++++task %d created, backlog: %d", taskID, backlogCount)

		msg := &TaskMsg {
			ID: taskID,
		}

		log.Infof("++++serializing task %d to zmq", taskID)
		msgBytes, err := SerializeTaskMsg(msg)
		if (nil != err) {
			errCnt += 1
			log.Errorf("Failed to serialize msg[taskID %d]: %s, dropping", taskID, err)
			if (errCnt > errorLimit) {
				log.Fatalf("Producer reached max error limit[%d], closing the app", errorLimit)
			}
			continue
		}
		log.Infof("++++sending task %d to zmq", taskID)

		err = sendWithTimeout(producer, msgBytes)
		if (nil != err) {
			errCnt += 1
			log.Errorf("Failed sending task %d to zeroMQ: %s, dropping", taskID, err)
			if (errCnt > errorLimit) {
				log.Fatalf("Producer reached max error limit[%d], closing the app", errorLimit)
			}
			continue
		}
		log.Infof("++++sending task %d sent to zmq", taskID)

		log.Debugf("Task %d has been created and sent, current backlog: %d", taskID, backlogCount)

		log.Infof("+++++++++Task %d has been created and sent, current backlog: %d", taskID, backlogCount)
		if (backlogCount >= config.MaxBacklog) {
			log.Infof("Backlog reached its limit[%d], producer is closing", config.MaxBacklog)
			break
		}
	}

	log.Info("Producer has finished")
}
