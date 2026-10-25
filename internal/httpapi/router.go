package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
)

// Pinger checks whether a database connection is available.
type Pinger interface {
	Ping(context.Context) error
}

// NewHandler registers the HTTP API routes.
func NewHandler(logger *slog.Logger, db Pinger) *gin.Engine {
	router := gin.New()
	router.HandleMethodNotAllowed = true
	_ = router.SetTrustedProxies(nil)
	router.Use(gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger.Error("request panicked", "error", fmt.Sprint(recovered), "stack", string(debug.Stack()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}))
	router.GET("/healthz", health)
	router.HEAD("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			logger.Warn("database readiness check failed", "error", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	return router
}

func health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
