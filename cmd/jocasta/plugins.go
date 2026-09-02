package main

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// hostDiscoverers builds every enabled source that can be asked which devices
// it knows about.
//
// They are built here rather than in internal/plugin, which would have to
// import internal/config and every implementation to do it, turning a near-leaf
// package into a hub.
//
// Construction performs no I/O, so a router that is down at boot is a source to
// retry rather than a reason to refuse to start. A source that cannot be
// constructed at all is a config error and is reported: a misconfigured entry
// that silently discovered nothing would look exactly like a quiet network.
//
// An instance is off unless it says enabled, since a map-keyed block has no
// static key path and so cannot carry a default.
//
// Instances are returned in name order because map iteration is not ordered,
// and a poller that reads its sources in a different order every cycle is
// harder to read in a log than one that does not.
func hostDiscoverers(cfg *config.Config, log *slog.Logger) ([]plugin.HostDiscoverer, error) {
	names := slices.Sorted(maps.Keys(cfg.Plugins.RouterOS))
	out := make([]plugin.HostDiscoverer, 0, len(names))

	for _, name := range names {
		rc := cfg.Plugins.RouterOS[name]
		if !rc.Enabled {
			// Said out loud, because a configured entry that reads nothing is
			// indistinguishable from a source with nothing to report.
			log.Info("source is configured but not enabled", slog.String("source", name))

			continue
		}

		p, err := newRouterOS(name, rc, log)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

// newRouterOS builds one configured RouterOS source.
func newRouterOS(name string, cfg config.RouterOS, log *slog.Logger) (*plugin.RouterOS, error) {
	client, err := routeros.New(&routeros.Config{
		Host:     cfg.Host,
		Port:     cfg.Port,
		User:     cfg.User,
		Password: cfg.Password,
		SSL:      cfg.SSL,
		Insecure: cfg.Insecure,
		Timeout:  cfg.Timeout,
	}, log)
	if err != nil {
		return nil, fmt.Errorf("plugin routeros %q: %w", name, err)
	}

	p, err := plugin.NewRouterOS(name, client, log)
	if err != nil {
		return nil, fmt.Errorf("plugin routeros %q: %w", name, err)
	}

	return p, nil
}
