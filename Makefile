.PHONY: nats-up nats-down run build test

nats-up:
	docker compose up -d nats

nats-down:
	docker compose down

run:
	go run ./cmd/server

build:
	go build ./...

test:
	go test ./...
