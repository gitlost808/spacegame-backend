.PHONY: run build test test-integration vet fmt db-up db-down \
	migrate-create migrate-up migrate-down migrate-status migrate-version

MIGRATIONS_DIR ?= migrations
export MIGRATIONS_DIR NAME

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server
	go build -o bin/migrate ./cmd/migrate

test:
	go test ./...

test-integration:
	go test -tags=integration ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down

migrate-create:
	go run ./cmd/migrate -dir "$$MIGRATIONS_DIR" create "$$NAME"

migrate-up:
	go run ./cmd/migrate -dir "$$MIGRATIONS_DIR" up

migrate-down:
	go run ./cmd/migrate -dir "$$MIGRATIONS_DIR" down

migrate-status:
	go run ./cmd/migrate -dir "$$MIGRATIONS_DIR" status

migrate-version:
	go run ./cmd/migrate -dir "$$MIGRATIONS_DIR" version
