# Spacegame Backend

A leaderboard service for a space shooter browser game, using
[Gin](https://gin-gonic.com/), PostgreSQL, [pgx](https://github.com/jackc/pgx), and
[sqlc](https://sqlc.dev/) for generated query methods. Requires Go 1.27 or later. Local database
development also requires Docker Compose. Database migrations use
[Goose](https://github.com/pressly/goose), pinned in `go.mod`.

## Run

```sh
cp .env.example .env
make db-up
make migrate-up
make run
```

The server and migration command load `.env` automatically for local development
using [godotenv](https://github.com/joho/godotenv). Existing process variables
take precedence, including explicitly empty values. No shell export commands
are needed. Docker Compose also reads `.env`. Docker must be running. If you
already have a `.env`, keep it instead of copying over it, and edit its settings
before starting the service.

`.env` is optional and is loaded only when `APP_ENV` is unset, empty, or
`development`. Set `APP_ENV=production` in your infrastructure configuration;
production processes use only injected variables and mounted secrets.
Configuration uses [caarlos0/env](https://github.com/caarlos0/env) for typed
parsing; invalid settings fail startup.

The server requires `DATABASE_URL` or `DATABASE_URL_FILE` and verifies the
connection before accepting HTTP requests. To use an existing PostgreSQL
database, set `DATABASE_URL` to its connection string and skip `make db-up`.

The server listens on `:8080` by default. Set `HTTP_ADDR` to change the address:

```sh
env HTTP_ADDR=127.0.0.1:8081 make run
```

Use a different port from `METRICS_ADDR`, which defaults to
`127.0.0.1:9090`.

In another terminal, check liveness and database readiness (use port 8081 if
you chose the alternate address above):

```sh
curl http://localhost:8080/healthz
# {"status":"ok"}
curl http://localhost:8080/readyz
# {"status":"ok"}
```

`/healthz` checks that the HTTP service is alive. `/readyz` checks PostgreSQL,
all migrations bundled with this build, and required game columns with a
two-second timeout. It returns HTTP 503 when any of those checks fail.

Logs are JSON written to standard output. Gin uses release mode by default;
set `GIN_MODE=debug` for its extra development output. On Ctrl+C or SIGTERM, the
server allows up to 10 seconds for active requests to finish, then closes the
database pool.

Prometheus metrics use a separate listener, defaulting to `127.0.0.1:9090`.
Set `METRICS_ADDR` for your private monitoring network. JSON request logs include
a request ID, route template, status, and duration.

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
require `DATABASE_URL` or `DATABASE_URL_FILE`, supplied through `.env` locally
or the process environment. No separate Goose installation is needed: the
`cmd/migrate` command uses the pinned library and PostgreSQL driver.

The initial migration creates the game session table and leaderboard index.
After changing the schema or `internal/store/queries.sql`, run `make generate`
and commit the generated files in `internal/store/`. The generator version is
pinned in the Makefile; it runs through Docker and is not needed at runtime.
See [migrations/README.md](migrations/README.md) for deployment conventions.

## Game API

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/api/v1/sessions` | Start a run and receive its private finish token |
| POST | `/api/v1/sessions/{session_id}/finish` | Validate and record the final score |
| GET | `/api/v1/leaderboard?limit=20` | List the best completed runs, with ranks |

Start a session when gameplay begins:

```sh
curl -X POST http://localhost:8080/api/v1/sessions \
  -H 'Content-Type: application/json' \
  -d '{"player_name":"Pilot"}'
```

The HTTP 201 response contains `session_id`, `session_token`, `player_name`,
`started_at`, and `expires_at`. Keep the token for this run. Names are trimmed
and must contain 1–32 Unicode characters without control characters.

When gameplay ends, replace `SESSION_ID` and `SESSION_TOKEN` with the values
from the start response:

```sh
curl -X POST http://localhost:8080/api/v1/sessions/SESSION_ID/finish \
  -H 'Authorization: Bearer SESSION_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"score":1000}'
```

The response contains the session ID, player name, score, start/finish times, and
`duration_ms`. Duration is calculated from server-recorded times; the client
submits only an integer score. Zero is valid. A valid finish is atomic and
cannot be overwritten. Retrying the same score with the same token returns the
original result, including after expiry. A different score returns HTTP 409.

The leaderboard returns `{"entries":[...]}`. Each entry includes a `rank` and
the completed result fields. It ranks individual runs, so the same display name
can appear multiple times. Scores sort descending, with earlier finish time
and then session ID breaking ties. The default limit is 20, with a maximum of
100. An empty leaderboard returns `{"entries":[]}`. Display names are not
authenticated accounts.

Validation uses these configurable starter defaults:

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `SESSION_MIN_DURATION` | `1s` | Shortest accepted run; may be set to `0s` |
| `SESSION_MAX_DURATION` | `1h` | Lifetime of a newly started session |
| `MAX_SCORE_PER_SECOND` | `1000` | Highest accepted average score rate |
| `CORS_ALLOWED_ORIGINS` | empty | Comma-separated browser origins allowed to read the API |

Tune these limits to the game's scoring rules. The score must be non-negative
and no greater than elapsed seconds multiplied by `MAX_SCORE_PER_SECOND`.
Expired sessions return HTTP 410; implausible scores or durations return 422.
An authenticated finish with a negative/excessive score, too-short duration,
or expired session deletes the unfinished session before returning that error.
Further attempts return 404, even if the score is corrected. Missing or wrong
tokens do not delete sessions. Completed results remain protected, including
when a valid finish races with an invalid attempt. Malformed HTTP/JSON requests
are rejected without consuming the session.
Missing/wrong tokens return 401, unknown sessions 404, rate limits return 429
with `Retry-After`, and malformed requests 400. JSON request bodies are limited to 4 KiB. Tokens are generated with 256 bits
of randomness and stored only as hashes. These checks filter implausible
submissions; they do not prove that a client actually played a game.

The example environment enables the frontend at `http://localhost:5173`.
Set `CORS_ALLOWED_ORIGINS` to your game's exact origin when hosted separately;
JSON and Authorization preflights are supported by
[gin-contrib/cors](https://github.com/gin-contrib/cors). Without configured
origins, the browser frontend must use the same origin as the API. Unlisted
cross-origin requests and preflights return HTTP 403. `*` allows any origin
without enabling cookie credentials.

Per-client request limits default to starts 30/minute (burst 10), finishes
120/minute (burst 20), and leaderboard reads 300/minute (burst 30). A global
bucket allows 50 requests/second (burst 100) per instance. State is bounded at
10,000 IPv4 addresses/IPv6 /64 prefixes. Tune the `RATE_LIMIT_*` settings in
`.env.example` for your traffic.

Expired unfinished sessions are deleted in batches every five minutes, after a
default 24-hour retention period. Completed scores remain. Configure
`CLEANUP_INTERVAL`, `EXPIRED_SESSION_RETENTION`, and `CLEANUP_BATCH_SIZE`.

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

## Production

This repository builds and publishes the application image. Production
configuration and deployment live in the separate infrastructure repository,
including ingress/TLS, secret mounts, rollout jobs, monitoring, and PostgreSQL
backups and restore drills. `compose.yaml` is for local development.

The [container contract](docs/container.md) documents commands, ports,
environment variables, database privileges, health probes, and metrics for the
infrastructure configuration. Set `APP_ENV=production` for both server and
migration jobs and select an explicit `sslmode` in the PostgreSQL connection
URI. Use `sslmode=verify-full` for remote databases; `sslmode=disable` is an
explicit option for a trusted, isolated database on the same host. Both commands
support `DATABASE_URL_FILE` for a mounted secret instead of `DATABASE_URL`.

## Published images

The [Docker workflow](.github/workflows/docker.yml) checks generated query code
and runs PostgreSQL integration tests before building the image. Unit tests also
run inside the Docker build. Pull requests build without publishing. Pushes
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
make generate # Regenerate database access code with pinned sqlc
make test   # Run tests
make test-integration # Test migrations, sessions, concurrency, and queries with PostgreSQL
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
internal/config/   Typed server configuration and local dotenv loading
internal/httpapi/  Gin routes, handlers, middleware, and tests
internal/database/ PostgreSQL connection pool and integration test
internal/game/     Session lifecycle, score validation, and leaderboard service
internal/store/    SQL queries and generated pgx access code
internal/maintenance/ Bounded cleanup worker for expired unfinished sessions
internal/observability/ Request metrics and structured logging
migrations/        Versioned SQL schema changes and authoring instructions
docs/container.md  Runtime contract for the separate infrastructure repository
Dockerfile         Server and migration binaries in a non-root runtime image
compose.yaml       Local app and PostgreSQL service with persistent storage
```

Register new endpoints in `internal/httpapi/router.go`. Add application packages
under `internal/` as the game grows. The database pool is created in the server
entry point and passed to the game service through generated query methods.
Integration tests use temporary tables or isolated schemas and clean up their
own data. Add versioned migrations and regenerate query methods when changing
the game schema.

The module name is `spacegame-backend`. When a repository remote is available,
you can change it with `go mod edit -module=your/repository/path` and update
the imports in the Go source files to match.
