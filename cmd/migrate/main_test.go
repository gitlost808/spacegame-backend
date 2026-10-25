package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateMigrationWithoutDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := run(context.Background(), []string{"-dir", dir, "create", "create_records"}, ""); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*_create_records.sql"))
	if err != nil || len(files) != 1 {
		t.Fatalf("migration files = %v, error = %v; want one SQL migration", files, err)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, annotation := range []string{"-- +goose Up", "-- +goose Down"} {
		if !strings.Contains(string(content), annotation) {
			t.Errorf("migration is missing %q", annotation)
		}
	}
}

func TestInvalidArgumentsAndConfiguration(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing command", nil, "a migration command is required"},
		{"unknown command", []string{"unknown"}, "unknown migration command"},
		{"missing name", []string{"create"}, "create NAME"},
		{"empty name", []string{"create", ""}, "create NAME"},
		{"unexpected argument", []string{"up", "extra"}, "does not accept additional arguments"},
		{"missing database URL", []string{"up"}, "DATABASE_URL is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(context.Background(), tt.args, "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
