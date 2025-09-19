# Use a minimal base image with Go installed
FROM golang:1.25 AS builder

# Set the working directory
WORKDIR /app

# Copy the Go source code
COPY . .

# Build the Go binary
RUN go build -o webhook main.go

# Expose port for webhook server
EXPOSE 8443

# Run the webhook
CMD ["/app/webhook"]
