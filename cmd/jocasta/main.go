package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/pushkar-anand/build-with-go/config"
	"github.com/pushkar-anand/build-with-go/logger"
)

// Link sqlc with go generate, now we need to just run go generate to generate models and functions for DB
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
	parser, err := kong.New(&cli,
		kong.Name("jocasta"),
		kong.Description("Network discovery and homelab device inventory tool."),
		kong.UsageOnError(),
		kong.BindTo(ctx, (*context.Context)(nil)),
	)
	if err != nil {
		return fmt.Errorf("init cli: %w", err)
	}

	kCtx, err := parser.Parse(args)
	if err != nil {
		parser.Errorf("%s", err)
		return err
	}

	cfg, err := config.Load[Config](
		config.WithDefaults(defaults),
		config.WithYAML(cli.ConfigFile),
		config.WithEnvPrefix("JOCASTA_"),
	)
	if err != nil {
		slog.Error("Failed to initialize config", logger.Err(err))
		return err
	}

	if cli.LogLevel != "" {
		cfg.Logger.Level = cli.LogLevel
	}
	if cli.LogFormat != "" {
		cfg.Logger.Format = cli.LogFormat
	}

	log := logger.New(
		logger.WithLevel(cfg.Logger.SlogLevel()),
		logger.WithFormat(cfg.Logger.FormatValue()),
	)

	return kCtx.Run(cfg, log)
}
