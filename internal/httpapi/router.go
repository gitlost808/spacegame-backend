package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// ReadinessChecker verifies database connectivity and the required schema.
type ReadinessChecker interface {
	CheckReady(context.Context) error
}

// NewHandler registers the HTTP API routes.
func NewHandler(logger *slog.Logger, db ReadinessChecker, config Config) (*gin.Engine, error) {
	router := gin.New()
	router.HandleMethodNotAllowed = true
	if err := router.SetTrustedProxies(config.TrustedProxies); err != nil {
		return nil, fmt.Errorf("invalid trusted proxy configuration: %w", err)
	}
	if config.Metrics != nil {
		router.Use(config.Metrics.Middleware(logger))
	}
	router.Use(gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger.Error("request panicked", "error", fmt.Sprint(recovered), "stack", string(debug.Stack()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}))
	corsConfig := cors.Config{
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:       10 * time.Minute,
	}
	for _, origin := range config.AllowedOrigins {
		if origin = strings.TrimSpace(origin); origin != "" {
			corsConfig.AllowOrigins = append(corsConfig.AllowOrigins, origin)
		}
	}
	if len(corsConfig.AllowOrigins) == 0 {
		// Same-origin requests pass through; no other browser origins are allowed.
		corsConfig.AllowOriginFunc = func(string) bool { return false }
	}
	if err := corsConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid CORS configuration: %w", err)
	}
	router.Use(cors.New(corsConfig))
	router.GET("/healthz", health)
	router.HEAD("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := db.CheckReady(ctx); err != nil {
			if config.Metrics != nil {
				config.Metrics.SetReady(false)
			}
			logger.Warn("database readiness check failed", "error", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		if config.Metrics != nil {
			config.Metrics.SetReady(true)
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api := router.Group("/api/v1")
	api.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	handler := gameHandler{service: config.Game, logger: logger}
	limit := func(action string) gin.HandlerFunc {
		if config.Limiter != nil {
			return config.Limiter.For(action)
		}
		return func(c *gin.Context) { c.Next() }
	}
	api.POST("/sessions", limit("start"), handler.start)
	api.POST("/sessions/:id/finish", limit("finish"), handler.finish)
	api.GET("/leaderboard", limit("leaderboard"), handler.leaderboard)
	return router, nil
}

func health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
