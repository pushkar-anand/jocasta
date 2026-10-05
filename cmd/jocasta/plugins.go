package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/pushkar-anand/jocasta/internal/config"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/pkg/openwrt"
	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// routerSources builds every enabled router, switch and access point, of every
// kind. They are one list, which hostDiscoverers and topologyReaders each take
// their share of.
//
// They are built here because internal/plugin would have to import
// internal/config and every implementation to do it, turning a near-leaf
// package into a hub.
//
// Construction performs no I/O, so a router that is down at boot is retried
// later and the server still starts. A source that cannot be
// constructed at all is a config error and is reported: a misconfigured entry
// that silently discovered nothing would look exactly like a quiet network.
//
// An instance is off unless it says enabled, since a map-keyed block has no
// static key path and so cannot carry a default.
//
// Instances are returned in name order because map iteration is not ordered,
// and a poller that reads its sources in a different order every cycle is
// harder to read in a log than one that does not.
func routerSources(ctx context.Context, cfg *config.Config, log *slog.Logger) ([]plugin.Plugin, error) {
	instances, err := routerInstances(cfg)
	if err != nil {
		return nil, err
	}

	names := slices.Sorted(maps.Keys(instances))
	out := make([]plugin.Plugin, 0, len(names))

	for _, name := range names {
		in := instances[name]

		if !in.source.Enabled {
			// Said out loud, because a configured entry that reads nothing is
			// indistinguishable from a source with nothing to report.
			log.InfoContext(ctx, "source is configured but not enabled", slog.String("src", name))

			continue
		}

		if in.plainHTTP {
			log.WarnContext(ctx,
				"router is reached over plain HTTP, so its password crosses the network unencrypted. Set ssl: true",
				slog.String("src", name))
		}

		p, err := in.build(log)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

// routerInstance is one configured router source, of any kind.
type routerInstance struct {
	kind   string
	source config.Source

	// plainHTTP is false for a kind not reached over HTTP at all.
	plainHTTP bool

	build func(log *slog.Logger) (plugin.Plugin, error)
}

// routerConfig is a router kind's config: anything that embeds
// [config.Source].
type routerConfig interface {
	Common() config.Source
}

// routerInstances returns every configured router source by name, of every
// kind. A new kind needs only its line here: whether it is enabled, and
// whether it is reached over plain HTTP, come from the blocks its config
// embeds.
//
// A name used under two kinds is refused. `jocasta plugin run` takes the bare
// name, which would then name two sources.
func routerInstances(cfg *config.Config) (map[string]routerInstance, error) {
	out := make(map[string]routerInstance)

	if err := addRouterKind(out, "routeros", cfg.Plugins.RouterOS, newRouterOS); err != nil {
		return nil, err
	}

	if err := addRouterKind(out, "openwrt", cfg.Plugins.OpenWrt, newOpenWrt); err != nil {
		return nil, err
	}

	return out, nil
}

// addRouterKind adds the instances in configs, of the kind configured under
// plugins.<kind>, to out.
func addRouterKind[C routerConfig](
	out map[string]routerInstance,
	kind string,
	configs map[string]C,
	build func(name string, cfg C, log *slog.Logger) (plugin.Plugin, error),
) error {
	for name, c := range configs {
		if prev, ok := out[name]; ok {
			return fmt.Errorf("source name %q is configured under both plugins.%s and plugins.%s", name, prev.kind, kind)
		}

		var plainHTTP bool
		if h, ok := any(c).(interface{ PlainHTTP() bool }); ok {
			plainHTTP = h.PlainHTTP()
		}

		out[name] = routerInstance{
			kind:      kind,
			source:    c.Common(),
			plainHTTP: plainHTTP,
			build: func(log *slog.Logger) (plugin.Plugin, error) {
				return build(name, c, log)
			},
		}
	}

	return nil
}

// newRouterSource builds the router instance called name, of whichever kind
// it is configured as.
func newRouterSource(cfg *config.Config, name string, log *slog.Logger) (plugin.Plugin, error) {
	instances, err := routerInstances(cfg)
	if err != nil {
		return nil, err
	}

	in, ok := instances[name]
	if !ok {
		return nil, fmt.Errorf("no source named %q is configured", name)
	}

	return in.build(log)
}

// hostDiscoverers returns the sources that can be asked which devices they
// know about. A source marked topology_only is a switch or access point, and
// is left out: the router above it lists the devices.
func hostDiscoverers(sources []plugin.Plugin) []plugin.HostDiscoverer {
	out := make([]plugin.HostDiscoverer, 0, len(sources))

	for _, p := range sources {
		if t, ok := p.(plugin.TopologyScoped); ok && t.IsTopologyOnly() {
			continue
		}

		if d, ok := p.(plugin.HostDiscoverer); ok {
			out = append(out, d)
		}
	}

	return out
}

// topologyReaders returns the sources that can say what is plugged into them:
// the router and each switch or access point alike.
func topologyReaders(sources []plugin.Plugin) []plugin.TopologyReader {
	out := make([]plugin.TopologyReader, 0, len(sources))

	for _, p := range sources {
		if r, ok := p.(plugin.TopologyReader); ok {
			out = append(out, r)
		}
	}

	return out
}

// newRouterOS builds one configured RouterOS source.
func newRouterOS(name string, cfg config.RouterOS, log *slog.Logger) (plugin.Plugin, error) {
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

	var opts []plugin.RouterOSOption
	if cfg.TopologyOnly {
		opts = append(opts, plugin.TopologyOnly())
	}

	p, err := plugin.NewRouterOS(name, client, log, opts...)
	if err != nil {
		return nil, fmt.Errorf("plugin routeros %q: %w", name, err)
	}

	return p, nil
}

// newOpenWrt builds one configured OpenWrt source.
func newOpenWrt(name string, cfg config.OpenWrt, log *slog.Logger) (plugin.Plugin, error) {
	client, err := openwrt.New(&openwrt.Config{
		Host:     cfg.Host,
		Port:     cfg.Port,
		User:     cfg.User,
		Password: cfg.Password,
		SSL:      cfg.SSL,
		Insecure: cfg.Insecure,
		Timeout:  cfg.Timeout,
	}, log)
	if err != nil {
		return nil, fmt.Errorf("plugin openwrt %q: %w", name, err)
	}

	var opts []plugin.OpenWrtOption
	if cfg.TopologyOnly {
		opts = append(opts, plugin.OpenWrtTopologyOnly())
	}

	p, err := plugin.NewOpenWrt(name, client, log, opts...)
	if err != nil {
		return nil, fmt.Errorf("plugin openwrt %q: %w", name, err)
	}

	return p, nil
}

// trafficReporters builds every enabled source that receives the flows a router
// exports, under the same rules as routerSources: off unless enabled, name
// order, and an entry that cannot be built is a config error.
func trafficReporters(ctx context.Context, cfg *config.Config, log *slog.Logger) ([]plugin.TrafficReporter, error) {
	names := slices.Sorted(maps.Keys(cfg.Plugins.NetFlow))
	out := make([]plugin.TrafficReporter, 0, len(names))

	for _, name := range names {
		nc := cfg.Plugins.NetFlow[name]
		if !nc.Enabled {
			log.InfoContext(ctx, "source is configured but not enabled", slog.String("src", name))

			continue
		}

		p, err := plugin.NewNetFlow(name, cmp.Or(nc.Listen, config.DefaultNetFlowListen), nc.Exporters, log)
		if err != nil {
			return nil, fmt.Errorf("plugin netflow %q: %w", name, err)
		}

		out = append(out, p)
	}

	return out, nil
}
