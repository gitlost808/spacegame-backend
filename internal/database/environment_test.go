package database_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"spacegame-backend/internal/database"
)

func TestDatabaseSecretFile(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", "")
	if value, err := database.URLFromEnv(); err != nil || value != "" {
		t.Fatalf("unset: %q %v", value, err)
	}
	path := filepath.Join(t.TempDir(), "database_url")
	secret := "postgres://pilot:private-password@db/game?sslmode=verify-full"
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL_FILE", path)
	if value, err := database.URLFromEnv(); err != nil || value != secret {
		t.Fatalf("secret file: error=%v", err)
	}
	t.Setenv("DATABASE_URL", "another-url")
	if _, err := database.URLFromEnv(); err == nil {
		t.Fatal("ambiguous configuration must fail")
	}
	t.Setenv("DATABASE_URL", "")
	for _, content := range []string{"", strings.Repeat("x", 16385)} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := database.URLFromEnv(); err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("invalid secret files must fail without exposing the value")
		}
	}
}

func TestProductionRequiresVerifiedTLS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	for _, mode := range []string{"disable", "prefer", "require", "verify-ca"} {
		pool, err := database.Open(context.Background(), "postgres://pilot:password@localhost/game?sslmode="+mode)
		if pool != nil {
			pool.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "TLS") {
			t.Fatalf("sslmode=%s must be rejected before connecting: %v", mode, err)
		}
	}
	config, err := pgx.ParseConfig("postgres://pilot:password@localhost/game?sslmode=verify-full")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ValidateTLS(&config.Config); err != nil {
		t.Fatalf("verified TLS should be accepted: %v", err)
	}
}
