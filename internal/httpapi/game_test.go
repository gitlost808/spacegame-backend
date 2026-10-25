package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
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
	start  func(string) (game.StartedSession, error)
	finish func(string, string, int64) (game.Result, error)
	list   func(int32) ([]game.Entry, error)
}

func (s stubGame) Start(_ context.Context, name string) (game.StartedSession, error) {
	return s.start(name)
}
func (s stubGame) Finish(_ context.Context, id, token string, score int64) (game.Result, error) {
	return s.finish(id, token, score)
}
func (s stubGame) Leaderboard(_ context.Context, limit int32) ([]game.Entry, error) {
	return s.list(limit)
}

func gameRouter(service httpapi.GameService, origins ...string) *gin.Engine {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return httpapi.NewHandler(logger, pingFunc(func(context.Context) error { return nil }), httpapi.Config{
		Game: service, AllowedOrigins: origins,
	})
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

func TestGameErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{game.ErrInvalidName, 400}, {game.ErrInvalidSessionID, 400},
		{game.ErrUnauthorized, 401}, {game.ErrNotFound, 404},
		{game.ErrAlreadyFinished, 409}, {game.ErrExpired, 410},
		{game.ErrTooShort, 422}, {game.ErrInvalidScore, 422},
		{context.DeadlineExceeded, 503}, {errors.New("private database details"), 500},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			handler := gameRouter(stubGame{finish: func(string, string, int64) (game.Result, error) {
				return game.Result{}, tt.err
			}})
			w := request(handler, http.MethodPost, "/api/v1/sessions/id/finish", `{"score":0}`, "token")
			if w.Code != tt.status {
				t.Fatalf("response = %d %s, want %d", w.Code, w.Body, tt.status)
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tt.status >= 500 && body["error"] != "service unavailable" {
				t.Fatal("server errors must not leak database details")
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
		if w.Code != http.StatusNoContent {
			t.Fatalf("preflight status = %d", w.Code)
		}
		want := ""
		if origin == "https://game.example" {
			want = origin
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("origin %s: allowed origin = %q, want %q", origin, got, want)
		}
	}
}
