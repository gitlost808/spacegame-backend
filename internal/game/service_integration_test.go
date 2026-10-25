//go:build integration

package game

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"spacegame-backend/internal/database"
	"spacegame-backend/internal/store"
)

func TestPostgresGameSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base, err := database.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := fmt.Sprintf("game_test_%d", time.Now().UnixNano())
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
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, sqlDB, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	queries := store.New(pool)
	service, _ := NewService(queries, DefaultPolicy())
	now := time.Now().UTC().Truncate(time.Microsecond)
	service.now = func() time.Time { return now }
	start := now
	alpha, err := service.Start(ctx, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := service.Start(ctx, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.Start(ctx, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, "Unfinished"); err != nil {
		t.Fatal(err)
	}
	if entries, err := service.Leaderboard(ctx, 20); err != nil || len(entries) != 0 {
		t.Fatalf("unfinished runs must not appear: %+v, %v", entries, err)
	}
	// A fresh service instance must be able to finish a persisted session.
	restarted, _ := NewService(queries, DefaultPolicy())
	restarted.now = func() time.Time { return now }
	for i, run := range []struct {
		session StartedSession
		score   int64
	}{{alpha, 100}, {beta, 200}, {again, 200}} {
		now = start.Add(time.Duration(10+i) * time.Second)
		result, err := restarted.Finish(ctx, run.session.SessionID, run.session.SessionToken, run.score)
		if err != nil || result.Score != run.score || result.DurationMS != int64(10+i)*1000 {
			t.Fatalf("finish = %+v, error = %v", result, err)
		}
	}
	entries, err := service.Leaderboard(ctx, 20)
	if err != nil || len(entries) != 3 {
		t.Fatalf("leaderboard = %+v, error = %v", entries, err)
	}
	for i, id := range []string{beta.SessionID, again.SessionID, alpha.SessionID} {
		if entries[i].SessionID != id || entries[i].Rank != i+1 {
			t.Errorf("rank %d: %+v", i+1, entries[i])
		}
	}
	if top, err := service.Leaderboard(ctx, 1); err != nil || len(top) != 1 || top[0].SessionID != beta.SessionID {
		t.Fatalf("limited leaderboard = %+v, error = %v", top, err)
	}
	for _, sameScore := range []bool{true, false} {
		t.Run(fmt.Sprintf("concurrent_finish_same_score_%t", sameScore), func(t *testing.T) {
			now = start
			run, err := service.Start(ctx, "Concurrent")
			if err != nil {
				t.Fatal(err)
			}
			now = start.Add(20 * time.Second)
			scores := []int64{300, 300}
			if !sameScore {
				scores[1] = 400
			}
			var wait sync.WaitGroup
			gate := make(chan struct{})
			results := make([]Result, 2)
			errorsFound := make([]error, 2)
			for i, score := range scores {
				wait.Add(1)
				go func() {
					defer wait.Done()
					<-gate
					results[i], errorsFound[i] = service.Finish(ctx, run.SessionID, run.SessionToken, score)
				}()
			}
			close(gate)
			wait.Wait()
			successes, conflicts := 0, 0
			for _, err := range errorsFound {
				if err == nil {
					successes++
				} else if errors.Is(err, ErrAlreadyFinished) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if sameScore && (successes != 2 || results[0] != results[1]) || !sameScore && (successes != 1 || conflicts != 1) {
				t.Fatalf("concurrent finish: successes = %d, conflicts = %d, results = %+v", successes, conflicts, results)
			}
		})
	}
	if err := goose.DownContext(ctx, sqlDB, "../../migrations"); err != nil {
		t.Fatalf("rollback game schema: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('game_sessions') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("game_sessions after rollback: exists = %t, error = %v", exists, err)
	}
}
