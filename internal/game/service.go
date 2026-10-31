package game

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"spacegame-backend/internal/namefilter"
	"spacegame-backend/internal/store"
)

var (
	ErrInvalidName      = errors.New("player_name must contain 1 to 32 characters without control characters")
	ErrDisallowedName   = errors.New("please choose another player name")
	ErrInvalidSessionID = errors.New("session_id must be a UUID")
	ErrUnauthorized     = errors.New("a valid session token is required")
	ErrNotFound         = errors.New("session not found")
	ErrAlreadyFinished  = errors.New("session has already finished with a different score")
	ErrSessionFinished  = errors.New("session has already finished")
	ErrRunRejected      = errors.New("run rejected")
	ErrInvalidLimit     = errors.New("limit must be between 1 and 100")
)

// Rejection reasons wrap ErrRunRejected for logs and tests. Clients only see
// ErrRunRejected so responses do not reveal which plausibility limit failed.
var (
	errExpired          = errors.New("session has expired")
	errTooShort         = errors.New("session duration is below the minimum")
	errInvalidScore     = errors.New("score progress is not plausible")
	errMissedCheckpoint = errors.New("checkpoint gap exceeded")
)

type StartedSession struct {
	SessionID    string    `json:"session_id"`
	SessionToken string    `json:"session_token"`
	PlayerName   string    `json:"player_name"`
	StartedAt    time.Time `json:"started_at"`
}

type Result struct {
	SessionID  string    `json:"session_id"`
	PlayerName string    `json:"player_name"`
	Score      int64     `json:"score"`
	DurationMS int64     `json:"duration_ms"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

type Entry struct {
	Rank int `json:"rank"`
	Result
}

// Service owns session timing, result validation, and leaderboard access.
type Service struct {
	queries store.Querier
	policy  Policy
	now     func() time.Time
}

func NewService(queries store.Querier, policy Policy) (*Service, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Service{queries: queries, policy: policy, now: time.Now}, nil
}

func (s *Service) Start(ctx context.Context, name string) (StartedSession, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 32 || strings.ContainsFunc(name, unicode.IsControl) {
		return StartedSession{}, ErrInvalidName
	}
	if namefilter.Disallowed(name) {
		return StartedSession{}, ErrDisallowedName
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return StartedSession{}, fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	startedAt := s.now().UTC().Truncate(time.Microsecond)
	session, err := s.queries.CreateSession(ctx, store.CreateSessionParams{
		PlayerName: name,
		TokenHash:  hash[:],
		StartedAt:  startedAt,
		ExpiresAt:  startedAt.Add(s.policy.MaxDuration),
	})
	if err != nil {
		return StartedSession{}, fmt.Errorf("start session: %w", err)
	}
	return StartedSession{
		SessionID: session.ID.String(), SessionToken: token, PlayerName: session.PlayerName,
		StartedAt: session.StartedAt,
	}, nil
}

// Checkpoint records the run's current total score. Runs must report progress
// at least every MaxCheckpointGap, so an idle session cannot claim a score later.
func (s *Service) Checkpoint(ctx context.Context, id, token string, score int64) error {
	sessionID, session, hash, err := s.authenticate(ctx, id, token)
	if err != nil {
		return err
	}
	if session.FinishedAt.Valid {
		return ErrSessionFinished
	}
	at := s.now().UTC().Truncate(time.Microsecond)
	if reason := s.checkProgress(session, at, score); reason != nil {
		if _, err := s.reject(ctx, sessionID, hash, reason); err != nil {
			return err
		}
		return ErrSessionFinished
	}
	recorded, err := s.queries.RecordCheckpoint(ctx, store.RecordCheckpointParams{
		ID: sessionID, TokenHash: hash, CheckpointScore: score, CheckpointAt: at,
	})
	if err != nil {
		return fmt.Errorf("record checkpoint: %w", err)
	}
	if recorded == 0 {
		// A concurrent finish, rejection, or newer checkpoint won the conditional update.
		session, err := s.queries.GetSession(ctx, sessionID)
		if err != nil {
			return sessionError(err)
		}
		if session.FinishedAt.Valid {
			return ErrSessionFinished
		}
	}
	return nil
}

func (s *Service) Finish(ctx context.Context, id, token string, score int64) (Result, error) {
	sessionID, session, hash, err := s.authenticate(ctx, id, token)
	if err != nil {
		return Result{}, err
	}
	if session.FinishedAt.Valid {
		return existingResult(session, score)
	}
	finishedAt := s.now().UTC().Truncate(time.Microsecond)
	reason := s.checkProgress(session, finishedAt, score)
	if reason == nil && finishedAt.Sub(session.StartedAt) < s.policy.MinDuration {
		reason = errTooShort
	}
	if reason != nil {
		session, err := s.reject(ctx, sessionID, hash, reason)
		if err != nil {
			return Result{}, err
		}
		return existingResult(session, score)
	}
	session, err = s.queries.FinishSession(ctx, store.FinishSessionParams{
		ID: sessionID, TokenHash: hash,
		Score:      pgtype.Int8{Int64: score, Valid: true},
		FinishedAt: pgtype.Timestamptz{Time: finishedAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Another finish request may have won the conditional update.
		session, err = s.queries.GetSession(ctx, sessionID)
		if err != nil {
			return Result{}, sessionError(err)
		}
		return existingResult(session, score)
	}
	if err != nil {
		return Result{}, fmt.Errorf("finish session: %w", err)
	}
	return result(session), nil
}

func (s *Service) authenticate(ctx context.Context, id, token string) (pgtype.UUID, store.GameSession, []byte, error) {
	var sessionID pgtype.UUID
	if err := sessionID.Scan(id); err != nil || !sessionID.Valid {
		return sessionID, store.GameSession{}, nil, ErrInvalidSessionID
	}
	session, err := s.queries.GetSession(ctx, sessionID)
	if err != nil {
		return sessionID, store.GameSession{}, nil, sessionError(err)
	}
	hash := sha256.Sum256([]byte(token))
	if token == "" || subtle.ConstantTimeCompare(hash[:], session.TokenHash) != 1 {
		return sessionID, store.GameSession{}, nil, ErrUnauthorized
	}
	return sessionID, session, hash[:], nil
}

// checkProgress validates the score reported at `at` against the last checkpoint.
func (s *Service) checkProgress(session store.GameSession, at time.Time, score int64) error {
	// Clock skew between instances must not place the last checkpoint in the future.
	since := max(at.Sub(session.CheckpointAt), 0)
	switch {
	case at.After(session.ExpiresAt):
		return errExpired
	case since > s.policy.MaxCheckpointGap:
		return errMissedCheckpoint
	case score < session.CheckpointScore || float64(score-session.CheckpointScore) > float64(s.policy.MaxScorePerSecond)*since.Seconds():
		return errInvalidScore
	}
	return nil
}

// reject deletes an unfinished run. If the run was already finished or deleted
// concurrently, it returns the current state instead.
func (s *Service) reject(ctx context.Context, id pgtype.UUID, tokenHash []byte, reason error) (store.GameSession, error) {
	deleted, err := s.queries.DeleteUnfinishedSession(ctx, store.DeleteUnfinishedSessionParams{
		ID: id, TokenHash: tokenHash,
	})
	if err != nil {
		return store.GameSession{}, fmt.Errorf("delete rejected session: %w", err)
	}
	if deleted > 0 {
		return store.GameSession{}, fmt.Errorf("%w: %w", ErrRunRejected, reason)
	}
	// A concurrent finish may have completed the run, or another rejection deleted it.
	session, err := s.queries.GetSession(ctx, id)
	if err != nil {
		return store.GameSession{}, sessionError(err)
	}
	return session, nil
}

func (s *Service) Leaderboard(ctx context.Context, limit int32) ([]Entry, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidLimit
	}
	rows, err := s.queries.ListLeaderboard(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list leaderboard: %w", err)
	}
	entries := make([]Entry, 0, len(rows))
	for i, row := range rows {
		entries = append(entries, Entry{Rank: i + 1, Result: Result{
			SessionID: row.ID.String(), PlayerName: row.PlayerName, Score: row.Score.Int64,
			StartedAt: row.StartedAt, FinishedAt: row.FinishedAt.Time,
			DurationMS: row.FinishedAt.Time.Sub(row.StartedAt).Milliseconds(),
		}})
	}
	return entries, nil
}

func existingResult(session store.GameSession, score int64) (Result, error) {
	if !session.FinishedAt.Valid || session.Score.Int64 != score {
		return Result{}, ErrAlreadyFinished
	}
	return result(session), nil
}

func result(session store.GameSession) Result {
	return Result{
		SessionID: session.ID.String(), PlayerName: session.PlayerName, Score: session.Score.Int64,
		StartedAt: session.StartedAt, FinishedAt: session.FinishedAt.Time,
		DurationMS: session.FinishedAt.Time.Sub(session.StartedAt).Milliseconds(),
	}
}

func sessionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("load session: %w", err)
}
