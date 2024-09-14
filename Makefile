gOCKER_COMPOSE_FILE=docker-compose.yml
SCRIPT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
GO_PRODUCER_DIR=$(SCRIPT_DIR)/producer
GO_CONSUMER_DIR=$(SCRIPT_DIR)/consumer
DOCKER_COMPOSE_FILE=$(SCRIPT_DIR)/docker-compose.yml
PRODUCER_BINARY=$(GO_PRODUCER_DIR)/producer
CONSUMER_BINARY=$(GO_CONSUMER_DIR)/consumer

# Paths to the version files for producer and consumer
PRODUCER_VERSION_FILE = producer/VERSION
CONSUMER_VERSION_FILE = consumer/VERSION

# Extract the major and minor version for producer and consumer
PRODUCER_VERSION := $(shell cat $(PRODUCER_VERSION_FILE))
CONSUMER_VERSION := $(shell cat $(CONSUMER_VERSION_FILE))
PRODUCER_MAJOR := $(shell echo $(PRODUCER_VERSION) | cut -d. -f1)
PRODUCER_MINOR := $(shell echo $(PRODUCER_VERSION) | cut -d. -f2)
CONSUMER_MAJOR := $(shell echo $(CONSUMER_VERSION) | cut -d. -f1)
CONSUMER_MINOR := $(shell echo $(CONSUMER_VERSION) | cut -d. -f2)

# Docker container names
PRODUCER_CONTAINER_NAME = prod-producer
CONSUMER_CONTAINER_NAME = prod-consumer


sqlc:
	sqlc generate

build: producer consumer

mod_tidy_producer:
	@echo "Updating GO modules"
	cd $(GO_PRODUCER_DIR) && go mod tidy

mod_tidy_consumer:
	@echo "Updating GO modules"
	cd $(GO_CONSUMER_DIR) && go mod tidy

producer: mod_tidy_producer sqlc
	@echo "Building producer..."
	@VERSION=$(shell cat $(GO_PRODUCER_DIR)/VERSION) && \
	docker-compose build producer --build-arg VERSION=$$VERSION || { echo "Producer build failed! Exiting..."; exit 1; }

consumer: mod_tidy_consumer sqlc
	@echo "Building consumer..."
	@VERSION=$(shell cat $(GO_PRODUCER_DIR)/VERSION) && \
	docker-compose build consumer --build-arg VERSION=$$VERSION || { echo "Consumer build failed! Exiting..."; exit 1; }

clean:
	@echo "Cleaning up binaries..."
	rm -f $(PRODUCER_BINARY)
	rm -f $(CONSUMER_BINARY)

run: stop
	@echo "Running Docker Compose..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) up

stop:
	@echo "Stopping Docker Compose..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) down --volumes --remove-orphans

lint:
	@echo "Linting Go code..."
	cd $(GO_PRODUCER_DIR) && golangci-lint run
	cd $(GO_CONSUMER_DIR) && golangci-lint run

run_producer:
	docker run --rm -it $(PRODUCER_CONTAINER_NAME):latest /bin/sh

run_consumer:
	docker run --rm -it $(CONSUMER_CONTAINER_NAME):latest /bin/sh

# Update producer version file and copy to the container
release_producer_major: producer
	@echo "Build succeeded. Updating producer major version..."
	@NEW_MAJOR=$$(($(PRODUCER_MAJOR) + 1)) && echo "$$NEW_MAJOR.0.0" > $(PRODUCER_VERSION_FILE)
	@$(MAKE) producer

release_producer_minor: producer
	@echo "Build succeeded. Updating producer minor version..."
	@NEW_MINOR=$$(($(PRODUCER_MINOR) + 1)) && echo "$(PRODUCER_MAJOR).$$NEW_MINOR.0" > $(PRODUCER_VERSION_FILE)
	@$(MAKE) producer

# Update consumer version file and copy to the container
release_consumer_major: consumer
	@echo "Build succeeded. Updating consumer major version..."
	@NEW_MAJOR=$$(($(CONSUMER_MAJOR) + 1)) && echo "$$NEW_MAJOR.0.0" > $(CONSUMER_VERSION_FILE)
	@$(MAKE) consumer

release_consumer_minor: consumer
	@echo "Build succeeded. Updating consumer minor version..."
	@NEW_MINOR=$$(($(CONSUMER_MINOR) + 1)) && echo "$(CONSUMER_MAJOR).$$NEW_MINOR.0" > $(CONSUMER_VERSION_FILE)
	@$(MAKE) consumer

