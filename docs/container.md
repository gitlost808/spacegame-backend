# Container contract

This repository owns the application, migrations, Docker image, and CI that
publishes it. The infrastructure repository owns production configuration,
ingress and HTTPS, secret delivery, migration jobs and rollouts, monitoring
and alert delivery, and PostgreSQL backups and restore drills.

## Image and process

CI publishes `ghcr.io/<owner>/<repository>:latest` and
`:sha-<full-commit-sha>` for successful default-branch builds. Images target
`linux/amd64`. Reference an immutable digest or commit tag in infrastructure
and use the same image for migrations and the server.

| Setting | Value |
| --- | --- |
| Default command | `server` |
| Migration command | `migrate up` (override the image command) |
| Other migration commands | `migrate status`, `migrate version`, `migrate down` |
| Working directory | `/app`; SQL files are bundled at `/app/migrations` |
| User/group | `10001:10001` |
| API listener | `:8080`, plain HTTP behind infrastructure ingress |
| Metrics listener | `127.0.0.1:9090` by default; separate HTTP server |
| Shutdown | SIGTERM; HTTP requests get up to 10 seconds to drain |
| Persistent application storage | None; state lives in PostgreSQL |

The runtime supports a read-only root filesystem and dropped Linux
capabilities. Give the process enough termination grace for request draining
and database worker shutdown, for example 30 seconds. JSON logs go to standard
output. Database connection failures at startup exit with a nonzero status;
the process checks connectivity with a five-second timeout.

## Runtime configuration

Set `APP_ENV=production` explicitly for both server and migration jobs. In
production the process reads injected environment variables and mounted
secrets, and never loads `.env`. Optional `.env` loading is limited to local
development when `APP_ENV` is unset, empty, or `development`. Existing process
variables always take precedence over local defaults.

| Variable | Default | Infrastructure setting |
| --- | --- | --- |
| `APP_ENV` | unset | `production` skips local `.env` loading and requires an explicit database `sslmode` |
| `DATABASE_URL` | required unless a file is configured | PostgreSQL connection URI; keep it in a secret |
| `DATABASE_URL_FILE` | unset | Alternative path to a mounted UTF-8 connection URI, at most 16 KiB |
| `HTTP_ADDR` | `:8080` | API bind address |
| `METRICS_ADDR` | `127.0.0.1:9090` | Set `:9090` for scraping across a private container network |
| `GIN_MODE` | `release` | Keep `release` in production |
| `CORS_ALLOWED_ORIGINS` | empty | Comma-separated exact browser origins when the game has a separate origin |
| `TRUSTED_PROXIES` | empty | Comma- or space-separated ingress IP addresses/CIDRs |

Set only one of `DATABASE_URL` and `DATABASE_URL_FILE`. Mounted secrets and
CA certificates must be readable by UID 10001. Secret values are loaded when
the process starts; restart the process after rotating them.

Production connections must specify `sslmode` explicitly in the PostgreSQL URI,
including URIs loaded through `DATABASE_URL_FILE`. The server and migration job
honor the selected pgx mode. For remote or managed databases, use certificate
and hostname verification:

```text
postgres://USER:PASSWORD@DB_HOST:5432/DATABASE?sslmode=verify-full
```

Percent-encode special URI characters in credentials. For a private CA, mount
the certificate and append `&sslrootcert=/path/in/container/ca.crt`. The image
includes the system CA bundle. Both commands use the selected SSL mode.

For a trusted, isolated database on the same Docker host, infrastructure can
explicitly select `sslmode=disable` while keeping `APP_ENV=production`. This
leaves database traffic unencrypted; keep the database port private. The
`require` mode encrypts traffic without hostname verification. Avoid `prefer`
and `allow` in production because they permit plaintext fallback. An omitted,
empty, or repeated URI `sslmode` is rejected in production to keep the choice
unambiguous and avoid pgx's default.

Only trust ingress addresses that can actually reach the service. The ingress
must sanitize forwarding headers supplied by clients. With no trusted proxies,
the service uses the direct peer address for client rate limits. Avoid trusting
all addresses: spoofed forwarding headers would bypass those limits.

## Probes and routing

| Endpoint | Successful response | Purpose |
| --- | --- | --- |
| API `GET /healthz` | 200, `{"status":"ok"}` | Liveness; independent of database availability |
| API `GET /readyz` | 200, `{"status":"ok"}` | Readiness; database connectivity, bundled migration versions, and required schema columns |
| Metrics `GET /metrics` | 200, Prometheus text | Private monitoring scrape |

Readiness returns 503 when its checks fail, with a two-second database timeout.
Use a probe timeout longer than two seconds. Readiness never creates or changes
database objects. Additional migration versions from newer compatible builds
do not make an older build unready. Missing bundled migrations do.

Keep port 9090 and readiness probes private. The public ingress should route
game requests to port 8080 and terminate HTTPS. Health endpoints are excluded
from application rate limits. Configure startup and restart policies in the
infrastructure repository.

## Migrations and database access

Run one migration job with `migrate up` before rolling out a new application
version. Fail the rollout if the job fails, then wait for readiness before
sending traffic. The server does not migrate on startup. The migration job
uses an account that owns the application schema; the server uses a separate
runtime account without schema modification privileges.

For the current schema, example runtime grants after the initial migration are:

```sql
GRANT CONNECT ON DATABASE spacegame TO spacegame_app;
GRANT USAGE ON SCHEMA public TO spacegame_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON game_sessions TO spacegame_app;
GRANT SELECT ON goose_db_version TO spacegame_app;
```

Adapt names and schema to the provisioned database. Cleanup requires DELETE;
readiness requires reading Goose metadata and game columns. Review privileges
when migrations add objects. Use backward-compatible migrations while older
instances are still running. Rolling back an image does not roll back schema
changes; run `migrate down` only after reviewing data loss and compatibility.

PostgreSQL backups, retention, off-host storage or managed PITR, and regular
restores into a separate test database belong in infrastructure operations.
Verify readiness and leaderboard data after a restore.

## Game policy, limits, and retention

| Variable | Default |
| --- | --- |
| `SESSION_MIN_DURATION` | `1s` |
| `SESSION_MAX_DURATION` | `1h` |
| `MAX_SCORE_PER_SECOND` | `1000` |
| `RATE_LIMIT_START_PER_MINUTE` / `RATE_LIMIT_START_BURST` | `30` / `10` |
| `RATE_LIMIT_FINISH_PER_MINUTE` / `RATE_LIMIT_FINISH_BURST` | `120` / `20` |
| `RATE_LIMIT_LEADERBOARD_PER_MINUTE` / `RATE_LIMIT_LEADERBOARD_BURST` | `300` / `30` |
| `RATE_LIMIT_GLOBAL_PER_SECOND` / `RATE_LIMIT_GLOBAL_BURST` | `50` / `100` |
| `RATE_LIMIT_MAX_CLIENTS` | `10000` |
| `CLEANUP_INTERVAL` | `5m` |
| `EXPIRED_SESSION_RETENTION` | `24h` |
| `CLEANUP_BATCH_SIZE` | `1000` |

Duration settings use Go syntax such as `5m` or `24h`. Rates and bursts are
positive integers. Tune score validation to actual gameplay before launch;
elapsed-time checks alone cannot establish that a client played a game.

Client limits group each IPv4 address or IPv6 /64. HTTP 429 includes
`Retry-After`. State is bounded, held in memory, reset on restart, and local to
each instance. Configure a shared budget at ingress when running replicas.

Each server runs cleanup immediately and every configured interval. It deletes
unfinished sessions after their expiry plus the retention period and preserves
completed scores. A sweep processes at most ten batches within 30 seconds and
skips locked rows, allowing multiple replicas to clean up concurrently. Expired
sessions return 410 while retained and 404 after deletion.

## Observability

Scrape the private metrics listener and collect JSON container logs. Requests
receive `X-Request-ID`. Logs use route templates and omit bodies, query
strings, authorization headers, and session IDs. Successful health checks are
omitted from request logs; noisy 429/unmatched logs are sampled. Metrics count
all API requests.

| Metric | Meaning |
| --- | --- |
| `spacegame_http_requests_total{method,route,status}` | Request count |
| `spacegame_http_request_duration_seconds{method,route}` | Request latency histogram |
| `spacegame_ready` | Result of the most recent readiness probe; initially zero |
| `spacegame_expired_sessions_deleted_total` | Removed unfinished sessions |
| `spacegame_cleanup_last_success_timestamp_seconds` | Last successful cleanup sweep, including empty sweeps |
| `spacegame_cleanup_errors_total` | Failed cleanup sweeps |

Go runtime and process metrics are also exposed. Poll `/readyz` regularly to
keep the readiness gauge current; scraping metrics does not run the readiness
check. Configure scrape failures, readiness failures, error rates, latency, and
stalled cleanup alerts in the infrastructure repository. A cleanup staleness
threshold must allow for its configured interval and execution time.
