// Package config loads process configuration and optional local development settings.
package config

import (
	"errors"
	"os"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Server struct {
	HTTPAddr       string   `env:"HTTP_ADDR" envDefault:":8080"`
	MetricsAddr    string   `env:"METRICS_ADDR" envDefault:"127.0.0.1:9090"`
	GinMode        string   `env:"GIN_MODE" envDefault:"release"`
	AllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS"`
	TrustedProxies string   `env:"TRUSTED_PROXIES"`
}

func ServerFromEnv() (Server, error) {
	return env.ParseAs[Server]()
}

// LoadDotEnv loads local defaults without replacing existing process variables.
// Production and other explicitly named environments use injected settings only.
func LoadDotEnv() error {
	if environment := os.Getenv("APP_ENV"); environment != "" && environment != "development" {
		return nil
	}
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot load .env; check its syntax and permissions")
	}
	return nil
}
