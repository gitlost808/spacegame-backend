//go:build integration

package database_test

import (
	"context"
	"os"
	"testing"
	"time"

	"spacegame-backend/internal/database"
)

func TestPostgresRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = tx.Rollback(cleanupCtx)
	}()

	if _, err := tx.Exec(ctx, "CREATE TEMP TABLE scaffold_test (id integer PRIMARY KEY, name text NOT NULL) ON COMMIT DROP"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO scaffold_test (id, name) VALUES ($1, $2)", 1, "spacegame"); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := tx.QueryRow(ctx, "SELECT name FROM scaffold_test WHERE id = $1", 1).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "spacegame" {
		t.Errorf("name = %q, want spacegame", name)
	}
}
