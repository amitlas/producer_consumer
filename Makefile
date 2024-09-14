DOCKER_COMPOSE_FILE=docker-compose.yml
SCRIPT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
GO_PRODUCER_DIR=$(SCRIPT_DIR)/producer
GO_CONSUMER_DIR=./consumer
# GO_CONSUMER_DIR=$(SCRIPT_DIR)/consumer
PRODUCER_BINARY=$(GO_PRODUCER_DIR)/producer
CONSUMER_BINARY=$(GO_CONSUMER_DIR)/consumer

build: producer consumer

mod_tidy_producer:
	@echo "Updating GO modules"
	cd $(GO_PRODUCER_DIR) && go mod tidy

mod_tidy_consumer:
	@echo "Updating GO modules"
	cd $(GO_CONSUMER_DIR) && go mod tidy

producer: mod_tidy_producer
	@echo "Building producer..."
	cd $(GO_PRODUCER_DIR) && docker-compose build

consumer: mod_tidy_consumer
	@echo "Building consumer..."
	cd $(GO_CONSUMER_DIR) && docker-compose build

clean:
	@echo "Cleaning up binaries..."
	rm -f $(PRODUCER_BINARY)
	rm -f $(CONSUMER_BINARY)

run: stop build
	@echo "Running Docker Compose..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) up 

stop:
	@echo "Stopping Docker Compose..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) down --volumes --remove-orphans

lint:
	@echo "Linting Go code..."
	cd $(GO_PRODUCER_DIR) && golangci-lint run 
	cd $(GO_CONSUMER_DIR) && golangci-lint run 

