package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"spacegame-backend/internal/httpapi"
)

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	newHandler(pingFunc(func(context.Context) error {
		t.Fatal("liveness must not query the database")
		return nil
	})).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got, _, err := mime.ParseMediaType(response.Header().Get("Content-Type")); err != nil || got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

func TestRouting(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"head health", http.MethodHead, "/healthz", http.StatusOK},
		{"unknown route", http.MethodGet, "/missing", http.StatusNotFound},
		{"unsupported method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, nil)
			response := httptest.NewRecorder()
			newHandler(pingFunc(func(context.Context) error { return nil })).ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Errorf("status = %d, want %d", response.Code, tt.status)
			}
		})
	}
}

func TestReadiness(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		code   int
		status string
	}{
		{"database available", nil, http.StatusOK, "ok"},
		{"database unavailable", errors.New("private database details"), http.StatusServiceUnavailable, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := newHandler(pingFunc(func(ctx context.Context) error {
				called = true
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Error("database check must have a deadline of at most two seconds")
				}
				return tt.err
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if !called {
				t.Error("readiness must query the database")
			}
			if response.Code != tt.code {
				t.Errorf("status = %d, want %d", response.Code, tt.code)
			}
			if got, want := response.Body.String(), `{"status":"`+tt.status+`"}`; got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
		})
	}
}

func TestReadinessUsesRequestContext(t *testing.T) {
	handler := newHandler(pingFunc(func(ctx context.Context) error {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Error("database check must inherit request cancellation")
		}
		return ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestPanicRecovery(t *testing.T) {
	handler := newHandler(pingFunc(func(context.Context) error { return nil }))
	handler.GET("/panic", func(*gin.Context) { panic("private panic details") })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got, want := response.Body.String(), `{"error":"internal server error"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

type pingFunc func(context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func newHandler(db httpapi.Pinger) *gin.Engine {
	return httpapi.NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), db)
}
