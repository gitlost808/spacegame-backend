package config_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"spacegame-backend/internal/config"
)

func TestLocalDotEnvPreservesInjectedSettings(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("APP_ENV", "development")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8082")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	t.Setenv("GIN_MODE", "")
	if err := os.Unsetenv("GIN_MODE"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("HTTP_ADDR=:8081\nGIN_MODE=release\nCORS_ALLOWED_ORIGINS=https://game.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.LoadDotEnv(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("GIN_MODE"); got != "release" {
		t.Fatalf("local setting was not loaded: %q", got)
	}
	if got := os.Getenv("HTTP_ADDR"); got != "127.0.0.1:8082" {
		t.Fatalf("injected address was overwritten: %q", got)
	}
	if got := os.Getenv("CORS_ALLOWED_ORIGINS"); got != "" {
		t.Fatalf("explicit empty origin list was overwritten: %q", got)
	}
}

func TestDotEnvMissingMalformedAndProduction(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("APP_ENV", "")
	if err := config.LoadDotEnv(); err != nil {
		t.Fatalf("an absent .env should be optional: %v", err)
	}
	if err := os.WriteFile(".env", []byte("INVALID@KEY=private-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.LoadDotEnv(); err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("malformed .env must fail without exposing its contents: %v", err)
	}
	for _, environment := range []string{"production", "staging", "test"} {
		t.Setenv("APP_ENV", environment)
		if err := config.LoadDotEnv(); err != nil {
			t.Fatalf("%s must ignore local .env files: %v", environment, err)
		}
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "injected-database")
	if err := os.WriteFile(".env", []byte("APP_ENV=development\nDATABASE_URL=local-database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.LoadDotEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("APP_ENV") != "production" || os.Getenv("DATABASE_URL") != "injected-database" {
		t.Fatal("production configuration must come exclusively from the process environment")
	}
}

func TestServerConfiguration(t *testing.T) {
	for _, name := range []string{"HTTP_ADDR", "METRICS_ADDR", "GIN_MODE", "CORS_ALLOWED_ORIGINS", "TRUSTED_PROXIES"} {
		t.Setenv(name, "")
	}
	settings, err := config.ServerFromEnv()
	if err != nil || settings.HTTPAddr != ":8080" || settings.MetricsAddr != "127.0.0.1:9090" || settings.GinMode != "release" || len(settings.AllowedOrigins) != 0 {
		t.Fatalf("defaults=%+v error=%v", settings, err)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:8081")
	t.Setenv("METRICS_ADDR", ":9091")
	t.Setenv("GIN_MODE", "debug")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://game.example,https://other.example")
	t.Setenv("TRUSTED_PROXIES", "192.0.2.1, 198.51.100.0/24")
	settings, err = config.ServerFromEnv()
	if err != nil || settings.HTTPAddr != "127.0.0.1:8081" || settings.MetricsAddr != ":9091" || settings.GinMode != "debug" || settings.TrustedProxies != "192.0.2.1, 198.51.100.0/24" || !reflect.DeepEqual(settings.AllowedOrigins, []string{"https://game.example", "https://other.example"}) {
		t.Fatalf("overrides=%+v error=%v", settings, err)
	}
}
