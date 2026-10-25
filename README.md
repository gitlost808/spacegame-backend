# Spacegame Backend

A Go web service using [Gin](https://gin-gonic.com/) and PostgreSQL through
[pgx](https://github.com/jackc/pgx). Requires Go 1.27 or later. Local database
development also requires Docker Compose. Database migrations use
[Goose](https://github.com/pressly/goose), pinned in `go.mod`.

## Run

```sh
cp .env.example .env
make db-up

set -a
. ./.env
set +a

make migrate-up
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

## Migrations

Create and edit a timestamped SQL migration, then apply it using the same
`DATABASE_URL` as the server:

```sh
make migrate-create NAME=create_players
# Edit the generated file in migrations/.
make migrate-status
make migrate-up
make migrate-version
make migrate-down  # Roll back the most recent migration
```

Creating a migration does not need a database connection. All other commands
require `DATABASE_URL`. No separate Goose installation is needed: the
`cmd/migrate` command uses the pinned library and PostgreSQL driver.

The directory initially has no SQL migrations until the first schema change is
defined. `migrate-up` and `migrate-status` verify the connection and report that
there are no SQL migrations. See [migrations/README.md](migrations/README.md)
for authoring and deployment conventions.

## Docker

Build the image, apply migrations as a separate command, and start the service:

```sh
docker compose build app
docker compose up -d --wait db
docker compose run --rm app migrate up
docker compose up -d app
```

The app uses the Compose database at `db:5432` and listens on
`http://localhost:8080`. Set `APP_PORT` to change the host port. Inspect migrations
with `docker compose run --rm app migrate status`. Stop the stack with
`docker compose down`; its database volume is preserved.

The multi-stage Dockerfile builds both binaries and runs them as a non-root user
in an Alpine image with CA certificates. It includes `migrations/` and excludes
local environment files from the build context. The default command starts the
server; overriding it with `migrate up` applies migrations. The server does not
apply migrations on startup.

To build an image directly, run `docker build -t spacegame-backend .`. When
running it outside Compose, pass a `DATABASE_URL` reachable from the container.

## Published images

The [Docker workflow](.github/workflows/docker.yml) builds the image and runs unit
tests inside the Docker build. Pull requests build without publishing. Pushes
to the repository's default branch publish these tags to GitHub Container
Registry:

```text
ghcr.io/<owner>/<repository>:latest
ghcr.io/<owner>/<repository>:sha-<full-commit-sha>
```

The image path is derived from the GitHub repository and normalized to lowercase.
The workflow can also be run manually from the default branch. It uses the
built-in `GITHUB_TOKEN` with `packages: write`; no additional registry credentials
are required. GitHub Actions must be enabled for the repository.

Images target `linux/amd64` and include both the server and migration command.
Set `DATABASE_URL` at deployment time and run `migrate up` from the published
image before starting the server. Use a commit tag or digest to keep the
migration and application deployments on the same build. `latest` is updated on
each successful default-branch build.

## Development

```sh
make fmt    # Format Go files
make test   # Run tests
make test-integration # Test PostgreSQL reads/writes with DATABASE_URL configured
make vet    # Run static checks
make build  # Build bin/server and bin/migrate
make run    # Start the server
```

Without Make, run `go test ./...`, `go vet ./...`, and
`go build -o bin/server ./cmd/server` directly. Run migration commands with
`go run ./cmd/migrate up` (or `status`, `version`, `down`, `create NAME`).

## Layout

```text
cmd/server/        Server entry point, configuration, and shutdown
cmd/migrate/       Goose migration command
internal/httpapi/  Gin routes, handlers, middleware, and tests
internal/database/ PostgreSQL connection pool and integration test
migrations/        Versioned SQL schema changes and authoring instructions
Dockerfile         Server and migration binaries in a non-root runtime image
compose.yaml       Local app and PostgreSQL service with persistent storage
```

Register new endpoints in `internal/httpapi/router.go`. Add application packages
under `internal/` as the game grows. The database pool is created in the server
entry point and can be passed into future persistence packages. Add versioned
SQL migrations when introducing the first tables. The integration test uses
a temporary table inside a rolled-back transaction.

The module name is `spacegame-backend`. When a repository remote is available,
you can change it with `go mod edit -module=your/repository/path` and update
the imports in the Go source files to match.
