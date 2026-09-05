package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"

	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/poller"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/internal/server"
	"golang.org/x/sync/errgroup"
)

type ServeCmd struct {
	Host string `name:"host" help:"Override server listen host."`
	Port int    `name:"port" short:"p" help:"Override server listen port."`
}

func (s *ServeCmd) Run(
	ctx context.Context,
	cfg *config.Config,
	log *slog.Logger,
	validator *validator.Validator,
	store *inventory.Store,
	sweeper *scanner.Scanner,
	a *auth.Auth,
) error {
	// The flags override the file, and an unset flag is its zero value.
	host := cmp.Or(s.Host, cfg.Server.Host)
	port := cmp.Or(s.Port, cfg.Server.Port)

	sCfg := &server.Config{
		Addr:   host,
		Port:   port,
		Logger: log,
	}

	p := poller.New(log)

	defer p.Stop()

	discoverers, err := hostDiscoverers(cfg, log)
	if err != nil {
		return err
	}

	pd, err := poller.NewDevice(
		log,
		sweeper,
		store,
		cfg.Scan.Source,
		cfg.Scan.Devices.Interval,
		cfg.Networks,
		poller.WithDiscoverers(discoverers...),
	)
	if err != nil {
		return fmt.Errorf("initialize device poller: %w", err)
	}

	if cfg.Scan.Devices.Enabled {
		err := p.Register(pd)
		if err != nil {
			return fmt.Errorf("register device poller: %w", err)
		}
	}

	if cfg.Scan.Ports.Enabled {
		pp, err := portsPoller(cfg, log, store)
		if err != nil {
			return err
		}

		if err := p.Register(pp); err != nil {
			return fmt.Errorf("register port poller: %w", err)
		}
	}

	grp, ctx := errgroup.WithContext(ctx)

	grp.Go(func() error {
		err := server.Start(ctx, sCfg, store, validator, a)
		if err != nil {
			return fmt.Errorf("start server: %w", err)
		}

		return nil
	})

	grp.Go(func() error {
		err := p.Start(ctx)
		if err != nil {
			return fmt.Errorf("start poller: %w", err)
		}

		return nil
	})

	err = grp.Wait()
	if err != nil {
		return fmt.Errorf("errgroup: %w", err)
	}

	return nil
}

// portsPoller builds the port-scan task, resolving the port set from config: a
// blank scan.ports.custom leaves the curated preset, a spec replaces it. A spec
// that will not parse fails startup rather than every scan.
func portsPoller(cfg *config.Config, log *slog.Logger, store *inventory.Store) (*poller.Ports, error) {
	opts := []scanner.PortOption{
		scanner.WithConcurrency(cfg.Scan.Ports.Concurrency),
	}

	if spec := cfg.Scan.Ports.Custom; spec != "" {
		ports, err := scanner.ParsePortSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("scan.ports.custom: %w", err)
		}

		opts = append(opts, scanner.WithPorts(ports))
	}

	sc := scanner.NewPortScanner(log, opts...)

	return poller.NewPorts(log, sc, store, cfg.Scan.Source, cfg.Scan.Ports.Interval), nil
}
