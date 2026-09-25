package main

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/notify"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/poller"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/internal/server"
	"github.com/pushkar-anand/jocasta/pkg/geo"
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
	conn *sql.DB,
	validator *validator.Validator,
	store *inventory.Store,
	sweeper *scanner.Scanner,
	a *auth.Auth,
) error {
	// The flags override the file, and an unset flag is its zero value.
	host := cmp.Or(s.Host, cfg.Server.Host)
	port := cmp.Or(s.Port, cfg.Server.Port)

	sCfg := &server.Config{
		Addr:                host,
		Port:                port,
		Logger:              log,
		CORSAllowedOrigins:  cfg.Server.CORS.AllowedOrigins,
		SessionLifetime:     cfg.Server.Auth.SessionLifetime,
		SessionIdleTimeout:  cfg.Server.Auth.IdleTimeout,
		SessionCookieSecure: cfg.Server.Auth.CookieSecure,
		MCPEnabled:          cfg.Server.MCP.Enabled,
	}

	p := poller.New(log)

	defer p.Stop()

	discoverers, err := hostDiscoverers(ctx, cfg, log)
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

	// Zero retention keeps a log forever; with both at zero there is nothing
	// to schedule.
	if cfg.Retention.History > 0 || cfg.Retention.Traffic > 0 {
		err := p.Register(poller.NewPrune(log, store, cfg.Retention.History, cfg.Retention.Traffic))
		if err != nil {
			return fmt.Errorf("register pruner: %w", err)
		}
	}

	destinations, err := notifyDestinations(ctx, cfg, log)
	if err != nil {
		return err
	}

	reporters, err := trafficReporters(ctx, cfg, log)
	if err != nil {
		return err
	}

	grp, ctx := errgroup.WithContext(ctx)

	if len(reporters) > 0 {
		sCfg.RecentTraffic = startTraffic(ctx, grp, log, store, reporters)
	}

	// Set before the poller starts, so no scan finishes unheard.
	if len(destinations) > 0 {
		n := notify.New(conn, store, log, destinations...)
		store.OnScanFinished(n.Queue)

		grp.Go(func() error { return n.Run(ctx) })
	}

	if sCfg.HomeCountry, err = homeCountry(cfg.Location.Country); err != nil {
		return err
	}

	grp.Go(func() error {
		err := server.Start(ctx, sCfg, conn, store, validator, a)
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
// that will not parse fails startup, before any scan runs.
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

// homeCountry checks location.country against the countries the world map
// draws, so a typo fails startup. Unchecked, it would quietly draw no lines.
// Case does not matter; empty is fine.
func homeCountry(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", nil
	}

	if _, ok := geo.CountryOf(code); !ok {
		return "", fmt.Errorf("location.country: %q is not the two-letter code of a country on the map", code)
	}

	return code, nil
}

// startTraffic runs every traffic source's listener and the recorder they feed,
// and returns the recorder for the map to read what is active from.
// A listener that cannot bind fails startup through the group, the same as a
// server that cannot: a configured source that silently received nothing would
// read as a network with no traffic.
func startTraffic(
	ctx context.Context,
	grp *errgroup.Group,
	log *slog.Logger,
	store *inventory.Store,
	reporters []plugin.TrafficReporter,
) *inventory.TrafficRecorder {
	rec := inventory.NewTrafficRecorder(store, log, hosts.ResolveName)

	grp.Go(func() error { return rec.Run(ctx) })

	for _, r := range reporters {
		grp.Go(func() error {
			return r.Listen(ctx, func(_ context.Context, flows []plugin.Flow) { rec.Add(r, flows) })
		})
	}

	return rec
}
