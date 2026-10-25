# Database migrations

SQL migrations are managed by Goose, pinned in `go.mod`. Create a migration with:

```sh
make migrate-create NAME=create_players
```

This creates a timestamped SQL file here. Replace the generated placeholder SQL
with your schema change in `-- +goose Up` and its reversal in `-- +goose Down`.
Goose executes each migration in a transaction by default. Statements that
require running outside a transaction, such as `CREATE INDEX CONCURRENTLY`, need
the `-- +goose NO TRANSACTION` annotation.

With `DATABASE_URL` exported:

```sh
make migrate-status
make migrate-up
make migrate-down
make migrate-version
```

`migrate-down` rolls back the most recently applied migration. Commit migrations
with the code that needs them. Once a migration has been applied to a shared
database, add another migration for further changes instead of editing it.

Run migrations once as a deployment step before starting the new application
version. The HTTP server does not apply migrations on startup.
The infrastructure repository owns this job. Use `migrate up` from the same
image digest as the application rollout, with the schema owner's credentials.
See the [container contract](../docs/container.md) for runtime credentials and
readiness requirements.

`00001_create_game_sessions.sql` creates the session/result table and leaderboard
index. Completed sessions are leaderboard entries; unfinished sessions are
excluded. The session's private finish token is stored only as a SHA-256 hash.

After changing migrations or `internal/store/queries.sql`, run `make generate`
to regenerate the checked-in PostgreSQL access code with the pinned sqlc image.

For another directory, use `make migrate-up MIGRATIONS_DIR=path/to/migrations`,
or `go run ./cmd/migrate -dir path/to/migrations up`.
