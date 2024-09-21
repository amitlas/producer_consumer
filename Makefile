DOCKER_COMPOSE_FILE=docker-compose.yml
SCRIPT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
GO_PRODUCER_DIR=$(SCRIPT_DIR)/producer
GO_CONSUMER_DIR=$(SCRIPT_DIR)/consumer
DOCKER_COMPOSE_FILE=$(SCRIPT_DIR)/docker-compose.yml
LOGS_PATH=$(SCRIPT_DIR)/logs/

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
	@echo "Cleaning up logs..."
	rm -f $(LOGS_PATH)/*

run: stop
	@echo "Running Docker Compose..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) up

stop:
	@echo "Stopping Docker Compose..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) down --volumes --remove-orphans

lint:
	@echo "Linting Go code..."
	golangci-lint run

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

# Pprof, flamegraph
FLAMEGRAPH_DIR=~/FlameGraph
DOWNLOAD_INTERVAL=30
SAMPLING_INTERVAL=1
# Producer-specific variables
PRODUCER_PPROF_PORT ?= 6060
PRODUCER_PROFILE_FILE ?= pprof_data/producer_profile.pb.gz
PRODUCER_OUTPUT_FILE ?= pprof_data/producer_out.svg
PRODUCER_PROFILE_URL ?= http://localhost:$(PRODUCER_PPROF_PORT)/debug/pprof/profile?seconds=$(DOWNLOAD_INTERVAL)
CONTINUOUS_PRODUCER_PROFILE_URL ?= http://localhost:$(PRODUCER_PPROF_PORT)/debug/pprof/profile?seconds=$(SAMPLING_INTERVAL)
# Consumer-specific variables
CONSUMER_PPROF_PORT=6061
CONSUMER_PROFILE_FILE=pprof_data/consumer_profile.pb.gz
CONSUMER_OUTPUT_FILE=pprof_data/consumer_out.svg
CONSUMER_PROFILE_URL=http://localhost:$(CONSUMER_PPROF_PORT)/debug/pprof/profile?seconds=$(DOWNLOAD_INTERVAL)
CONTINUOUS_CONSUMER_PROFILE_URL=http://localhost:$(CONSUMER_PPROF_PORT)/debug/pprof/profile?seconds=$(SAMPLING_INTERVAL)

define flamegraph_generation_overwrite
	curl -s $(1) -o $(2); \
	go tool pprof -raw -output=pprof_data/profile.out $(2); \
	$(FLAMEGRAPH_DIR)/stackcollapse-go.pl pprof_data/profile.out > pprof_data/out.folded; \
	$(FLAMEGRAPH_DIR)/flamegraph.pl pprof_data/out.folded > $(3); \
	echo "Flamegraph saved as $(3)";
endef

define flamegraph_generation_append
	curl -s $(1) -o $(2); \
	go tool pprof -raw -output=pprof_data/profile.out $(2); \
	$(FLAMEGRAPH_DIR)/stackcollapse-go.pl pprof_data/profile.out >> pprof_data/out.folded; \
	$(FLAMEGRAPH_DIR)/flamegraph.pl pprof_data/out.folded > $(3); \
	echo "Flamegraph saved as $(3)";
endef

producer_flamegraph: $(PRODUCER_PROFILE_FILE)
	$(call flamegraph_generation_overwrite, $(PRODUCER_PROFILE_URL), $(PRODUCER_PROFILE_FILE), $(PRODUCER_OUTPUT_FILE))

consumer_flamegraph: $(CONSUMER_PROFILE_FILE)
	$(call flamegraph_generation_overwrite, $(CONSUMER_PROFILE_URL), $(CONSUMER_PROFILE_FILE), $(CONSUMER_OUTPUT_FILE))

$(PRODUCER_PROFILE_FILE):
	echo "Capturing profile from $(PRODUCER_PROFILE_URL)..."; \
	curl -s -o $(PRODUCER_PROFILE_FILE) $(PRODUCER_PROFILE_URL)

$(CONSUMER_PROFILE_FILE):
	echo "Capturing profile from $(CONSUMER_PROFILE_URL)..."; \
	curl -s -o $(CONSUMER_PROFILE_FILE) $(CONSUMER_PROFILE_URL)

define continuous_flamegraph
	while true; do \
	  $(call flamegraph_generation_append, $(1), $(2), $(3)) \
	done
endef

continuous_producer_flamegraph:
	$(eval PRODUCER_PROFILE_URL := $(CONTINUOUS_PRODUCER_PROFILE_URL))
	$(call continuous_flamegraph, $(PRODUCER_PROFILE_URL), $(PRODUCER_PROFILE_FILE), $(PRODUCER_OUTPUT_FILE))

continuous_consumer_flamegraph:
	$(eval CONSUMER_PROFILE_URL := $(CONTINUOUS_CONSUMER_PROFILE_URL))
	$(call continuous_flamegraph, $(CONSUMER_PROFILE_URL), $(CONSUMER_PROFILE_FILE), $(CONSUMER_OUTPUT_FILE))

db_wipe:
	@echo "Ensuring the PostgreSQL container is running..."
	@if [ $$(docker ps -q -f name=postgres) = "" ]; then \
		echo "PostgreSQL container is not running. Starting it..."; \
		docker-compose up -d postgres; \
		sleep 5; \
	else \
		echo "PostgreSQL container is already running."; \
	fi
	@echo "Dropping and recreating the tasks_db database..."
	docker exec -it postgres psql -U postgres -c "DROP DATABASE IF EXISTS tasks_db;"
	docker exec -it postgres psql -U postgres -c "CREATE DATABASE tasks_db;"
	@echo "Database tasks_db wiped and recreated."


manual_migrate:
	flyway migrate

manual_rollback:
	flyway undo

get_db_table_columns:
	docker exec -it postgres psql -U postgres -d tasks_db -c "SELECT column_name, data_type FROM information_schema.columns WHERE table_name = 'tasks';"

.PHONY: sqlc db_wipe
.PHONY:	build clean consumer producer
.PHONY: run lint run_producer run_consumer producer_flamegraph consumer_flamegraph continuous_producer_flamegraph continuous_consumer_flamegraph
.PHONY: release_producer_major release_producer_minor release_consumer_major release_consumer_minor
.PHONY: manual_migrate manual_rollback get_db_table_columns
