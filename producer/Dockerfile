# Stage 1: Build the Go binary using golang image
FROM golang:1.23 AS builder

# Install ZeroMQ development libraries and GCC for building with CGO
RUN apt-get update && apt-get install -y \
    libzmq3-dev \
    gcc \
    pkg-config

WORKDIR /app

# Copy Go module files
#COPY producer/go.mod producer/go.sum ./
#RUN go mod download
COPY .. /app
####is needed?
WORKDIR /app
COPY go.mod go.sum /app/
RUN go mod download
########


COPY ./producer /app/producer
COPY ./common /app/common
COPY ./producer/config.json /app/common/config.json
COPY ./db /app/db
COPY ./migrations /app/producer/migrations

# Set the working directory to the producer directory
WORKDIR /app/producer

# Build the Go application with CGO enabled
RUN VERSION=$(cat /app/producer/VERSION) && CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w -X main.version=$VERSION" -tags=containers -o producer


# Stage 2: Create a minimal runtime image using Ubuntu 22.04
FROM ubuntu:22.04

# Install runtime dependencies for ZeroMQ and necessary libraries
RUN apt-get update && apt-get install -y \
    libzmq5 libsodium23 libstdc++6

# Set the working directory
WORKDIR /app

COPY --from=builder /app/producer/producer ./producer
COPY --from=builder /app/migrations ./migrations

# Ensure the binary has execute permissions
RUN chmod +x ./producer

# Run the application
CMD ["./producer"]

