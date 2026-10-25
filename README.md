# Spacegame Backend

A Go web service using [Gin](https://gin-gonic.com/) and PostgreSQL through
[pgx](https://github.com/jackc/pgx). Requires Go 1.27 or later. Local database
development also requires Docker Compose.

## Run

```sh
cp .env.example .env
make db-up

set -a
. ./.env
set +a

make run
```

The Go process reads configuration from environment variables; it does not load
`.env` automatically. The commands above export the example settings into your
shell. Docker Compose reads `.env` itself.

The server requires `DATABASE_URL` and verifies the connection before accepting
HTTP requests. To use an existing PostgreSQL database, set `DATABASE_URL` to its
connection string and skip `make db-up`.

The server listens on `:8080` by default. Set `HTTP_ADDR` to change the address:

```sh
HTTP_ADDR=127.0.0.1:9090 go run ./cmd/server
```

Check liveness and database readiness:

```sh
curl http://localhost:8080/healthz
# {"status":"ok"}
curl http://localhost:8080/readyz
# {"status":"ok"}
```

`/healthz` checks that the HTTP service is alive. `/readyz` checks PostgreSQL with
a two-second timeout and returns HTTP 503 if it is unavailable.

Logs are JSON written to standard output. Gin uses release mode by default;
set `GIN_MODE=debug` for its extra development output. On Ctrl+C or SIGTERM, the
server allows up to 10 seconds for active requests to finish, then closes the
database pool.

The local database uses the development credentials in `compose.yaml` and binds
to `127.0.0.1:5432`. `make db-down` stops it and preserves its named volume.
For another port, change `POSTGRES_PORT` and the port in `DATABASE_URL` together.
The example connection disables TLS for local development; use your provider's
connection settings for a hosted database.

## Development

```sh
make fmt    # Format Go files
make test   # Run tests
make test-integration # Test PostgreSQL reads/writes with DATABASE_URL configured
make vet    # Run static checks
make build  # Build bin/server
make run    # Start the server
```

Without Make, run `go test ./...`, `go vet ./...`, and
`go build -o bin/server ./cmd/server` directly.

## Layout

```text
cmd/server/        Server entry point, configuration, and shutdown
internal/httpapi/  Gin routes, handlers, middleware, and tests
internal/database/ PostgreSQL connection pool and integration test
compose.yaml       Local PostgreSQL service and persistent storage
```

Register new endpoints in `internal/httpapi/router.go`. Add application packages
under `internal/` as the game grows. The database pool is created in the server
entry point and can be passed into future persistence packages. Add versioned
schema migrations when introducing the first tables. The integration test uses
a temporary table inside a rolled-back transaction.

The module name is `spacegame-backend`. When a repository remote is available,
you can change it with `go mod edit -module=your/repository/path` and update
the imports in the Go source files to match.
