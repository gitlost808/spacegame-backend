package database_test

import (
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

func TestProductionRequiresExplicitSSLMode(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		connectionURL := "postgres://pilot:password@localhost/game?sslmode=" + mode
		if _, err := pgx.ParseConfig(connectionURL); err != nil {
			t.Fatalf("sslmode=%s must be accepted by pgx: %v", mode, err)
		}
		if err := database.ValidateSSLMode(connectionURL); err != nil {
			t.Fatalf("explicit sslmode=%s must be accepted in production: %v", mode, err)
		}
	}
	for _, connectionURL := range []string{
		"postgres://pilot:private-password@localhost/game",
		"postgres://pilot:private-password@localhost/game?sslmode=",
		"postgres://pilot:private-password@localhost/game?application_name=sslmode%3Ddisable",
		"postgres://pilot:private-password@localhost/game?sslmode=disable&sslmode=",
		"postgres://pilot:private-password@localhost/game?sslmode=&sslmode=disable",
	} {
		if err := database.ValidateSSLMode(connectionURL); err == nil || !strings.Contains(err.Error(), "sslmode") || strings.Contains(err.Error(), "private-password") {
			t.Fatalf("missing explicit sslmode must fail without exposing credentials: %v", err)
		}
	}
	t.Setenv("APP_ENV", "development")
	if err := database.ValidateSSLMode("postgres://pilot:password@localhost/game"); err != nil {
		t.Fatalf("development should retain pgx defaults: %v", err)
	}
}
