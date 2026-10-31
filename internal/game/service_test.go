package game

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"spacegame-backend/internal/store"
)

type stubStore struct {
	store.Querier
	create     func(store.CreateSessionParams) (store.GameSession, error)
	get        func() (store.GameSession, error)
	finish     func(store.FinishSessionParams) (store.GameSession, error)
	delete     func(store.DeleteUnfinishedSessionParams) (int64, error)
	checkpoint func(store.RecordCheckpointParams) (int64, error)
	list       func(int32) ([]store.ListLeaderboardRow, error)
}

func (s stubStore) CreateSession(_ context.Context, p store.CreateSessionParams) (store.GameSession, error) {
	return s.create(p)
}
func (s stubStore) GetSession(context.Context, pgtype.UUID) (store.GameSession, error) {
	return s.get()
}
func (s stubStore) FinishSession(_ context.Context, p store.FinishSessionParams) (store.GameSession, error) {
	return s.finish(p)
}
func (s stubStore) DeleteUnfinishedSession(_ context.Context, p store.DeleteUnfinishedSessionParams) (int64, error) {
	return s.delete(p)
}
func (s stubStore) RecordCheckpoint(_ context.Context, p store.RecordCheckpointParams) (int64, error) {
	return s.checkpoint(p)
}
func (s stubStore) ListLeaderboard(_ context.Context, limit int32) ([]store.ListLeaderboardRow, error) {
	return s.list(limit)
}

func fixture() store.GameSession {
	var id pgtype.UUID
	_ = id.Scan("00000000-0000-0000-0000-000000000001")
	hash := sha256.Sum256([]byte("test-token"))
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return store.GameSession{ID: id, PlayerName: "Pilot", TokenHash: hash[:], StartedAt: start, ExpiresAt: start.Add(time.Hour), CheckpointAt: start}
}

func TestStart(t *testing.T) {
	var parameters store.CreateSessionParams
	db := stubStore{create: func(p store.CreateSessionParams) (store.GameSession, error) {
		parameters = p
		session := fixture()
		session.PlayerName, session.StartedAt, session.ExpiresAt = p.PlayerName, p.StartedAt, p.ExpiresAt
		return session, nil
	}}
	service, _ := NewService(db, DefaultPolicy())
	now := fixture().StartedAt
	service.now = func() time.Time { return now }
	started, err := service.Start(context.Background(), "  Pilot 🚀  ")
	if err != nil {
		t.Fatal(err)
	}
	if started.PlayerName != "Pilot 🚀" || started.StartedAt != now || parameters.ExpiresAt != now.Add(time.Hour) {
		t.Fatalf("unexpected session: %+v", started)
	}
	hash := sha256.Sum256([]byte(started.SessionToken))
	if len(started.SessionToken) != 43 || string(parameters.TokenHash) != string(hash[:]) {
		t.Fatal("store must receive the token's SHA-256 hash, with a 256-bit random token returned to the client")
	}
	second, err := service.Start(context.Background(), "Pilot")
	if err != nil || second.SessionToken == started.SessionToken {
		t.Fatal("each session must receive a new private token")
	}
}

func TestInvalidPlayerNames(t *testing.T) {
	service, _ := NewService(stubStore{}, DefaultPolicy())
	for _, name := range []string{"", "   ", strings.Repeat("🚀", 33), "Pilot\nTwo", "Pilot\x00"} {
		if _, err := service.Start(context.Background(), name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("name %q: error = %v, want invalid name", name, err)
		}
	}
}

func TestDisallowedPlayerNamesAreNotSaved(t *testing.T) {
	service, _ := NewService(stubStore{create: func(store.CreateSessionParams) (store.GameSession, error) {
		t.Fatal("a rejected name must not create a database row")
		return store.GameSession{}, nil
	}}, DefaultPolicy())
	for _, name := range []string{"  xxFuckxx  ", "Scheiße", "f\u200buck", "sch3i$$3", "ADOLF HILTER 88 HEIL", "S13g H31l", "14/88"} {
		if _, err := service.Start(context.Background(), name); !errors.Is(err, ErrDisallowedName) {
			t.Errorf("name %q: error=%v, want a disallowed name", name, err)
		}
	}
}

func TestAcceptedNamesKeepOriginalSpelling(t *testing.T) {
	service, _ := NewService(stubStore{create: func(p store.CreateSessionParams) (store.GameSession, error) {
		if p.PlayerName != "Müller 🚀" {
			t.Fatalf("comparison normalization must not change the stored name: %q", p.PlayerName)
		}
		session := fixture()
		session.PlayerName = p.PlayerName
		return session, nil
	}}, DefaultPolicy())
	started, err := service.Start(context.Background(), "  Müller 🚀  ")
	if err != nil || started.PlayerName != "Müller 🚀" {
		t.Fatalf("display name=%q error=%v", started.PlayerName, err)
	}
}

func TestFinishValidation(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		score   int64
		token   string
		want    error
	}{
		{"valid", 10 * time.Second, 1000, "test-token", nil},
		{"zero score", time.Second, 0, "test-token", nil},
		{"at score bound", time.Second, 1000, "test-token", nil},
		{"fractional duration", 1500 * time.Millisecond, 1500, "test-token", nil},
		{"above score bound", time.Second, 1001, "test-token", errInvalidScore},
		{"negative score", 10 * time.Second, -1, "test-token", errInvalidScore},
		{"too short", 999 * time.Millisecond, 0, "test-token", errTooShort},
		{"clock before start", -time.Second, 0, "test-token", errTooShort},
		{"expired", time.Hour + time.Microsecond, 0, "test-token", errExpired},
		{"missed checkpoint", 31 * time.Second, 1000, "test-token", errMissedCheckpoint},
		{"wrong token", 10 * time.Second, 1000, "wrong", ErrUnauthorized},
		{"missing token", 10 * time.Second, 1000, "", ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := fixture()
			writes := 0
			deleted := false
			db := stubStore{
				get: func() (store.GameSession, error) {
					if deleted {
						return store.GameSession{}, pgx.ErrNoRows
					}
					return session, nil
				},
				delete: func(p store.DeleteUnfinishedSessionParams) (int64, error) {
					if p.ID != session.ID || string(p.TokenHash) != string(session.TokenHash) {
						t.Fatal("rejection must target only the authenticated session")
					}
					deleted = true
					return 1, nil
				},
				finish: func(p store.FinishSessionParams) (store.GameSession, error) {
					writes++
					session.Score, session.FinishedAt = p.Score, p.FinishedAt
					return session, nil
				},
			}
			service, _ := NewService(db, DefaultPolicy())
			service.now = func() time.Time { return session.StartedAt.Add(tt.elapsed) }
			result, err := service.Finish(context.Background(), session.ID.String(), tt.token, tt.score)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if tt.want != nil && writes != 0 {
				t.Fatal("an invalid result must not be stored")
			}
			if tt.want == nil && (writes != 1 || result.Score != tt.score || result.DurationMS != tt.elapsed.Milliseconds()) {
				t.Fatalf("unexpected result %+v, writes = %d", result, writes)
			}
			wantDeletion := tt.want != nil && tt.want != ErrUnauthorized
			if deleted != wantDeletion {
				t.Fatalf("deleted=%t, want %t", deleted, wantDeletion)
			}
			if wantDeletion && !errors.Is(err, ErrRunRejected) {
				t.Fatalf("rejections must expose only ErrRunRejected to callers: %v", err)
			}
			if deleted {
				if _, err := service.Finish(context.Background(), session.ID.String(), "test-token", 0); !errors.Is(err, ErrNotFound) {
					t.Fatalf("a rejected session must not be retryable: %v", err)
				}
			}
		})
	}
}

func TestRejectedFinishDeletionFailure(t *testing.T) {
	session := fixture()
	want := errors.New("database unavailable")
	service, _ := NewService(stubStore{
		get:    func() (store.GameSession, error) { return session, nil },
		delete: func(store.DeleteUnfinishedSessionParams) (int64, error) { return 0, want },
	}, DefaultPolicy())
	service.now = func() time.Time { return session.StartedAt.Add(time.Second) }
	if _, err := service.Finish(context.Background(), session.ID.String(), "test-token", -1); !errors.Is(err, want) {
		t.Fatalf("failed deletion must report the database error: %v", err)
	}
}

func TestRejectedFinishLostRace(t *testing.T) {
	for _, winner := range []int64{0, 100} {
		session := fixture()
		service, _ := NewService(stubStore{
			get: func() (store.GameSession, error) { return session, nil },
			delete: func(store.DeleteUnfinishedSessionParams) (int64, error) {
				session.FinishedAt = pgtype.Timestamptz{Time: session.StartedAt.Add(time.Second), Valid: true}
				session.Score = pgtype.Int8{Int64: winner, Valid: true}
				return 0, nil
			},
		}, DefaultPolicy())
		service.now = func() time.Time { return session.StartedAt.Add(500 * time.Millisecond) }
		result, err := service.Finish(context.Background(), session.ID.String(), "test-token", 0)
		if winner == 0 && (err != nil || result.Score != 0) || winner != 0 && !errors.Is(err, ErrAlreadyFinished) {
			t.Fatalf("a completed run must survive concurrent rejection: result=%+v error=%v", result, err)
		}
	}
}

func TestFinishRetryAndConflict(t *testing.T) {
	session := fixture()
	writes := 0
	service, _ := NewService(stubStore{
		get: func() (store.GameSession, error) { return session, nil },
		finish: func(p store.FinishSessionParams) (store.GameSession, error) {
			writes++
			session.Score, session.FinishedAt = p.Score, p.FinishedAt
			return session, nil
		},
	}, DefaultPolicy())
	now := session.StartedAt.Add(10 * time.Second)
	service.now = func() time.Time { return now }
	first, err := service.Finish(context.Background(), session.ID.String(), "test-token", 500)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	retry, err := service.Finish(context.Background(), session.ID.String(), "test-token", 500)
	if err != nil || retry != first || writes != 1 {
		t.Fatalf("retry = %+v, error = %v, writes = %d", retry, err, writes)
	}
	if _, err := service.Finish(context.Background(), session.ID.String(), "test-token", 600); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("different score: %v", err)
	}
	if _, err := service.Finish(context.Background(), session.ID.String(), "wrong", 500); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("retry must still require authentication: %v", err)
	}
}

func TestFinishLostRace(t *testing.T) {
	for _, winner := range []int64{500, 600} {
		session := fixture()
		service, _ := NewService(stubStore{
			get: func() (store.GameSession, error) { return session, nil },
			finish: func(p store.FinishSessionParams) (store.GameSession, error) {
				session.FinishedAt = p.FinishedAt
				session.Score = pgtype.Int8{Int64: winner, Valid: true}
				return store.GameSession{}, pgx.ErrNoRows
			},
		}, DefaultPolicy())
		service.now = func() time.Time { return session.StartedAt.Add(10 * time.Second) }
		_, err := service.Finish(context.Background(), session.ID.String(), "test-token", 500)
		if winner == 500 && err != nil || winner != 500 && !errors.Is(err, ErrAlreadyFinished) {
			t.Fatalf("winning score %d: error = %v", winner, err)
		}
	}
}

func TestSessionLookupErrors(t *testing.T) {
	service, _ := NewService(stubStore{get: func() (store.GameSession, error) { return store.GameSession{}, pgx.ErrNoRows }}, DefaultPolicy())
	if _, err := service.Finish(context.Background(), "invalid", "token", 0); !errors.Is(err, ErrInvalidSessionID) {
		t.Fatal(err)
	}
	if _, err := service.Finish(context.Background(), fixture().ID.String(), "token", 0); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestLeaderboardLimitsAndEmptyResponse(t *testing.T) {
	service, _ := NewService(stubStore{list: func(limit int32) ([]store.ListLeaderboardRow, error) {
		if limit != 20 {
			t.Errorf("query limit = %d, want 20", limit)
		}
		return nil, nil
	}}, DefaultPolicy())
	for _, limit := range []int32{0, -1, 101} {
		if _, err := service.Leaderboard(context.Background(), limit); !errors.Is(err, ErrInvalidLimit) {
			t.Fatal(err)
		}
	}
	entries, err := service.Leaderboard(context.Background(), 20)
	if err != nil || entries == nil || len(entries) != 0 {
		t.Fatalf("entries = %v, error = %v; want a non-nil empty slice", entries, err)
	}
}

func TestPolicyFromEnv(t *testing.T) {
	for _, name := range []string{"SESSION_MIN_DURATION", "SESSION_MAX_DURATION", "MAX_SCORE_PER_SECOND", "SESSION_MAX_CHECKPOINT_GAP"} {
		t.Setenv(name, "")
	}
	if policy, err := PolicyFromEnv(); err != nil || policy != DefaultPolicy() {
		t.Fatalf("defaults = %+v, error = %v", policy, err)
	}
	t.Setenv("SESSION_MIN_DURATION", "2s")
	t.Setenv("SESSION_MAX_DURATION", "30m")
	t.Setenv("MAX_SCORE_PER_SECOND", "250")
	t.Setenv("SESSION_MAX_CHECKPOINT_GAP", "15s")
	if policy, err := PolicyFromEnv(); err != nil || policy.MinDuration != 2*time.Second || policy.MaxDuration != 30*time.Minute || policy.MaxScorePerSecond != 250 || policy.MaxCheckpointGap != 15*time.Second {
		t.Fatalf("configuration = %+v, error = %v", policy, err)
	}
	for _, value := range []string{"invalid", "0s", "-1s"} {
		t.Setenv("SESSION_MAX_CHECKPOINT_GAP", value)
		if _, err := PolicyFromEnv(); err == nil {
			t.Errorf("checkpoint gap %q must fail", value)
		}
	}
	t.Setenv("SESSION_MAX_CHECKPOINT_GAP", "15s")
	for _, value := range []string{"not-a-number", "0", "-1"} {
		t.Setenv("MAX_SCORE_PER_SECOND", value)
		if _, err := PolicyFromEnv(); err == nil {
			t.Errorf("score rate %q must fail", value)
		}
	}
	t.Setenv("MAX_SCORE_PER_SECOND", "1000")
	for _, value := range []string{"invalid", "0s", "1s"} {
		t.Setenv("SESSION_MAX_DURATION", value)
		if _, err := PolicyFromEnv(); err == nil {
			t.Errorf("maximum duration %q must fail", value)
		}
	}
}

// runStore keeps one session in memory so a run can progress through checkpoints.
func runStore(session *store.GameSession, deleted *bool) stubStore {
	return stubStore{
		get: func() (store.GameSession, error) {
			if *deleted {
				return store.GameSession{}, pgx.ErrNoRows
			}
			return *session, nil
		},
		checkpoint: func(p store.RecordCheckpointParams) (int64, error) {
			if *deleted || session.FinishedAt.Valid || !session.CheckpointAt.Before(p.CheckpointAt) {
				return 0, nil
			}
			session.CheckpointScore, session.CheckpointAt = p.CheckpointScore, p.CheckpointAt
			return 1, nil
		},
		delete: func(store.DeleteUnfinishedSessionParams) (int64, error) {
			if *deleted || session.FinishedAt.Valid {
				return 0, nil
			}
			*deleted = true
			return 1, nil
		},
		finish: func(p store.FinishSessionParams) (store.GameSession, error) {
			session.Score, session.FinishedAt = p.Score, p.FinishedAt
			return *session, nil
		},
	}
}

func TestIdleRunCannotClaimScore(t *testing.T) {
	// Regression: a client could start, idle, and finish just before expiry with the maximum allowed score.
	session, deleted := fixture(), false
	service, _ := NewService(runStore(&session, &deleted), DefaultPolicy())
	service.now = func() time.Time { return session.StartedAt.Add(59 * time.Minute) }
	_, err := service.Finish(context.Background(), session.ID.String(), "test-token", 59*60*1000)
	if !errors.Is(err, ErrRunRejected) || !errors.Is(err, errMissedCheckpoint) || !deleted {
		t.Fatalf("an idle run must be rejected and deleted: error=%v deleted=%t", err, deleted)
	}
}

func TestCheckpointedRun(t *testing.T) {
	session, deleted := fixture(), false
	service, _ := NewService(runStore(&session, &deleted), DefaultPolicy())
	now := session.StartedAt
	service.now = func() time.Time { return now }
	for i := int64(1); i <= 10; i++ {
		now = now.Add(10 * time.Second)
		if err := service.Checkpoint(context.Background(), session.ID.String(), "test-token", i*10000); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	now = now.Add(5 * time.Second)
	result, err := service.Finish(context.Background(), session.ID.String(), "test-token", 105000)
	if err != nil || result.Score != 105000 || result.DurationMS != 105000 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestCheckpointValidation(t *testing.T) {
	tests := []struct {
		name  string
		since time.Duration
		score int64
		token string
		want  error
	}{
		{"progress", 10 * time.Second, 15000, "test-token", nil},
		{"unchanged score", 10 * time.Second, 5000, "test-token", nil},
		{"at gap and rate bound", 30 * time.Second, 35000, "test-token", nil},
		{"decreasing score", 10 * time.Second, 4999, "test-token", errInvalidScore},
		{"negative score", 10 * time.Second, -1, "test-token", errInvalidScore},
		{"above rate bound", 10 * time.Second, 15001, "test-token", errInvalidScore},
		{"gap exceeded", 30*time.Second + time.Microsecond, 5000, "test-token", errMissedCheckpoint},
		{"expired", time.Hour, 5000, "test-token", errExpired},
		{"wrong token", 10 * time.Second, 15000, "wrong", ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, deleted := fixture(), false
			session.CheckpointAt, session.CheckpointScore = session.StartedAt.Add(10*time.Second), 5000
			previous := session
			service, _ := NewService(runStore(&session, &deleted), DefaultPolicy())
			now := session.CheckpointAt.Add(tt.since)
			service.now = func() time.Time { return now }
			err := service.Checkpoint(context.Background(), session.ID.String(), tt.token, tt.score)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			wantDeletion := tt.want != nil && tt.want != ErrUnauthorized
			if deleted != wantDeletion || wantDeletion && !errors.Is(err, ErrRunRejected) {
				t.Fatalf("deleted=%t error=%v, want deletion %t with ErrRunRejected", deleted, err, wantDeletion)
			}
			if tt.want == nil && (session.CheckpointScore != tt.score || session.CheckpointAt != now) {
				t.Fatalf("checkpoint not recorded: %+v", session)
			}
			if tt.want != nil && (session.CheckpointScore != previous.CheckpointScore || session.CheckpointAt != previous.CheckpointAt) {
				t.Fatalf("an invalid checkpoint must not be recorded: %+v", session)
			}
		})
	}
}

func TestCheckpointAfterFinish(t *testing.T) {
	session, deleted := fixture(), false
	session.Score = pgtype.Int8{Int64: 100, Valid: true}
	session.FinishedAt = pgtype.Timestamptz{Time: session.StartedAt.Add(5 * time.Second), Valid: true}
	db := runStore(&session, &deleted)
	db.checkpoint = func(store.RecordCheckpointParams) (int64, error) {
		t.Fatal("a finished run must not record checkpoints")
		return 0, nil
	}
	service, _ := NewService(db, DefaultPolicy())
	service.now = func() time.Time { return session.StartedAt.Add(10 * time.Second) }
	if err := service.Checkpoint(context.Background(), session.ID.String(), "test-token", 100); !errors.Is(err, ErrSessionFinished) {
		t.Fatalf("error = %v", err)
	}
}

func TestCheckpointLostRace(t *testing.T) {
	for _, finished := range []bool{false, true} {
		session, deleted := fixture(), false
		db := runStore(&session, &deleted)
		db.checkpoint = func(store.RecordCheckpointParams) (int64, error) {
			if finished {
				session.Score = pgtype.Int8{Int64: 0, Valid: true}
				session.FinishedAt = pgtype.Timestamptz{Time: session.StartedAt.Add(time.Second), Valid: true}
			}
			return 0, nil
		}
		service, _ := NewService(db, DefaultPolicy())
		service.now = func() time.Time { return session.StartedAt.Add(2 * time.Second) }
		err := service.Checkpoint(context.Background(), session.ID.String(), "test-token", 1000)
		if finished && !errors.Is(err, ErrSessionFinished) || !finished && err != nil {
			t.Fatalf("finished=%t: error = %v", finished, err)
		}
	}
}

func TestFinishFromLastCheckpoint(t *testing.T) {
	for _, tt := range []struct {
		score int64
		want  error
	}{{35000, nil}, {35001, errInvalidScore}, {19999, errInvalidScore}} {
		session, deleted := fixture(), false
		session.CheckpointAt, session.CheckpointScore = session.StartedAt.Add(25*time.Second), 20000
		service, _ := NewService(runStore(&session, &deleted), DefaultPolicy())
		service.now = func() time.Time { return session.StartedAt.Add(40 * time.Second) }
		if _, err := service.Finish(context.Background(), session.ID.String(), "test-token", tt.score); !errors.Is(err, tt.want) {
			t.Errorf("score %d: error = %v, want %v", tt.score, err, tt.want)
		}
	}
}
