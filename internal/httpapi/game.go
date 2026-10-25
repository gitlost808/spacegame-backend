package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"spacegame-backend/internal/game"
)

type GameService interface {
	Start(context.Context, string) (game.StartedSession, error)
	Finish(context.Context, string, string, int64) (game.Result, error)
	Leaderboard(context.Context, int32) ([]game.Entry, error)
}

type Config struct {
	Game           GameService
	AllowedOrigins []string
}

type gameHandler struct {
	service GameService
	logger  *slog.Logger
}

func (h gameHandler) start(c *gin.Context) {
	var body struct {
		PlayerName string `json:"player_name"`
	}
	if !readJSON(c, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	session, err := h.service.Start(ctx, body.PlayerName)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, session)
}

func (h gameHandler) finish(c *gin.Context) {
	credentials := strings.Fields(c.GetHeader("Authorization"))
	if len(credentials) != 2 || !strings.EqualFold(credentials[0], "Bearer") {
		h.writeError(c, game.ErrUnauthorized)
		return
	}
	var body struct {
		Score *int64 `json:"score"`
	}
	if !readJSON(c, &body) {
		return
	}
	if body.Score == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "score is required and must be an integer"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := h.service.Finish(ctx, c.Param("id"), credentials[1], *body.Score)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h gameHandler) leaderboard(c *gin.Context) {
	limit, err := strconv.ParseInt(c.DefaultQuery("limit", "20"), 10, 32)
	if err != nil || limit < 1 || limit > 100 {
		h.writeError(c, game.ErrInvalidLimit)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	entries, err := h.service.Leaderboard(ctx, int32(limit))
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

func (h gameHandler) writeError(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, game.ErrInvalidName), errors.Is(err, game.ErrInvalidSessionID), errors.Is(err, game.ErrInvalidLimit):
		code = http.StatusBadRequest
	case errors.Is(err, game.ErrUnauthorized):
		code = http.StatusUnauthorized
		c.Header("WWW-Authenticate", "Bearer")
	case errors.Is(err, game.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, game.ErrAlreadyFinished):
		code = http.StatusConflict
	case errors.Is(err, game.ErrExpired):
		code = http.StatusGone
	case errors.Is(err, game.ErrTooShort), errors.Is(err, game.ErrInvalidScore):
		code = http.StatusUnprocessableEntity
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code = http.StatusServiceUnavailable
	}
	message := err.Error()
	if code >= 500 {
		h.logger.Error("game request failed", "error", err)
		message = "service unavailable"
	}
	c.JSON(code, gin.H{"error": message})
}

func readJSON(c *gin.Context, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "Content-Type must be application/json"})
		return false
	}
	body := http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body must not exceed 4096 bytes"})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "could not read request body"})
		}
		return false
	}
	if err := json.Unmarshal(data, destination); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON request"})
		return false
	}
	return true
}

func cors(origins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowed[origin] = true
		}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		c.Writer.Header().Add("Vary", "Origin")
		if origin != "" && (allowed[origin] || allowed["*"]) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			c.Header("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
