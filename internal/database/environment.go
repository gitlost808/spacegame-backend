package database

import (
	"errors"
	"io"
	"os"
	"strings"
)

// URLFromEnv supports either a connection string or a mounted secret file.
// An unset value is returned unchanged so non-database commands still work.
func URLFromEnv() (string, error) {
	value := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	path := os.Getenv("DATABASE_URL_FILE")
	if path == "" {
		return value, nil
	}
	if value != "" {
		return "", errors.New("set only one of DATABASE_URL and DATABASE_URL_FILE")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("cannot open DATABASE_URL_FILE")
	}
	defer file.Close()
	buffer, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(buffer) > 16384 {
		return "", errors.New("DATABASE_URL_FILE must contain a non-empty connection string of at most 16 KiB")
	}
	value = strings.TrimSpace(string(buffer))
	if value == "" {
		return "", errors.New("DATABASE_URL_FILE is empty")
	}
	return value, nil
}
