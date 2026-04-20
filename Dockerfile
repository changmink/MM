FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server ./cmd/server

# ─────────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata ffmpeg

WORKDIR /app
COPY --from=builder /build/server ./server
COPY web/ ./web/

RUN mkdir -p /data

ENV DATA_DIR=/data
ENV WEB_DIR=/app/web
EXPOSE 8080

ENTRYPOINT ["./server"]
