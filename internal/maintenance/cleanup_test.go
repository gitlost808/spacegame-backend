package maintenance

import (
	"context"
	"errors"
	"testing"
	"time"

	"spacegame-backend/internal/store"
)

type cleanupStore struct {
	store.Querier
	call func(store.DeleteExpiredSessionsParams) (int64, error)
}

func (s cleanupStore) DeleteExpiredSessions(_ context.Context, p store.DeleteExpiredSessionsParams) (int64, error) {
	return s.call(p)
}

func TestCleanupBatchesAndRetention(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	cleaner := Cleaner{Config: CleanupConfig{Retention: 24 * time.Hour, BatchSize: 2}, Queries: cleanupStore{
		call: func(p store.DeleteExpiredSessionsParams) (int64, error) {
			if p.Cutoff != now.Add(-24*time.Hour) || p.BatchSize != 2 {
				t.Fatalf("unexpected cleanup parameters: %+v", p)
			}
			calls++
			if calls == 1 {
				return 2, nil
			}
			return 1, nil
		},
	}}
	if deleted, err := cleaner.Sweep(context.Background(), now); err != nil || deleted != 3 || calls != 2 {
		t.Fatalf("deleted=%d calls=%d error=%v", deleted, calls, err)
	}
}

func TestCleanupBudgetAndPartialFailure(t *testing.T) {
	calls := 0
	cleaner := Cleaner{Config: CleanupConfig{BatchSize: 2}, Queries: cleanupStore{
		call: func(store.DeleteExpiredSessionsParams) (int64, error) { calls++; return 2, nil },
	}}
	if deleted, err := cleaner.Sweep(context.Background(), time.Now()); err != nil || deleted != 20 || calls != 10 {
		t.Fatalf("sweep must be bounded: deleted=%d calls=%d error=%v", deleted, calls, err)
	}
	calls = 0
	want := errors.New("database unavailable")
	cleaner.Queries = cleanupStore{call: func(store.DeleteExpiredSessionsParams) (int64, error) {
		calls++
		if calls == 1 {
			return 2, nil
		}
		return 0, want
	}}
	if deleted, err := cleaner.Sweep(context.Background(), time.Now()); deleted != 2 || !errors.Is(err, want) {
		t.Fatalf("partial sweep: deleted=%d error=%v", deleted, err)
	}
}

func TestCleanupConfig(t *testing.T) {
	for _, key := range []string{"CLEANUP_INTERVAL", "EXPIRED_SESSION_RETENTION", "CLEANUP_BATCH_SIZE"} {
		t.Setenv(key, "")
	}
	c, err := CleanupConfigFromEnv()
	if err != nil || c.Interval != 5*time.Minute || c.Retention != 24*time.Hour || c.BatchSize != 1000 {
		t.Fatalf("defaults=%+v error=%v", c, err)
	}
	t.Setenv("EXPIRED_SESSION_RETENTION", "-1s")
	if _, err := CleanupConfigFromEnv(); err == nil {
		t.Fatal("negative retention must fail")
	}
}
