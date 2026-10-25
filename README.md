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
Missing/wrong tokens return 401, unknown sessions 404, and malformed requests
400. JSON request bodies are limited to 4 KiB. Tokens are generated with 256 bits
of randomness and stored only as hashes. These checks filter implausible
submissions; they do not prove that a client actually played a game.

The example environment enables the frontend at `http://localhost:5173`.
Set `CORS_ALLOWED_ORIGINS` to your game's exact origin when hosted separately;
JSON and Authorization preflights are supported. Without configured origins,
the browser frontend must use the same origin as the API.

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
internal/httpapi/  Gin routes, handlers, middleware, and tests
internal/database/ PostgreSQL connection pool and integration test
internal/game/     Session lifecycle, score validation, and leaderboard service
internal/store/    SQL queries and generated pgx access code
migrations/        Versioned SQL schema changes and authoring instructions
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
