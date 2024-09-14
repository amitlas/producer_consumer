# Stage 1: Build the Go binary using golang image
FROM golang:1.20 as builder

# Install ZeroMQ development libraries and GCC for building with CGO
RUN apt-get update && apt-get install -y \
    libzmq3-dev \
    gcc \
    pkg-config

WORKDIR /app

# Copy Go module files
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the Go application with CGO enabled
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -tags=containers -o consumer

# Stage 2: Create a minimal runtime image using Ubuntu 22.04
FROM ubuntu:22.04

# Install runtime dependencies for ZeroMQ and necessary libraries
RUN apt-get update && apt-get install -y \
    libzmq5 libsodium23 libstdc++6

# Copy the binary from the builder stage
COPY --from=builder /app/consumer /app/consumer

# Set the working directory
WORKDIR /app

# Ensure the binary has execute permissions
RUN chmod +x ./consumer

# Run the application
CMD ["./consumer"]

