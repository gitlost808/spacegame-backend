//go:build integration

package database_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"spacegame-backend/internal/database"
	"spacegame-backend/internal/store"
	"spacegame-backend/migrations"
)

func TestSchemaReadinessAndCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := fmt.Sprintf("readiness_test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := base.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	config, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	versions, err := migrations.Versions()
	if err != nil {
		t.Fatal(err)
	}
	ready := database.Readiness{Pool: pool, Versions: versions}
	if err := ready.CheckReady(ctx); err == nil {
		t.Fatal("empty database must not be ready")
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("readiness must not create migration metadata: exists=%t error=%v", exists, err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, sqlDB, "../../migrations", 1); err != nil {
		t.Fatal(err)
	}
	if err := ready.CheckReady(ctx); err == nil {
		t.Fatal("missing cleanup migration must make readiness fail")
	}
	if err := goose.UpContext(ctx, sqlDB, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if err := ready.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE game_sessions RENAME COLUMN score TO missing_score"); err != nil {
		t.Fatal(err)
	}
	if err := ready.CheckReady(ctx); err == nil {
		t.Fatal("schema drift must make readiness fail")
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE game_sessions RENAME COLUMN missing_score TO score"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownContext(ctx, sqlDB, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if err := ready.CheckReady(ctx); err == nil {
		t.Fatal("rolled-back required migration must make readiness fail")
	}
	if err := goose.UpContext(ctx, sqlDB, "../../migrations"); err != nil {
		t.Fatal(err)
	}

	queries := store.New(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	create := func(name string, start, expiry time.Time) store.GameSession {
		t.Helper()
		run, err := queries.CreateSession(ctx, store.CreateSessionParams{
			PlayerName: name, TokenHash: make([]byte, 32), StartedAt: start, ExpiresAt: expiry,
		})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	old := create("old", now.Add(-72*time.Hour), now.Add(-48*time.Hour))
	locked := create("locked", now.Add(-72*time.Hour), now.Add(-48*time.Hour))
	recent := create("recent", now.Add(-2*time.Hour), now.Add(-time.Hour))
	active := create("active", now, now.Add(time.Hour))
	completed := create("completed", now.Add(-72*time.Hour), now.Add(-48*time.Hour))
	if _, err := queries.FinishSession(ctx, store.FinishSessionParams{
		ID: completed.ID, TokenHash: completed.TokenHash,
		Score:      pgtype.Int8{Int64: 50, Valid: true},
		FinishedAt: pgtype.Timestamptz{Time: completed.StartedAt.Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "SELECT id FROM game_sessions WHERE id=$1 FOR UPDATE", locked.ID); err != nil {
		t.Fatal(err)
	}
	parameters := store.DeleteExpiredSessionsParams{Cutoff: now.Add(-24 * time.Hour), BatchSize: 1}
	if deleted, err := queries.DeleteExpiredSessions(ctx, parameters); err != nil || deleted != 1 {
		t.Fatalf("cleanup should skip locked row: deleted=%d error=%v", deleted, err)
	}
	if _, err := queries.GetSession(ctx, old.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("old expired session must be deleted: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if deleted, err := queries.DeleteExpiredSessions(ctx, parameters); err != nil || deleted != 1 {
		t.Fatalf("unlocked expired row: deleted=%d error=%v", deleted, err)
	}
	if deleted, err := queries.DeleteExpiredSessions(ctx, parameters); err != nil || deleted != 0 {
		t.Fatalf("remaining sessions must be preserved: deleted=%d error=%v", deleted, err)
	}
	for _, session := range []store.GameSession{recent, active, completed} {
		if _, err := queries.GetSession(ctx, session.ID); err != nil {
			t.Errorf("session %s was incorrectly deleted: %v", session.PlayerName, err)
		}
	}
	if err := ready.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
}
