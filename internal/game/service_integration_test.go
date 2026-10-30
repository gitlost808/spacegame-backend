//go:build integration

package game

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"spacegame-backend/internal/database"
	"spacegame-backend/internal/store"
)

type gatedFinishStore struct {
	store.Querier
	pauseFinish bool
	entered     chan struct{}
	resume      chan struct{}
}

func (s gatedFinishStore) wait(ctx context.Context) error {
	close(s.entered)
	select {
	case <-s.resume:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s gatedFinishStore) FinishSession(ctx context.Context, p store.FinishSessionParams) (store.GameSession, error) {
	if s.pauseFinish {
		if err := s.wait(ctx); err != nil {
			return store.GameSession{}, err
		}
	}
	return s.Querier.FinishSession(ctx, p)
}

func (s gatedFinishStore) DeleteUnfinishedSession(ctx context.Context, p store.DeleteUnfinishedSessionParams) (int64, error) {
	if !s.pauseFinish {
		if err := s.wait(ctx); err != nil {
			return 0, err
		}
	}
	return s.Querier.DeleteUnfinishedSession(ctx, p)
}

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
	for _, tt := range []struct {
		name    string
		elapsed time.Duration
		score   int64
		want    error
	}{
		{"excessive score", 2 * time.Second, 2001, ErrInvalidScore},
		{"negative score", 2 * time.Second, -1, ErrInvalidScore},
		{"too short", 500 * time.Millisecond, 0, ErrTooShort},
		{"expired", time.Hour + time.Second, 0, ErrExpired},
	} {
		t.Run("invalid_finish_deletes_"+tt.name, func(t *testing.T) {
			now = start
			run, err := service.Start(ctx, "Rejected")
			if err != nil {
				t.Fatal(err)
			}
			now = start.Add(tt.elapsed)
			if _, err := service.Finish(ctx, run.SessionID, run.SessionToken, tt.score); !errors.Is(err, tt.want) {
				t.Fatalf("invalid finish: %v, want %v", err, tt.want)
			}
			if _, err := restarted.Finish(ctx, run.SessionID, run.SessionToken, 0); !errors.Is(err, ErrNotFound) {
				t.Fatalf("a corrected retry through another instance must fail: %v", err)
			}
		})
	}
	t.Run("deletion_requires_matching_token_and_unfinished_run", func(t *testing.T) {
		now = start
		run, err := service.Start(ctx, "Protected")
		if err != nil {
			t.Fatal(err)
		}
		var id pgtype.UUID
		if err := id.Scan(run.SessionID); err != nil {
			t.Fatal(err)
		}
		badHash := sha256.Sum256([]byte("wrong-token"))
		if deleted, err := queries.DeleteUnfinishedSession(ctx, store.DeleteUnfinishedSessionParams{ID: id, TokenHash: badHash[:]}); err != nil || deleted != 0 {
			t.Fatalf("a wrong hash must not delete the session: deleted=%d error=%v", deleted, err)
		}
		now = start.Add(2 * time.Second)
		if _, err := service.Finish(ctx, run.SessionID, "wrong-token", -1); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("wrong token must be rejected before score validation: %v", err)
		}
		completed, err := service.Finish(ctx, run.SessionID, run.SessionToken, 100)
		if err != nil {
			t.Fatalf("an unauthorized request must leave the session usable: %v", err)
		}
		hash := sha256.Sum256([]byte(run.SessionToken))
		if deleted, err := queries.DeleteUnfinishedSession(ctx, store.DeleteUnfinishedSessionParams{ID: id, TokenHash: hash[:]}); err != nil || deleted != 0 {
			t.Fatalf("a completed result must never be deleted by rejection: deleted=%d error=%v", deleted, err)
		}
		if _, err := service.Finish(ctx, run.SessionID, run.SessionToken, 101); !errors.Is(err, ErrAlreadyFinished) {
			t.Fatalf("different-score retries must preserve the original result: %v", err)
		}
		if retry, err := service.Finish(ctx, run.SessionID, run.SessionToken, 100); err != nil || retry != completed {
			t.Fatalf("same-score retries must remain idempotent: result=%+v error=%v", retry, err)
		}
	})
	for _, rejectionWins := range []bool{true, false} {
		t.Run(fmt.Sprintf("concurrent_rejection_wins_%t", rejectionWins), func(t *testing.T) {
			now = start
			run, err := service.Start(ctx, "Racing")
			if err != nil {
				t.Fatal(err)
			}
			now = start.Add(2 * time.Second)
			gate := gatedFinishStore{Querier: queries, pauseFinish: rejectionWins, entered: make(chan struct{}), resume: make(chan struct{})}
			var once sync.Once
			resume := func() { once.Do(func() { close(gate.resume) }) }
			defer resume()
			paused, _ := NewService(gate, DefaultPolicy())
			paused.now = func() time.Time { return now }
			pausedScore, firstScore := int64(2001), int64(100)
			if rejectionWins {
				pausedScore, firstScore = firstScore, pausedScore
			}
			done := make(chan error, 1)
			go func() {
				_, err := paused.Finish(ctx, run.SessionID, run.SessionToken, pausedScore)
				done <- err
			}()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			_, firstErr := service.Finish(ctx, run.SessionID, run.SessionToken, firstScore)
			resume()
			var pausedErr error
			select {
			case pausedErr = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if rejectionWins {
				if !errors.Is(firstErr, ErrInvalidScore) || !errors.Is(pausedErr, ErrNotFound) {
					t.Fatalf("a winning rejection must prevent the pending finish: first=%v pending=%v", firstErr, pausedErr)
				}
			} else {
				if firstErr != nil || !errors.Is(pausedErr, ErrAlreadyFinished) {
					t.Fatalf("a winning completion must survive rejection: first=%v pending=%v", firstErr, pausedErr)
				}
				if retry, err := service.Finish(ctx, run.SessionID, run.SessionToken, 100); err != nil || retry.Score != 100 {
					t.Fatalf("completed score must remain accessible: result=%+v error=%v", retry, err)
				}
			}
		})
	}
	if err := goose.DownToContext(ctx, sqlDB, "../../migrations", 0); err != nil {
		t.Fatalf("rollback game schema: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('game_sessions') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("game_sessions after rollback: exists = %t, error = %v", exists, err)
	}
}
