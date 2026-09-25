package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // location.timezone must resolve on a host without a zone database

	"github.com/alecthomas/kong"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/api"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// Regenerate the sqlc models and query code from sqlc.yaml.
//go:generate go tool sqlc generate -f ./../../sqlc.yaml

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)

		os.Exit(1)
	}
}

func run(args []string) error {
	ctx := context.Background()

	// A signal cancels this context; the server and every worker shut down
	// when it does, so an interrupt lets work in flight finish.
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGINT, syscall.SIGABRT, syscall.SIGTERM)
	defer cancel()

	var cli CLI

	parser, err := newParser(&cli, kong.BindTo(ctx, (*context.Context)(nil)))
	if err != nil {
		return fmt.Errorf("init cli: %w", err)
	}

	kCtx, err := parser.Parse(args)
	if err != nil {
		// kong.UsageOnError() only fires through FatalIfErrorf, which exits the
		// process itself. Printing usage here keeps the one exit path in run,
		// which reports the message.
		if parseErr, ok := errors.AsType[*kong.ParseError](err); ok {
			_ = parseErr.Context.PrintUsage(false)
		}

		return err
	}

	// version reports the build and exits. It is handled here because the steps
	// below load configuration and open the database, and opening the database
	// creates one when none exists: a surprising side effect of asking for a
	// version number.
	if kCtx.Command() == "version" {
		return kCtx.Run()
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

	defer func() { _ = conn.Close() }()

	v, err := validator.New(
		// The default message for oneof names only the rule. This one lists
		// the values it admits, so a client learns what would be right.
		validator.WithCustomMessage("oneof", func(field, param string) string {
			return fmt.Sprintf("%s must be one of: %s", field, strings.ReplaceAll(param, " ", ", "))
		}),
		// The device's type must name one of the classes the classifier knows.
		// The rule is registered here because the tag is used on the API's
		// curation request; registering it in the handler would mean the reader
		// it hands out was built with a vocabulary the validator had not heard
		// of.
		validator.WithCustomTags(map[string]validator.ValidationFunc{
			"deviceclass": api.DeviceClassRule,
		}),
	)
	if err != nil {
		return fmt.Errorf("initialize validator: %w", err)
	}

	store := inventory.New(
		conn,
		log,
		inventory.WithOnlineWindow(cfg.Inventory.OnlineWindow),
		inventory.WithAddressGrace(cfg.Inventory.AddressGrace),
	)

	a, err := auth.New(
		models.New(conn),
		password.NewHasher(password.WithKeyLength(64)),
	)
	if err != nil {
		return fmt.Errorf("initialize auth: %w", err)
	}

	// The scanner the poller sweeps with comes from config: nobody is at a
	// terminal to pass rates to a sweep that runs on a timer. The scan
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
	return kCtx.Run(cfg, log, conn, v, store, sweeper, a)
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

	// Set process-wide, before anything reads the clock, so every command
	// shows times in the same zone.
	loc, err := timezone(cfg.Location.Timezone)
	if err != nil {
		return nil, err
	}

	if loc != nil {
		time.Local = loc
	}

	return cfg, nil
}

// timezone resolves location.timezone, the zone times are shown in; a
// container's is otherwise UTC whatever the network's is. Empty returns nil,
// leaving the TZ variable and the system to decide. A name that does not
// resolve fails startup; unchecked, it would quietly show UTC.
func timezone(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil //nolint:nilnil // nil means "leave time.Local alone"
	}

	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("location.timezone: %q is not a known time zone: %w", name, err)
	}

	return loc, nil
}
