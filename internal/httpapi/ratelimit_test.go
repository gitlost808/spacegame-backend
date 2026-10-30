package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func smallLimiter(t *testing.T) (*RateLimiter, *time.Time) {
	t.Helper()
	c := DefaultRateConfig()
	c.Start, c.Finish, c.Leaderboard = RateRule{60, 2}, RateRule{60, 2}, RateRule{60, 2}
	c.GlobalPerSecond, c.GlobalBurst = 1000, 1000
	limiter, err := NewRateLimiter(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	limiter.now = func() time.Time { return now }
	return limiter, &now
}

func TestRateLimitRefillAndScope(t *testing.T) {
	l, now := smallLimiter(t)
	if l.allow("a", "start") != 0 || l.allow("a", "start") != 0 || l.allow("a", "start") <= 0 {
		t.Fatal("start burst must allow exactly two requests")
	}
	if l.allow("b", "start") != 0 || l.allow("a", "finish") != 0 {
		t.Fatal("client and route buckets must be independent")
	}
	*now = now.Add(time.Second)
	if l.allow("a", "start") != 0 || l.allow("a", "start") <= 0 {
		t.Fatal("one second must replenish one token")
	}
}

func TestGlobalLimitAndBoundedClients(t *testing.T) {
	l, now := smallLimiter(t)
	l.config.MaxClients = 2
	if l.allow("a", "start") != 0 || l.allow("b", "start") != 0 || l.allow("c", "start") <= 0 || len(l.clients) != 2 {
		t.Fatal("client capacity must fail closed without evicting active clients")
	}
	*now = now.Add(l.ttl + time.Minute)
	if l.allow("c", "start") != 0 || len(l.clients) != 1 {
		t.Fatal("inactive client state must be pruned")
	}
	c := DefaultRateConfig()
	c.GlobalBurst, c.GlobalPerSecond = 1, 1
	g, _ := NewRateLimiter(c)
	if g.allow("a", "start") != 0 || g.allow("b", "leaderboard") <= 0 {
		t.Fatal("global budget must apply across clients and routes")
	}
}

func TestConcurrentRateLimits(t *testing.T) {
	l, _ := smallLimiter(t)
	var accepted atomic.Int32
	var wait sync.WaitGroup
	for range 50 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if l.allow("same-client", "start") == 0 {
				accepted.Add(1)
			}
		}()
	}
	wait.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("accepted %d requests, want 2", accepted.Load())
	}
}

func TestIPv6ClientGrouping(t *testing.T) {
	if clientKey("2001:db8:abcd:1::1") != clientKey("2001:db8:abcd:1::ffff") {
		t.Fatal("addresses in one IPv6 /64 must share a bucket")
	}
	if clientKey("::ffff:192.0.2.1") != clientKey("192.0.2.1") {
		t.Fatal("mapped IPv4 must use the same key as IPv4")
	}
}

func TestForwardedHeadersRequireTrustedProxy(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		limiter, _ := smallLimiter(t)
		router := gin.New()
		proxies := []string(nil)
		if trusted {
			proxies = []string{"192.0.2.10"}
		}
		if err := router.SetTrustedProxies(proxies); err != nil {
			t.Fatal(err)
		}
		router.POST("/start", limiter.For("start"), func(c *gin.Context) { c.Status(201) })
		for i, forwarded := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
			r := httptest.NewRequest(http.MethodPost, "/start", nil)
			r.RemoteAddr = "192.0.2.10:1234"
			r.Header.Set("X-Forwarded-For", forwarded)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			want := 201
			if !trusted && i == 2 {
				want = 429
				if w.Header().Get("Retry-After") == "" {
					t.Fatal("rate-limited responses must include Retry-After")
				}
			}
			if w.Code != want {
				t.Fatalf("trusted=%t request=%d: status=%d want=%d", trusted, i, w.Code, want)
			}
		}
	}
}

type readyStub struct{}

func (readyStub) CheckReady(context.Context) error { return nil }

func TestInvalidProxyConfigFails(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if _, err := NewHandler(logger, readyStub{}, Config{TrustedProxies: []string{"not-an-ip"}}); err == nil {
		t.Fatal("invalid trusted proxies must fail configuration")
	}
}

func TestRateConfigFromEnv(t *testing.T) {
	values := map[string]string{
		"RATE_LIMIT_START_PER_MINUTE": "61", "RATE_LIMIT_START_BURST": "11",
		"RATE_LIMIT_FINISH_PER_MINUTE": "121", "RATE_LIMIT_FINISH_BURST": "21",
		"RATE_LIMIT_LEADERBOARD_PER_MINUTE": "301", "RATE_LIMIT_LEADERBOARD_BURST": "31",
		"RATE_LIMIT_GLOBAL_PER_SECOND": "51", "RATE_LIMIT_GLOBAL_BURST": "101",
		"RATE_LIMIT_MAX_CLIENTS": "10001",
	}
	for name := range values {
		t.Setenv(name, "")
	}
	if got, err := RateConfigFromEnv(); err != nil || got != DefaultRateConfig() {
		t.Fatalf("defaults=%+v error=%v", got, err)
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	want := RateConfig{
		Start: RateRule{61, 11}, Finish: RateRule{121, 21}, Leaderboard: RateRule{301, 31},
		GlobalPerSecond: 51, GlobalBurst: 101, MaxClients: 10001,
	}
	if got, err := RateConfigFromEnv(); err != nil || got != want {
		t.Fatalf("overrides=%+v error=%v", got, err)
	}
	for _, tt := range []struct{ name, value string }{
		{"RATE_LIMIT_START_PER_MINUTE", "not-an-integer"},
		{"RATE_LIMIT_FINISH_BURST", "0"},
		{"RATE_LIMIT_LEADERBOARD_PER_MINUTE", "1000001"},
		{"RATE_LIMIT_GLOBAL_PER_SECOND", "-1"},
		{"RATE_LIMIT_GLOBAL_BURST", "1000001"},
		{"RATE_LIMIT_MAX_CLIENTS", "0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.name, tt.value)
			if _, err := RateConfigFromEnv(); err == nil {
				t.Fatalf("%s=%s must fail", tt.name, tt.value)
			}
		})
	}
}
