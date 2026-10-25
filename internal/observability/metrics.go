package observability

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
)

type Metrics struct {
	registry       *prometheus.Registry
	requests       *prometheus.CounterVec
	durations      *prometheus.HistogramVec
	ready          prometheus.Gauge
	deleted        prometheus.Counter
	cleanupSuccess prometheus.Gauge
	cleanupErrors  prometheus.Counter
}

func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "spacegame_http_requests_total", Help: "HTTP requests by method, route template, and status.",
		}, []string{"method", "route", "status"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "spacegame_http_request_duration_seconds", Help: "HTTP request duration by route template.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
		ready: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "spacegame_ready", Help: "One when the most recent database and schema readiness check passed.",
		}),
		deleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "spacegame_expired_sessions_deleted_total", Help: "Expired unfinished sessions removed.",
		}),
		cleanupSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "spacegame_cleanup_last_success_timestamp_seconds", Help: "Unix time of the last successful cleanup sweep.",
		}),
		cleanupErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "spacegame_cleanup_errors_total", Help: "Failed cleanup sweeps.",
		}),
	}
	m.registry.MustRegister(m.requests, m.durations, m.ready, m.deleted, m.cleanupSuccess, m.cleanupErrors,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return mux
}

func (m *Metrics) SetReady(ready bool) {
	if ready {
		m.ready.Set(1)
	} else {
		m.ready.Set(0)
	}
}

func (m *Metrics) ObserveCleanup(deleted int64, err error) {
	m.deleted.Add(float64(deleted))
	if err != nil {
		m.cleanupErrors.Inc()
	} else {
		m.cleanupSuccess.SetToCurrentTime()
	}
}

func (m *Metrics) Middleware(logger *slog.Logger) gin.HandlerFunc {
	noisyLogs := rate.NewLimiter(1, 5)
	return func(c *gin.Context) {
		started := time.Now()
		requestID := rand.Text()
		c.Header("X-Request-ID", requestID)
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		method := c.Request.Method
		switch method {
		case "GET", "HEAD", "POST", "OPTIONS", "PUT", "PATCH", "DELETE":
		default:
			method = "OTHER"
		}
		status := c.Writer.Status()
		duration := time.Since(started)
		m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
		m.durations.WithLabelValues(method, route).Observe(duration.Seconds())
		if (route == "/healthz" || route == "/readyz") && status < 400 {
			return
		}
		if (status == 429 || route == "unmatched") && !noisyLogs.Allow() {
			return
		}
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		logger.Log(c.Request.Context(), level, "http request",
			"request_id", requestID, "method", method, "route", route,
			"status", status, "duration_ms", float64(duration.Microseconds())/1000)
	}
}
