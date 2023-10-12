# Use the official Go image as the base image
FROM golang:1.21 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire project
COPY . .

# Build the Go application for a smaller and more secure container
RUN CGO_ENABLED=0 GOOS=linux go build -o google-disk-space-cli

# Use a small base image for the release stage
FROM alpine:3.14

# Set the working directory inside the container
WORKDIR /

# Copy the binary from the builder stage
COPY --from=builder /app/google-disk-space-cli /

# Command to run the application
CMD ["/google-disk-space-cli"]
