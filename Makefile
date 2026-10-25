.PHONY: run build test test-integration vet fmt db-up db-down

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test ./...

test-integration:
	go test -tags=integration ./internal/database -count=1

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

db-up:
	docker compose up -d --wait db

db-down:
	docker compose down
