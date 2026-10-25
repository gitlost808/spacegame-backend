//go:build integration

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestMigrationLifecycle(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Keep test tables and Goose metadata separate from application data.
	schema := fmt.Sprintf("migrate_test_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("remove test schema: %v", err)
		}
	}()
	previousTable := goose.TableName()
	goose.SetTableName(schema + ".goose_db_version")
	defer goose.SetTableName(previousTable)

	dir := t.TempDir()
	for _, command := range []string{"up", "status"} {
		if err := run(ctx, []string{"-dir", dir, command}, databaseURL); err != nil {
			t.Fatalf("%s with no migrations: %v", command, err)
		}
	}
	migration := fmt.Sprintf(`-- +goose Up
CREATE TABLE %[1]s.records (id integer PRIMARY KEY, name text NOT NULL);
INSERT INTO %[1]s.records (id, name) VALUES (1, 'migration test');

-- +goose Down
DROP TABLE %[1]s.records;
`, schema)
	if err := os.WriteFile(filepath.Join(dir, "00001_create_records.sql"), []byte(migration), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"up", "up", "status", "version"} {
		if err := run(ctx, []string{"-dir", dir, command}, databaseURL); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	var name string
	if err := db.QueryRowContext(ctx, "SELECT name FROM "+schema+".records WHERE id = 1").Scan(&name); err != nil || name != "migration test" {
		t.Fatalf("migrated record = %q, error = %v", name, err)
	}

	failing := fmt.Sprintf(`-- +goose Up
CREATE TABLE %[1]s.rolled_back (id integer);
INSERT INTO %[1]s.missing_table VALUES (1);

-- +goose Down
DROP TABLE %[1]s.rolled_back;
`, schema)
	if err := os.WriteFile(filepath.Join(dir, "00002_failing.sql"), []byte(failing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"-dir", dir, "up"}, databaseURL); err == nil {
		t.Fatal("expected the invalid migration to fail")
	}
	assertTableAbsent(t, ctx, db, schema+".rolled_back")
	if version, err := goose.GetDBVersionContext(ctx, db); err != nil || version != 1 {
		t.Fatalf("version after failed migration = %d, error = %v; want 1", version, err)
	}
	if err := run(ctx, []string{"-dir", dir, "down"}, databaseURL); err != nil {
		t.Fatal(err)
	}
	assertTableAbsent(t, ctx, db, schema+".records")
	if version, err := goose.GetDBVersionContext(ctx, db); err != nil || version != 0 {
		t.Fatalf("version after rollback = %d, error = %v; want 0", version, err)
	}
}

func assertTableAbsent(t *testing.T, ctx context.Context, db *sql.DB, table string) {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("table %s should not exist", table)
	}
}
