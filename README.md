# Spacegame Backend

A Go HTTP backend starter using only the standard library. Requires Go 1.27 or later.

## Run

```sh
go run ./cmd/server
```

The server listens on `:8080` by default. Set `HTTP_ADDR` to change the address:

```sh
HTTP_ADDR=127.0.0.1:9090 go run ./cmd/server
```

Check the health endpoint:

```sh
curl http://localhost:8080/healthz
# {"status":"ok"}
```

Logs are JSON written to standard output. On Ctrl+C or SIGTERM, the server allows
up to 10 seconds for active requests to finish before exiting.

## Development

```sh
make fmt    # Format Go files
make test   # Run tests
make vet    # Run static checks
make build  # Build bin/server
make run    # Start the server
```

Without Make, run `go test ./...`, `go vet ./...`, and
`go build -o bin/server ./cmd/server` directly.

## Layout

```text
cmd/server/        Server entry point, configuration, and shutdown
internal/httpapi/  HTTP routes, handlers, and tests
```

Register new endpoints in `internal/httpapi/router.go`. Add application packages
under `internal/` as the game grows.

The module name is `spacegame-backend`. When a repository remote is available,
you can change it with `go mod edit -module=your/repository/path` and update
the imports in the Go source files to match.
