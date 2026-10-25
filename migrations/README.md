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

This directory initially contains no SQL migrations because the game schema has
not been defined. `migrate-up` and `migrate-status` report that there are no SQL
migrations after verifying the database connection.

For another directory, use `make migrate-up MIGRATIONS_DIR=path/to/migrations`,
or `go run ./cmd/migrate -dir path/to/migrations up`.
