.PHONY: build test run docker-up docker-down

build:
	go build -o bin/custody-api ./cmd/custody-api

test:
	go test ./...

run:
	go run ./cmd/custody-api

docker-up:
	docker compose -f deploy/docker-compose.yml up --build

docker-down:
	docker compose -f deploy/docker-compose.yml down
