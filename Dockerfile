# ---- Build Stage ----
FROM golang:1.21.6-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0
RUN go build -o /app/bot

# ---- Runtime Stage ----
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/bot .
COPY --from=builder /app/config.json .

ENV GODEBUG=netdns=go

EXPOSE 8080

CMD ["./bot"]
