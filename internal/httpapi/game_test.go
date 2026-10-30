package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"spacegame-backend/internal/game"
	"spacegame-backend/internal/httpapi"
)

type stubGame struct {
	start      func(string) (game.StartedSession, error)
	checkpoint func(string, string, int64) error
	finish     func(string, string, int64) (game.Result, error)
	list       func(int32) ([]game.Entry, error)
}

func (s stubGame) Start(_ context.Context, name string) (game.StartedSession, error) {
	return s.start(name)
}
func (s stubGame) Checkpoint(_ context.Context, id, token string, score int64) error {
	return s.checkpoint(id, token, score)
}
func (s stubGame) Finish(_ context.Context, id, token string, score int64) (game.Result, error) {
	return s.finish(id, token, score)
}
func (s stubGame) Leaderboard(_ context.Context, limit int32) ([]game.Entry, error) {
	return s.list(limit)
}

func gameRouter(service httpapi.GameService, origins ...string) *gin.Engine {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	router, err := httpapi.NewHandler(logger, pingFunc(func(context.Context) error { return nil }), httpapi.Config{
		Game: service, AllowedOrigins: origins,
	})
	if err != nil {
		panic(err)
	}
	return router
}

func request(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestGameEndpoints(t *testing.T) {
	handler := gameRouter(stubGame{
		start: func(name string) (game.StartedSession, error) {
			if name != "Pilot" {
				t.Errorf("name = %q", name)
			}
			return game.StartedSession{SessionID: "session-id", SessionToken: "secret-token"}, nil
		},
		checkpoint: func(id, token string, score int64) error {
			if id != "session-id" || token != "secret-token" || score != 500 {
				t.Errorf("checkpoint arguments = %q, %q, %d", id, token, score)
			}
			return nil
		},
		finish: func(id, token string, score int64) (game.Result, error) {
			if id != "session-id" || token != "secret-token" || score != 0 {
				t.Errorf("finish arguments = %q, %q, %d", id, token, score)
			}
			return game.Result{SessionID: id, Score: score}, nil
		},
		list: func(limit int32) ([]game.Entry, error) {
			if limit != 20 {
				t.Errorf("default limit = %d", limit)
			}
			return []game.Entry{}, nil
		},
	})
	start := request(handler, http.MethodPost, "/api/v1/sessions", `{"player_name":"Pilot"}`, "")
	if start.Code != http.StatusCreated || !strings.Contains(start.Body.String(), `"session_token":"secret-token"`) {
		t.Fatalf("start: %d %s", start.Code, start.Body)
	}
	if strings.Contains(start.Body.String(), "expires_at") {
		t.Fatalf("start must not reveal the session lifetime: %s", start.Body)
	}
	checkpoint := request(handler, http.MethodPost, "/api/v1/sessions/session-id/checkpoint", `{"score":500}`, "secret-token")
	if checkpoint.Code != http.StatusNoContent || checkpoint.Body.Len() != 0 {
		t.Fatalf("checkpoint: %d %s", checkpoint.Code, checkpoint.Body)
	}
	finish := request(handler, http.MethodPost, "/api/v1/sessions/session-id/finish", `{"score":0}`, "secret-token")
	if finish.Code != http.StatusOK || strings.Contains(finish.Body.String(), "token") {
		t.Fatalf("finish: %d %s", finish.Code, finish.Body)
	}
	leaderboard := request(handler, http.MethodGet, "/api/v1/leaderboard", "", "")
	if leaderboard.Code != http.StatusOK || leaderboard.Body.String() != `{"entries":[]}` {
		t.Fatalf("leaderboard: %d %s", leaderboard.Code, leaderboard.Body)
	}
	if start.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session tokens must not be cached")
	}
}

func TestGameRequestValidation(t *testing.T) {
	handler := gameRouter(stubGame{})
	tests := []struct {
		name, method, path, body, token, contentType string
		status                                       int
	}{
		{"malformed JSON", "POST", "/api/v1/sessions", "{", "", "application/json", 400},
		{"trailing JSON", "POST", "/api/v1/sessions", `{"player_name":"Pilot"}{}`, "", "application/json", 400},
		{"large body", "POST", "/api/v1/sessions", strings.Repeat("x", 4097), "", "application/json", 413},
		{"wrong content type", "POST", "/api/v1/sessions", `{}`, "", "text/plain", 415},
		{"invalid JSON media type", "POST", "/api/v1/sessions", `{}`, "", "application/json-extra", 415},
		{"missing authorization", "POST", "/api/v1/sessions/id/finish", `{"score":0}`, "", "application/json", 401},
		{"missing score", "POST", "/api/v1/sessions/id/finish", `{}`, "token", "application/json", 400},
		{"null score", "POST", "/api/v1/sessions/id/finish", `{"score":null}`, "token", "application/json", 400},
		{"fractional score", "POST", "/api/v1/sessions/id/finish", `{"score":1.5}`, "token", "application/json", 400},
		{"overflow score", "POST", "/api/v1/sessions/id/finish", `{"score":9223372036854775808}`, "token", "application/json", 400},
		{"checkpoint missing authorization", "POST", "/api/v1/sessions/id/checkpoint", `{"score":0}`, "", "application/json", 401},
		{"checkpoint missing score", "POST", "/api/v1/sessions/id/checkpoint", `{}`, "token", "application/json", 400},
		{"checkpoint wrong content type", "POST", "/api/v1/sessions/id/checkpoint", `{"score":0}`, "token", "text/plain", 415},
		{"zero limit", "GET", "/api/v1/leaderboard?limit=0", "", "", "", 400},
		{"large limit", "GET", "/api/v1/leaderboard?limit=101", "", "", "", 400},
		{"invalid limit", "GET", "/api/v1/leaderboard?limit=abc", "", "", "", 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			if tt.token != "" {
				r.Header.Set("Authorization", "Bearer "+tt.token)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("response = %d %s, want %d", w.Code, w.Body, tt.status)
			}
		})
	}
}

func TestDisallowedPlayerNameResponse(t *testing.T) {
	// Rejected names must return before any database access through this nil store.
	service, err := game.NewService(nil, game.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	handler := gameRouter(service)
	for _, name := range []string{"xxFuckxx", "Scheiße", "sch3i$$3", "ADOLF HILTER 88 HEIL", "Adolf 88 Hilter", "Sieg Heil", "14/88"} {
		body, err := json.Marshal(map[string]string{"player_name": name})
		if err != nil {
			t.Fatal(err)
		}
		w := request(handler, http.MethodPost, "/api/v1/sessions", string(body), "")
		if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"please choose another player name"}` {
			t.Fatalf("name %q: response=%d %s", name, w.Code, w.Body)
		}
	}
}

func TestGameErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{game.ErrInvalidName, 400}, {game.ErrInvalidSessionID, 400},
		{game.ErrUnauthorized, 401}, {game.ErrNotFound, 404},
		{game.ErrAlreadyFinished, 409}, {game.ErrSessionFinished, 409},
		{fmt.Errorf("%w: score progress is not plausible", game.ErrRunRejected), 422},
		{fmt.Errorf("%w: checkpoint gap exceeded", game.ErrRunRejected), 422},
		{context.DeadlineExceeded, 503}, {errors.New("private database details"), 500},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			handler := gameRouter(stubGame{
				checkpoint: func(string, string, int64) error { return tt.err },
				finish:     func(string, string, int64) (game.Result, error) { return game.Result{}, tt.err },
			})
			for _, path := range []string{"/api/v1/sessions/id/checkpoint", "/api/v1/sessions/id/finish"} {
				w := request(handler, http.MethodPost, path, `{"score":0}`, "token")
				if w.Code != tt.status {
					t.Fatalf("%s: response = %d %s, want %d", path, w.Code, w.Body, tt.status)
				}
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if tt.status >= 500 && body["error"] != "service unavailable" {
					t.Fatal("server errors must not leak database details")
				}
				if tt.status == http.StatusUnprocessableEntity && body["error"] != "run rejected" {
					t.Fatalf("rejections must not reveal which limit failed: %s", w.Body)
				}
			}
		})
	}
}

func TestBrowserPreflight(t *testing.T) {
	handler := gameRouter(stubGame{}, "https://game.example")
	for _, origin := range []string{"https://game.example", "https://unlisted.example"} {
		r := httptest.NewRequest(http.MethodOptions, "/api/v1/sessions/id/finish", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := ""
		wantStatus := http.StatusForbidden
		if origin == "https://game.example" {
			want = origin
			wantStatus = http.StatusNoContent
			if w.Header().Get("Access-Control-Max-Age") != "600" || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Content-Type") {
				t.Fatalf("preflight is missing authorization, JSON, or cache settings: %v", w.Header())
			}
		}
		if w.Code != wantStatus {
			t.Fatalf("origin %s: preflight status = %d, want %d", origin, w.Code, wantStatus)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("origin %s: allowed origin = %q, want %q", origin, got, want)
		}
	}
}

func TestCORSRequests(t *testing.T) {
	for _, tt := range []struct {
		name, origin, allowed string
		origins               []string
		status                int
	}{
		{"no origin", "", "", nil, 200},
		{"same origin", "http://example.com", "", nil, 200},
		{"unconfigured cross origin", "https://other.example", "", nil, 403},
		{"allowed origin", "https://game.example", "https://game.example", []string{" https://game.example ", ""}, 200},
		{"blocked origin", "https://other.example", "", []string{"https://game.example"}, 403},
		{"wildcard", "https://other.example", "*", []string{"*"}, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := gameRouter(stubGame{list: func(int32) ([]game.Entry, error) {
				called = true
				return []game.Entry{}, nil
			}}, tt.origins...)
			r := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard", nil)
			r.Header.Set("Origin", tt.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tt.status || w.Header().Get("Access-Control-Allow-Origin") != tt.allowed {
				t.Fatalf("status=%d headers=%v", w.Code, w.Header())
			}
			if called != (tt.status == http.StatusOK) {
				t.Fatal("disallowed browser origins must not reach the game service")
			}
			if w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("CORS must not enable cookie credentials")
			}
		})
	}
}

func TestInvalidCORSConfiguration(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	_, err := httpapi.NewHandler(logger, pingFunc(func(context.Context) error { return nil }), httpapi.Config{
		AllowedOrigins: []string{"not-an-origin"},
	})
	if err == nil {
		t.Fatal("invalid CORS settings must fail configuration")
	}
}
