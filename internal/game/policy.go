package game

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Policy defines the plausibility limits for a completed run.
type Policy struct {
	MinDuration       time.Duration
	MaxDuration       time.Duration
	MaxScorePerSecond int64
}

func DefaultPolicy() Policy {
	return Policy{MinDuration: time.Second, MaxDuration: time.Hour, MaxScorePerSecond: 1000}
}

func (p Policy) Validate() error {
	if p.MinDuration < 0 || p.MaxDuration <= 0 || p.MinDuration > p.MaxDuration || p.MaxScorePerSecond <= 0 {
		return errors.New("session durations must satisfy 0 <= minimum <= maximum, maximum must be positive, and score rate must be positive")
	}
	return nil
}

// PolicyFromEnv reads optional limits, using defaults for unset variables.
func PolicyFromEnv() (Policy, error) {
	policy := DefaultPolicy()
	for _, field := range []struct {
		name  string
		value *time.Duration
	}{
		{"SESSION_MIN_DURATION", &policy.MinDuration},
		{"SESSION_MAX_DURATION", &policy.MaxDuration},
	} {
		if value := os.Getenv(field.name); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return Policy{}, fmt.Errorf("%s must be a Go duration: %w", field.name, err)
			}
			*field.value = duration
		}
	}
	if value := os.Getenv("MAX_SCORE_PER_SECOND"); value != "" {
		rate, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Policy{}, fmt.Errorf("MAX_SCORE_PER_SECOND must be an integer: %w", err)
		}
		policy.MaxScorePerSecond = rate
	}
	return policy, policy.Validate()
}
