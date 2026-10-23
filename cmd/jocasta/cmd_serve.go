package main

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/pushkar-anand/build-with-go/http/clientip"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/localnet"
	"github.com/pushkar-anand/jocasta/internal/notify"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/poller"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/internal/server"
	"github.com/pushkar-anand/jocasta/pkg/geo"
	"golang.org/x/sync/errgroup"
)

// ServeCmd configures HTTP listener overrides for the running service.
type ServeCmd struct {
	Host string `name:"host" help:"Override server listen host."`
	Port int    `name:"port" short:"p" help:"Override server listen port."`
}

// Run serves HTTP and runs the configured collectors and notification workers.
// It waits for shutdown and stops the poller before returning.
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

	trustedProxies, err := parseTrustedProxies(cfg.Server.TrustedProxies)
	if err != nil {
		return fmt.Errorf("parse trusted proxies: %w", err)
	}

	sCfg := &server.Config{
		Addr:                host,
		Port:                port,
		Logger:              log,
		CORSAllowedOrigins:  cfg.Server.CORS.AllowedOrigins,
		SessionLifetime:     cfg.Server.Auth.SessionLifetime,
		SessionIdleTimeout:  cfg.Server.Auth.IdleTimeout,
		SessionCookieSecure: cfg.Server.Auth.CookieSecure,
		MCPEnabled:          cfg.Server.MCP.Enabled,
		TrustedProxies:      trustedProxies,
	}

	p := poller.New(log, store)

	defer p.Stop()

	sources, err := routerSources(ctx, cfg, log)
	if err != nil {
		return err
	}

	deviceOpts := []poller.DeviceOption{poller.WithDiscoverers(hostDiscoverers(sources)...)}

	var pp *poller.Ports

	if cfg.Scan.Ports.Enabled {
		if pp, err = portsPoller(cfg, log, store); err != nil {
			return err
		}

		deviceOpts = append(deviceOpts, poller.WithPortScan(pp))
	}

	if len(cfg.Networks) == 0 {
		cfg.Networks = currentNetwork(ctx, log)
	}

	pd, err := poller.NewDevice(
		log,
		sweeper,
		store,
		cfg.Scan.Source,
		cfg.Scan.Devices.Interval,
		cfg.Networks,
		deviceOpts...,
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

	readers := topologyReaders(sources)

	// On the sweep's schedule: a switch forgets an address a few minutes
	// after the device goes quiet, so reading it far less often than the
	// sweep runs would miss the devices that come and go.
	if len(readers) > 0 {
		err := p.Register(poller.NewTopology(log, store, cfg.Scan.Devices.Interval, readers...))
		if err != nil {
			return fmt.Errorf("register topology reader: %w", err)
		}
	}

	resolvers, err := queryReaders(ctx, cfg, log)
	if err != nil {
		return err
	}

	if len(resolvers) > 0 {
		if err := p.Register(poller.NewDNS(log, store, resolvers...)); err != nil {
			return fmt.Errorf("register dns reader: %w", err)
		}
	}

	if pp != nil {
		if err := p.Register(pp); err != nil {
			return fmt.Errorf("register port poller: %w", err)
		}
	}

	// Zero retention keeps a kind forever; with every window at zero there is
	// nothing to schedule.
	r := inventory.Retention{
		History: cfg.Retention.History,
		Traffic: cfg.Retention.Traffic,
		DNS:     cfg.Retention.DNS,
		Devices: cfg.Retention.Devices,
	}

	if r.History > 0 || r.Traffic > 0 || r.DNS > 0 || r.Devices > 0 {
		err := p.Register(poller.NewPrune(log, store, r))
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
		sCfg.Notifier = n

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

// currentNetworkMinBits is the shortest prefix that currentNetwork sweeps. A
// larger network takes a long time to sweep. So the operator must list it.
const currentNetworkMinBits = 22

// currentNetwork returns the network of the host, for use when the config
// lists no networks. It returns nil if Jocasta cannot find the network, or if
// the network is larger than a /22.
func currentNetwork(ctx context.Context, log *slog.Logger) []string {
	n, err := localnet.Current(ctx)
	if err != nil {
		log.WarnContext(ctx, "no networks configured, and the host's network was not found; sweeping none",
			logger.Err(err),
		)

		return nil
	}

	if n.Bits() < currentNetworkMinBits {
		log.WarnContext(ctx, "no networks configured, and the host's network is larger than a /22; list it under networks to sweep it",
			slog.String("network", n.String()),
		)

		return nil
	}

	log.InfoContext(ctx, "no networks configured; sweeping the host's network",
		slog.String("network", n.String()),
	)

	return []string{n.String()}
}

// portsPoller builds the port-scan task. A blank scan.ports.custom keeps the
// curated preset, and a spec replaces it. A spec that will not parse fails
// startup, before any scan runs.
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
// draws. A typo fails startup; otherwise the map would quietly draw no lines.
// Case does not matter, and empty is fine.
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

// startTraffic runs each traffic source's listener and the recorder they feed.
// It returns the recorder, which the map reads for active traffic. A listener
// that cannot bind fails startup, like a server that cannot. Otherwise a source
// that received nothing would look like a network with no traffic.
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

// parseTrustedProxies parses the proxy addresses and CIDR prefixes from config.
// A bare address becomes a prefix covering just that address.
func parseTrustedProxies(raw []string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix

	for _, s := range raw {
		p, err := clientip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("server.trusted_proxies %q: %w", s, err)
		}

		prefixes = append(prefixes, p)
	}

	return prefixes, nil
}
