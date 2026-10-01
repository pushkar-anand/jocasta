package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// routerOSPrefix namespaces an instance key so it cannot collide with another
// source kind's.
const routerOSPrefix = "routeros:"

// RouterOS reads the devices a MikroTik router knows about.
//
// It reads two tables because neither is sufficient: ARP resolves hardware
// across every VLAN but carries no names, and a lease carries a name but
// outlives the device it names.
type RouterOS struct {
	name   string
	client *routeros.RouterOS
	logger *slog.Logger

	// topologyOnly marks a switch or access point read for its switching
	// tables alone. The router that routes the network is the one without it.
	topologyOnly bool

	// now is a field so tests can pin the timestamp their facts carry.
	now func() time.Time
}

// RouterOSOption configures a RouterOS source.
type RouterOSOption func(*RouterOS)

// TopologyOnly marks the source as a switch or access point: it is read for
// what is plugged into it, and is not the device the network hangs from.
func TopologyOnly() RouterOSOption {
	return func(r *RouterOS) { r.topologyOnly = true }
}

// IsTopologyOnly reports whether the source is read for its switching tables
// alone, which is why it is left out of device discovery.
func (r *RouterOS) IsTopologyOnly() bool { return r.topologyOnly }

// ErrNoInstanceName refuses a missing name: the name is a database key, and
// one default would file two routers' facts under one source.
var ErrNoInstanceName = errors.New("plugin: routeros instance has no name")

// NewRouterOS builds the plugin for one router. name is the instance key from
// config, such as "gateway" or "switch_rack".
//
// It performs no I/O, which is why the name comes from config: reading
// /system/identity would need the router up, and a router that is down at
// startup is retried later while the server still starts.
func NewRouterOS(
	name string,
	client *routeros.RouterOS,
	log *slog.Logger,
	opts ...RouterOSOption,
) (*RouterOS, error) {
	if name == "" {
		return nil, ErrNoInstanceName
	}

	if client == nil {
		return nil, fmt.Errorf("plugin: routeros %q has no client", name)
	}

	if log == nil {
		log = slog.Default()
	}

	r := &RouterOS{
		name:   routerOSPrefix + name,
		client: client,
		logger: log.With(slog.String("plugin", routerOSPrefix+name)),
		now:    time.Now,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

// Name returns the plugin's configured name.
func (r *RouterOS) Name() string { return r.name }

// Kind identifies this plugin as a RouterOS source.
func (r *RouterOS) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

// Discover merges both tables into one fact per device per address. One table
// failing does not cost the other, so a partial read returns its facts
// alongside the error.
func (r *RouterOS) Discover(ctx context.Context) ([]Fact, error) {
	var errs []error

	c := make(claims)

	arp, err := r.client.ARP(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read arp table: %w", classifyRouterOS(err)))
	} else {
		r.collectARP(ctx, c, arp)
	}

	leases, err := r.client.DHCPLeases(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read dhcp leases: %w", classifyRouterOS(err)))
	} else {
		r.collectLeases(ctx, c, leases)
	}

	shareByDevice(c)

	facts, err := buildFacts(ctx, r.logger, r.now(), c)
	if err != nil {
		errs = append(errs, err)
	}

	return facts, errors.Join(errs...)
}

// collectARP reads the ARP table into c.
//
// Most of the table is not evidence of anything: the router keeps a failed
// entry for very nearly every address in every subnet it serves, so the usable
// rows are a small fraction of it. Dropping the rest here is what stops the
// first run inventing a device per address.
func (r *RouterOS) collectARP(ctx context.Context, c claims, entries []routeros.ARPEntry) {
	for _, e := range entries {
		mac, ok := hosts.CanonicalMAC(e.MACAddress)
		if !ok {
			r.logger.DebugContext(ctx, "ignoring arp entry with an unusable hardware address",
				slog.String("address", e.Address),
				slog.String("mac", e.MACAddress),
			)

			continue
		}

		// Identifies nothing and claims nothing.
		if mac == "" && !e.Usable() {
			continue
		}

		addr, ok := normaliseAddr(e.Address)
		if !ok {
			r.logger.DebugContext(ctx, "ignoring arp entry with an unusable address",
				slog.String("address", e.Address),
			)

			continue
		}

		d := draftFor(c, claimKey{mac: mac, addr: addr})

		// A resolved entry keeps the device in the inventory; only one the
		// router has actually heard from lately makes it present. A "stale"
		// entry is only the router remembering a hardware address.
		d.present = d.present || e.Reachable()

		d.set("interface", e.Interface)
		d.set("arp_status", e.Status)
		d.set("arp_dynamic", strconv.FormatBool(bool(e.Dynamic)))
	}
}

// collectLeases reads the DHCP lease table into c. Only a bound lease is a
// sighting: a static one can name something unplugged a month ago.
func (r *RouterOS) collectLeases(ctx context.Context, c claims, leases []routeros.DHCPLease) {
	for _, l := range leases {
		mac, ok := hosts.CanonicalMAC(cmp.Or(l.ActiveMACAddress, l.MACAddress))
		if !ok {
			r.logger.DebugContext(ctx, "ignoring lease with an unusable hardware address",
				slog.String("address", l.Address),
				slog.String("mac", cmp.Or(l.ActiveMACAddress, l.MACAddress)),
			)

			continue
		}

		addr, ok := normaliseAddr(cmp.Or(l.ActiveAddress, l.Address))
		if !ok {
			r.logger.DebugContext(ctx, "ignoring lease with an unusable address",
				slog.String("address", l.Address),
			)

			continue
		}

		// Configuration for an address, which claims nothing about a device.
		if mac == "" && l.HostName == "" {
			continue
		}

		d := draftFor(c, claimKey{mac: mac, addr: addr})
		d.present = d.present || l.Bound()

		if l.HostName != "" {
			from := dbtype.HostnameFromDHCPLease
			if l.Static() {
				from = dbtype.HostnameFromDHCPStatic
			}

			// Standing decides between this router's own leases; ingest
			// applies the same ladder across sources.
			if d.hostname == "" || from.Rank() > d.nameFrom.Rank() {
				d.hostname = l.HostName
				d.nameFrom = from
			}
		}

		d.set("dhcp_server", l.Server)
		d.set("dhcp_status", l.Status)
		d.set("dhcp_dynamic", strconv.FormatBool(bool(l.Dynamic)))

		// A note, which is never used as a name: tables read
		// "Workstation - wired" beside a host-name of "workstation".
		d.set("dhcp_comment", l.Comment)
	}
}

// normaliseAddr renders an address so a lease and an ARP entry for the same one
// meet at the same key.
func normaliseAddr(s string) (addr string, ok bool) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", false
	}

	return a.String(), true
}

// classifyRouterOS maps the client's errors onto this package's, so nothing
// above has to import pkg/routeros to tell a retryable failure from one that
// needs a human. ErrNotFound stays unmapped, because it is neither.
func classifyRouterOS(err error) error {
	switch {
	case errors.Is(err, routeros.ErrUnauthorized):
		return fmt.Errorf("%w: %w", ErrAuth, err)
	case errors.Is(err, routeros.ErrUnreachable), errors.Is(err, routeros.ErrTLS):
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	default:
		return err
	}
}

var (
	_ Plugin         = (*RouterOS)(nil)
	_ HostDiscoverer = (*RouterOS)(nil)
	_ TopologyScoped = (*RouterOS)(nil)
)
