package database_test

import (
	"context"
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
