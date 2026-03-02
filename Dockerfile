FROM golang:1.26-alpine AS builder
ARG TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags="-s -w" -o repopsy .

FROM alpine:3.23
RUN apk add --no-cache git
RUN adduser -D -u 1000 repopsy
WORKDIR /data
COPY --from=builder /build/repopsy /usr/local/bin/repopsy
USER repopsy
ENTRYPOINT ["repopsy"]
