package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	zmq "github.com/pebbe/zmq4"
)

// Config structure to match config.json
type Config struct {
	SubscribeAll bool `json:"subscribe_all"`
}

func main() {
	// Read config.json
	configFile, err := os.Open("config.json")
	if err != nil {
		log.Fatalf("Failed to open config file: %s", err)
	}
	defer configFile.Close()

	// Parse the config.json
	byteValue, _ := io.ReadAll(configFile)
	var config Config
	err = json.Unmarshal(byteValue, &config)
	if err != nil {
	    log.Fatalf("Failed to unmarshal JSON: %v", err)
	}


	// Create a new ZeroMQ subscriber socket
	subscriber, _ := zmq.NewSocket(zmq.SUB)
	defer subscriber.Close()

	// Connect to the producer
	err = subscriber.Connect("tcp://producer:5555")
	if err != nil {
	    log.Fatalf("Failed to connect subscriber: %v", err)
	}


	// Subscribe to all messages if configured
	if config.SubscribeAll {
		err = subscriber.SetSubscribe("")
		if err != nil {
		    log.Fatalf("Failed to set subscription: %v", err)
		}
	}


	fmt.Println("Consumer connected to producer, waiting for messages...")

	// Receive and print messages
	for {
		msg, _ := subscriber.Recv(0)
		fmt.Println("Received:", msg)
		time.Sleep(time.Second)
	}
}
