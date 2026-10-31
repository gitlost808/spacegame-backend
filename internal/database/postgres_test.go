package database_test

import (
	"context"
	"strings"
	"testing"

	"spacegame-backend/internal/database"
)

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	for _, databaseURL := range []string{"", "   ", "postgres://localhost:invalid/spacegame"} {
		t.Run(databaseURL, func(t *testing.T) {
			pool, err := database.Open(context.Background(), databaseURL)
			if pool != nil {
				pool.Close()
				t.Fatal("expected no pool for invalid configuration")
			}
			if err == nil {
				t.Fatal("expected an error for invalid configuration")
			}
		})
	}
}

func TestOpenProductionRejectsImplicitSSLMode(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	pool, err := database.Open(context.Background(), "postgres://pilot:password@localhost/game")
	if pool != nil {
		pool.Close()
		t.Fatal("an implicit SSL mode must not open a pool")
	}
	if err == nil || !strings.Contains(err.Error(), "sslmode") {
		t.Fatalf("must reject an implicit SSL mode before connecting: %v", err)
	}
}
