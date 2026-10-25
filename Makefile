.PHONY: run build test vet fmt

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal
