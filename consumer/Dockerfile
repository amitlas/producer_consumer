# Stage 1: Build the Go binary using golang image
FROM golang:1.23 AS builder

# Install ZeroMQ development libraries and GCC for building with CGO
RUN apt-get update && apt-get install -y \
    libzmq3-dev \
    gcc \
    pkg-config

# Copy Go module files
COPY .. /app
COPY ./migrations /app/consumer/migrations
# needed for embed
#COPY ./consumer/config.json /app/common/config.json

# Set the working directory to the consumer directory
WORKDIR /app/consumer

# Build the Go application with CGO enabled
RUN VERSION=$(cat /app/consumer/VERSION) && CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w -X main.version=$VERSION" -tags=containers -o consumer


# Stage 2: Create a minimal runtime image using Ubuntu 22.04
FROM ubuntu:22.04

# Install runtime dependencies for ZeroMQ and necessary libraries
RUN apt-get update && apt-get install -y \
    libzmq5 libsodium23 libstdc++6

# Set the working directory
WORKDIR /app

# Copy binary and config to image
COPY --from=builder /app/consumer/consumer ./consumer
COPY --from=builder /app/consumer/config.json ./config.json

# Ensure the binary has execute permissions
RUN chmod +x ./consumer

# Run the application
CMD ["./consumer"]

