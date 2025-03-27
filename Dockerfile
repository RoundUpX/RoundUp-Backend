# Use an official Go image as the build environment
FROM golang:1.23-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the Go application binary
RUN go build -o main .

# Use a minimal image for the final container
FROM alpine:latest

# Set the working directory in the final container
WORKDIR /root/

# Copy the compiled binary from the builder stage
COPY --from=builder /app/main .

# Expose the port your application listens on
EXPOSE 8082

# Run the binary
CMD ["./main"]
