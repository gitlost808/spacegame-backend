package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"spacegame-backend/internal/store"
)

type CleanupConfig struct {
	Interval  time.Duration
	Retention time.Duration
	BatchSize int32
}

func CleanupConfigFromEnv() (CleanupConfig, error) {
	config := CleanupConfig{Interval: 5 * time.Minute, Retention: 24 * time.Hour, BatchSize: 1000}
	for _, item := range []struct {
		name string
		dst  *time.Duration
	}{{"CLEANUP_INTERVAL", &config.Interval}, {"EXPIRED_SESSION_RETENTION", &config.Retention}} {
		if value := os.Getenv(item.name); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return config, fmt.Errorf("invalid %s: %w", item.name, err)
			}
			*item.dst = duration
		}
	}
	if value := os.Getenv("CLEANUP_BATCH_SIZE"); value != "" {
		size, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return config, errors.New("CLEANUP_BATCH_SIZE must be an integer")
		}
		config.BatchSize = int32(size)
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
