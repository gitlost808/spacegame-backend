package maintenance

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"

	"spacegame-backend/internal/store"
)

type CleanupConfig struct {
	Interval  time.Duration `env:"CLEANUP_INTERVAL"`
	Retention time.Duration `env:"EXPIRED_SESSION_RETENTION"`
	BatchSize int32         `env:"CLEANUP_BATCH_SIZE"`
}

func CleanupConfigFromEnv() (CleanupConfig, error) {
	config := CleanupConfig{Interval: 5 * time.Minute, Retention: 24 * time.Hour, BatchSize: 1000}
	if err := env.Parse(&config); err != nil {
		return config, err
	}
	return config, config.Validate()
}

func (c CleanupConfig) Validate() error {
	if c.Interval < time.Second || c.Retention < 0 || c.BatchSize < 1 || c.BatchSize > 10000 {
		return errors.New("cleanup interval must be at least 1s, retention must be non-negative, and batch size must be 1 to 10000")
	}
	return nil
}

type Cleaner struct {
	Queries store.Querier
	Config  CleanupConfig
	Logger  *slog.Logger
	Observe func(deleted int64, err error)
}

// Sweep removes at most ten batches of expired, unfinished sessions.
func (c Cleaner) Sweep(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for range 10 {
		deleted, err := c.Queries.DeleteExpiredSessions(ctx, store.DeleteExpiredSessionsParams{
			Cutoff: now.Add(-c.Config.Retention), BatchSize: c.Config.BatchSize,
		})
		total += deleted
		if err != nil {
			return total, err
		}
		if deleted < int64(c.Config.BatchSize) {
			return total, nil
		}
	}
	return total, nil
}

func (c Cleaner) Run(ctx context.Context) {
	ticker := time.NewTicker(c.Config.Interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		deleted, err := c.Sweep(sweepCtx, time.Now().UTC())
		cancel()
		if c.Observe != nil {
			c.Observe(deleted, err)
		}
		if err != nil && ctx.Err() == nil {
			c.Logger.Error("expired session cleanup failed", "error", err)
		} else if deleted > 0 {
			c.Logger.Info("expired sessions deleted", "count", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
