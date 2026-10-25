package httpapi

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type RateRule struct {
	PerMinute int
	Burst     int
}

type RateConfig struct {
	Start, Finish, Leaderboard RateRule
	GlobalPerSecond            int
	GlobalBurst                int
	MaxClients                 int
}

func DefaultRateConfig() RateConfig {
	return RateConfig{
		Start: RateRule{30, 10}, Finish: RateRule{120, 20}, Leaderboard: RateRule{300, 30},
		GlobalPerSecond: 50, GlobalBurst: 100, MaxClients: 10000,
	}
}

func RateConfigFromEnv() (RateConfig, error) {
	c := DefaultRateConfig()
	for _, item := range []struct {
		name string
		dst  *int
	}{
		{"RATE_LIMIT_START_PER_MINUTE", &c.Start.PerMinute}, {"RATE_LIMIT_START_BURST", &c.Start.Burst},
		{"RATE_LIMIT_FINISH_PER_MINUTE", &c.Finish.PerMinute}, {"RATE_LIMIT_FINISH_BURST", &c.Finish.Burst},
		{"RATE_LIMIT_LEADERBOARD_PER_MINUTE", &c.Leaderboard.PerMinute}, {"RATE_LIMIT_LEADERBOARD_BURST", &c.Leaderboard.Burst},
		{"RATE_LIMIT_GLOBAL_PER_SECOND", &c.GlobalPerSecond}, {"RATE_LIMIT_GLOBAL_BURST", &c.GlobalBurst},
		{"RATE_LIMIT_MAX_CLIENTS", &c.MaxClients},
	} {
		if value := os.Getenv(item.name); value != "" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 1 || number > 1000000 {
				return c, fmt.Errorf("%s must be an integer between 1 and 1000000", item.name)
			}
			*item.dst = number
		}
	}
	return c, nil
}

type clientLimits struct {
	buckets  map[string]*rate.Limiter
	lastSeen time.Time
}

// RateLimiter uses bounded per-client state and one global token bucket.
// Limits are local to one application instance.
type RateLimiter struct {
	mu        sync.Mutex
	config    RateConfig
	clients   map[string]*clientLimits
	global    *rate.Limiter
	ttl       time.Duration
	nextPrune time.Time
	now       func() time.Time
}

func NewRateLimiter(config RateConfig) (*RateLimiter, error) {
	if config.GlobalPerSecond < 1 || config.GlobalBurst < 1 || config.MaxClients < 1 {
		return nil, errors.New("global request limits and client capacity must be positive")
	}
	ttl := 10 * time.Minute
	for _, rule := range []RateRule{config.Start, config.Finish, config.Leaderboard} {
		if rule.PerMinute < 1 || rule.Burst < 1 || rule.PerMinute > 1000000 || rule.Burst > 1000000 {
			return nil, errors.New("request rates and bursts must be between 1 and 1000000")
		}
		// Eviction must not refill a bucket sooner than normal token accrual.
		ttl = max(ttl, time.Duration(float64(rule.Burst)/float64(rule.PerMinute)*float64(time.Minute)))
	}
	return &RateLimiter{
		config: config, clients: make(map[string]*clientLimits),
		global: rate.NewLimiter(rate.Limit(config.GlobalPerSecond), config.GlobalBurst),
		ttl:    ttl, now: time.Now,
	}, nil
}

func (l *RateLimiter) For(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if retry := l.allow(clientKey(c.ClientIP()), action); retry > 0 {
			c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

func clientKey(value string) string {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return "unknown"
	}
	address = address.Unmap()
	if address.Is6() {
		return netip.PrefixFrom(address, 64).Masked().String()
	}
	return address.String()
}

func (l *RateLimiter) allow(key, action string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !now.Before(l.nextPrune) {
		for key, client := range l.clients {
			if now.Sub(client.lastSeen) >= l.ttl {
				delete(l.clients, key)
			}
		}
		l.nextPrune = now.Add(time.Minute)
	}
	if retry := waitFor(l.global, now); retry > 0 {
		return retry
	}
	client := l.clients[key]
	if client == nil {
		if len(l.clients) >= l.config.MaxClients {
			return time.Minute
		}
		client = &clientLimits{buckets: make(map[string]*rate.Limiter)}
		for name, rule := range map[string]RateRule{"start": l.config.Start, "finish": l.config.Finish, "leaderboard": l.config.Leaderboard} {
			client.buckets[name] = rate.NewLimiter(rate.Limit(rule.PerMinute)/60, rule.Burst)
		}
		l.clients[key] = client
	}
	client.lastSeen = now
	bucket := client.buckets[action]
	if bucket == nil {
		return time.Minute
	}
	if retry := waitFor(bucket, now); retry > 0 {
		return retry
	}
	l.global.AllowN(now, 1)
	bucket.AllowN(now, 1)
	return 0
}

func waitFor(bucket *rate.Limiter, now time.Time) time.Duration {
	if tokens := bucket.TokensAt(now); tokens < 1 {
		return time.Duration(math.Ceil((1 - tokens) / float64(bucket.Limit()) * float64(time.Second)))
	}
	return 0
}
