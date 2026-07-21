DATABASE_URL ?= postgres://pricetags:pricetags@localhost:5432/pricetags?sslmode=disable

.PHONY: up down logs build run test lint migrate-up migrate-down

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f app

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./...

lint:
	golangci-lint run ./...

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down 1
