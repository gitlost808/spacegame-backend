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
	ErrExpired          = errors.New("session has expired")
	ErrTooShort         = errors.New("session duration is below the configured minimum")
	ErrInvalidScore     = errors.New("score must be non-negative and within the configured score-per-second limit")
	ErrInvalidLimit     = errors.New("limit must be between 1 and 100")
)

type StartedSession struct {
	SessionID    string    `json:"session_id"`
	SessionToken string    `json:"session_token"`
	PlayerName   string    `json:"player_name"`
	StartedAt    time.Time `json:"started_at"`
	ExpiresAt    time.Time `json:"expires_at"`
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
		StartedAt: session.StartedAt, ExpiresAt: session.ExpiresAt,
	}, nil
}

func (s *Service) Finish(ctx context.Context, id, token string, score int64) (Result, error) {
	var sessionID pgtype.UUID
	if err := sessionID.Scan(id); err != nil || !sessionID.Valid {
		return Result{}, ErrInvalidSessionID
	}
	session, err := s.queries.GetSession(ctx, sessionID)
	if err != nil {
		return Result{}, sessionError(err)
	}
	hash := sha256.Sum256([]byte(token))
	if token == "" || subtle.ConstantTimeCompare(hash[:], session.TokenHash) != 1 {
		return Result{}, ErrUnauthorized
	}
	if session.FinishedAt.Valid {
		return existingResult(session, score)
	}
	finishedAt := s.now().UTC().Truncate(time.Microsecond)
	if finishedAt.After(session.ExpiresAt) {
		return s.rejectFinish(ctx, sessionID, hash[:], score, ErrExpired)
	}
	elapsed := finishedAt.Sub(session.StartedAt)
	if elapsed < s.policy.MinDuration {
		return s.rejectFinish(ctx, sessionID, hash[:], score, ErrTooShort)
	}
	if score < 0 || float64(score) > float64(s.policy.MaxScorePerSecond)*elapsed.Seconds() {
		return s.rejectFinish(ctx, sessionID, hash[:], score, ErrInvalidScore)
	}
	session, err = s.queries.FinishSession(ctx, store.FinishSessionParams{
		ID: sessionID, TokenHash: hash[:],
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

func (s *Service) rejectFinish(ctx context.Context, id pgtype.UUID, tokenHash []byte, score int64, reason error) (Result, error) {
	deleted, err := s.queries.DeleteUnfinishedSession(ctx, store.DeleteUnfinishedSessionParams{
		ID: id, TokenHash: tokenHash,
	})
	if err != nil {
		return Result{}, fmt.Errorf("delete rejected session: %w", err)
	}
	if deleted > 0 {
		return Result{}, reason
	}
	// A concurrent finish may have completed the run, or another rejection deleted it.
	session, err := s.queries.GetSession(ctx, id)
	if err != nil {
		return Result{}, sessionError(err)
	}
	return existingResult(session, score)
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
