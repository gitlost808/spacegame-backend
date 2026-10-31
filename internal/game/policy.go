package game

import (
	"errors"
	"time"

	"github.com/caarlos0/env/v11"
)

// Policy defines the plausibility limits for a completed run.
type Policy struct {
	MinDuration       time.Duration `env:"SESSION_MIN_DURATION"`
	MaxDuration       time.Duration `env:"SESSION_MAX_DURATION"`
	MaxScorePerSecond int64         `env:"MAX_SCORE_PER_SECOND"`
	// MaxCheckpointGap bounds the time between start, checkpoints, and finish.
	MaxCheckpointGap time.Duration `env:"SESSION_MAX_CHECKPOINT_GAP"`
}

func DefaultPolicy() Policy {
	return Policy{MinDuration: time.Second, MaxDuration: time.Hour, MaxScorePerSecond: 1000, MaxCheckpointGap: 30 * time.Second}
}

func (p Policy) Validate() error {
	if p.MinDuration < 0 || p.MaxDuration <= 0 || p.MinDuration > p.MaxDuration || p.MaxScorePerSecond <= 0 || p.MaxCheckpointGap <= 0 {
		return errors.New("session durations must satisfy 0 <= minimum <= maximum, maximum and checkpoint gap must be positive, and score rate must be positive")
	}
	return nil
}

// PolicyFromEnv reads optional limits, using defaults for unset variables.
func PolicyFromEnv() (Policy, error) {
	policy := DefaultPolicy()
	if err := env.Parse(&policy); err != nil {
		return Policy{}, err
	}
	return policy, policy.Validate()
}
