# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o repopsy .

# Final stage
FROM alpine:3.19

# Install git (required for repopsy to work)
RUN apk add --no-cache git

# Create non-root user
RUN adduser -D -u 1000 repopsy

WORKDIR /data

# Copy binary from builder
COPY --from=builder /build/repopsy /usr/local/bin/repopsy

# Use non-root user
USER repopsy

ENTRYPOINT ["repopsy"]
