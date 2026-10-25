package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"spacegame-backend/internal/database"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	databaseURL, err := database.URLFromEnv()
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
	if err := run(ctx, os.Args[1:], databaseURL); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, databaseURL string) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dir := flags.String("dir", "migrations", "directory containing SQL migrations")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: migrate [-dir migrations] {create NAME|up|down|status|version}")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() == 0 {
		return errors.New("a migration command is required; use migrate -h for help")
	}
	command := flags.Arg(0)
	switch command {
	case "create":
		if flags.NArg() != 2 || strings.TrimSpace(flags.Arg(1)) == "" {
			return errors.New("usage: migrate [-dir migrations] create NAME")
		}
		return goose.RunContext(ctx, command, nil, *dir, flags.Arg(1), "sql")
	case "up", "down", "status", "version":
		if flags.NArg() != 1 {
			return fmt.Errorf("%s does not accept additional arguments", command)
		}
	default:
		return fmt.Errorf("unknown migration command %q; use migrate -h for help", command)
	}
	if strings.TrimSpace(databaseURL) == "" {
		return errors.New("DATABASE_URL is required")
	}

	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	if err := database.ValidateTLS(&config.Config); err != nil {
		return err
	}
	db := stdlib.OpenDB(*config)
	defer db.Close()
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = db.PingContext(connectCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.RunContext(ctx, command, db, *dir); err != nil {
		if errors.Is(err, goose.ErrNoMigrationFiles) && (command == "up" || command == "status") {
			log.Printf("no SQL migrations found in %s", *dir)
			return nil
		}
		return err
	}
	return nil
}
