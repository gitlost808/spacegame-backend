package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"spacegame-backend/internal/database"
	"spacegame-backend/internal/game"
	"spacegame-backend/internal/httpapi"
	"spacegame-backend/internal/maintenance"
	"spacegame-backend/internal/observability"
	"spacegame-backend/internal/store"
	"spacegame-backend/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	if err := run(ctx, addr, logger); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, addr string, logger *slog.Logger) error {
	databaseURL, err := database.URLFromEnv()
	if err != nil {
		return err
	}
	policy, err := game.PolicyFromEnv()
	if err != nil {
		return err
	}
	cleanupConfig, err := maintenance.CleanupConfigFromEnv()
	if err != nil {
		return err
	}
	rateConfig, err := httpapi.RateConfigFromEnv()
	if err != nil {
		return err
	}
	limiter, err := httpapi.NewRateLimiter(rateConfig)
	if err != nil {
		return err
	}
	versions, err := migrations.Versions()
	if err != nil {
		return err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	pool, err := database.Open(connectCtx, databaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()
	queries := store.New(pool)
	service, err := game.NewService(queries, policy)
	if err != nil {
		return err
	}
	metrics := observability.NewMetrics()
	handler, err := httpapi.NewHandler(logger, database.Readiness{Pool: pool, Versions: versions}, httpapi.Config{
		Game: service, AllowedOrigins: strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ","),
		TrustedProxies: strings.Fields(strings.ReplaceAll(os.Getenv("TRUSTED_PROXIES"), ",", " ")),
		Limiter:        limiter, Metrics: metrics,
	})
	if err != nil {
		return err
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		maintenance.Cleaner{Queries: queries, Config: cleanupConfig, Logger: logger, Observe: metrics.ObserveCleanup}.Run(workerCtx)
	}()
	defer func() {
		cancelWorker()
		<-workerDone
	}()

	newServer := func(address string, handler http.Handler) *http.Server {
		return &http.Server{
			Addr: address, Handler: handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	}
	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = "127.0.0.1:9090"
	}
	servers := []*http.Server{newServer(addr, handler), newServer(metricsAddr, metrics.Handler())}
	serverErrors := make(chan error, len(servers))
	for _, server := range servers {
		go func() {
			logger.Info("server starting", "addr", server.Addr)
			serverErrors <- server.ListenAndServe()
		}()
	}
	var result error
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			result = fmt.Errorf("serve HTTP: %w", err)
		}
	case <-ctx.Done():
		logger.Info("server shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			result = errors.Join(result, fmt.Errorf("shut down HTTP server: %w", err))
		}
	}
	return result
}
