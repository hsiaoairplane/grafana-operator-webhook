# Use a minimal base image with Go installed
FROM golang:1.26 AS builder

# Set the working directory
WORKDIR /app

# Copy the Go source code
COPY . .

# Build the Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -o webhook main.go

# Use a minimal final image
FROM gcr.io/distroless/static-debian12

# Set the working directory
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/webhook /app/webhook

# Expose port for webhook server
EXPOSE 8443

# Run the webhook
CMD ["/app/webhook"]
