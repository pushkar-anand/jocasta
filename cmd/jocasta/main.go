package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// Link sqlc with go generate, running go generate will generate the DB models and queries.
//go:generate go tool sqlc generate -f ./../../sqlc.yaml

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)

		os.Exit(1)
	}
}

func run(args []string) error {
	ctx := context.Background()

	// Create a context that will be canceled when the OS sends a signal to the process.
	// This will be used to gracefully shut down the application, shutting down the server and other workers.
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGINT, syscall.SIGABRT, syscall.SIGTERM)
	defer cancel()

	var cli CLI

	parser, err := newParser(&cli, kong.BindTo(ctx, (*context.Context)(nil)))
	if err != nil {
		return fmt.Errorf("init cli: %w", err)
	}

	kCtx, err := parser.Parse(args)
	if err != nil {
		// kong.UsageOnError() only takes effect through FatalIfErrorf, which
		// exits the process. Print the usage block here instead so the main stays
		// in charge of exiting and let the main report the message at
		// once.
		if parseErr, ok := errors.AsType[*kong.ParseError](err); ok {
			_ = parseErr.Context.PrintUsage(false)
		}

		return err
	}

	cfg, err := loadConfig(&cli)
	if err != nil {
		return err
	}

	log := logger.New(
		logger.WithLevel(cfg.Logger.SlogLevel()),
		logger.WithFormat(cfg.Logger.FormatValue()),
	)

	conn, err := db.New(&db.Config{Path: cfg.DB.Path, Name: cfg.DB.Name})
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}

	defer func() { _ = conn.Conn.Close() }()

	v, err := validator.New(
		// The default message for oneof names the rule rather than the values
		// it admits, which tells a client its filter was wrong without telling
		// it what would be right.
		validator.WithCustomMessage("oneof", func(field, param string) string {
			return fmt.Sprintf("%s must be one of: %s", field, strings.ReplaceAll(param, " ", ", "))
		}),
	)
	if err != nil {
		return fmt.Errorf("initialize validator: %w", err)
	}

	store := inventory.New(
		conn.Conn,
		log,
		inventory.WithOnlineWindow(cfg.Inventory.OnlineWindow),
	)

	// The scanner the poller sweeps with is configured, not flagged: nobody is
	// at a terminal to pass rates to a sweep that runs on a timer. The scan
	// command builds its own from its flags, which are per-invocation.
	sweeper := scanner.New(
		log,
		scanner.WithRate(cfg.Scan.Devices.Rate),
		scanner.WithRounds(cfg.Scan.Devices.Rounds),
		scanner.WithWait(cfg.Scan.Devices.Wait),
		scanner.WithNameResolution(cfg.Scan.Devices.ResolveNames),
		scanner.WithMACResolution(cfg.Scan.Devices.ResolveMACs),
	)

	// Kong hands each command only the arguments its Run signature names, so a
	// command that wants none of these stays as it is.
	return kCtx.Run(cfg, log, conn, v, store, sweeper)
}

// loadConfig loads the configuration named by cli, then layers the global CLI
// flags over it.
func loadConfig(cli *CLI) (*config.Config, error) {
	cfg, err := config.New(cli.ConfigFile)
	if err != nil {
		return nil, err
	}

	if cli.LogLevel != "" {
		cfg.Logger.Level = cli.LogLevel
	}

	if cli.LogFormat != "" {
		cfg.Logger.Format = cli.LogFormat
	}

	return cfg, nil
}
