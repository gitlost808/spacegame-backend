package observability_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"spacegame-backend/internal/observability"
)

func TestRequestMetricsAndRedactedLogs(t *testing.T) {
	var logs bytes.Buffer
	metrics := observability.NewMetrics()
	router := gin.New()
	router.Use(metrics.Middleware(slog.New(slog.NewJSONHandler(&logs, nil))))
	router.POST("/sessions/:id/finish", func(c *gin.Context) { c.Status(409) })
	r := httptest.NewRequest("POST", "/sessions/private-session-id/finish?secret=private-query", strings.NewReader("private-body"))
	r.Header.Set("Authorization", "Bearer private-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("response must include a request ID")
	}
	for _, secret := range []string{"private-token", "private-body", "private-query", "private-session-id"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("request log leaked %s", secret)
		}
	}
	metrics.SetReady(true)
	metrics.ObserveCleanup(3, nil)
	scrape := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expected := range []string{
		`spacegame_http_requests_total{method="POST",route="/sessions/:id/finish",status="409"} 1`,
		"spacegame_ready 1", "spacegame_expired_sessions_deleted_total 3",
		"spacegame_http_request_duration_seconds_count", "go_goroutines",
	} {
		if !strings.Contains(scrape.Body.String(), expected) {
			t.Errorf("metrics missing %q", expected)
		}
	}
}
