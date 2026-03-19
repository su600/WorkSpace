# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o workspace-server .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/workspace-server .

# Default mount point for the directory to serve
VOLUME ["/data"]

EXPOSE 8080

ENV ROOT_DIR=/data

ENTRYPOINT ["./workspace-server"]
