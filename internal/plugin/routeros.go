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

// routerOSPrefix puts an instance key in a namespace. So it cannot collide
// with the key of a different source kind.
const routerOSPrefix = "routeros:"

// RouterOS reads the devices that a MikroTik router knows.
//
// It reads two tables, because neither is enough. ARP resolves hardware across
// every VLAN, but has no names. A lease has a name, but stays after its device
// goes.
type RouterOS struct {
	name   string
	client *routeros.RouterOS
	logger *slog.Logger

	// topologyOnly marks a switch or access point that the plugin reads only
	// for its switching tables. The router of the network does not have it.
	topologyOnly bool

	// now is a field, so tests can fix the timestamp of their facts.
	now func() time.Time
}

// RouterOSOption configures a RouterOS source.
type RouterOSOption func(*RouterOS)

// TopologyOnly marks the source as a switch or access point. Jocasta reads it
// for what is connected to it. The network does not hang from it.
func TopologyOnly() RouterOSOption {
	return func(r *RouterOS) { r.topologyOnly = true }
}

// IsTopologyOnly reports whether Jocasta reads the source only for its
// switching tables. If so, device discovery leaves it out.
func (r *RouterOS) IsTopologyOnly() bool { return r.topologyOnly }

// ErrNoInstanceName refuses a missing name. The name is a database key. With
// one default, the facts of two routers would go under one source.
var ErrNoInstanceName = errors.New("plugin: routeros instance has no name")

// NewRouterOS builds the plugin for one router. name is the instance key from
// config, such as "gateway" or "switch_rack".
//
// It does no I/O, so the name comes from config. A read of /system/identity
// would need the router to be up. If the router is down at startup, the
// server still starts and tries again later.
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

// Name returns the configured name of the plugin.
func (r *RouterOS) Name() string { return r.name }

// Kind identifies this plugin as a RouterOS source.
func (r *RouterOS) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

// Discover merges the tables into one fact for each device and address.
func (r *RouterOS) Discover(ctx context.Context) ([]Fact, error) {
	c := make(claims)

	arp, err := r.client.ARP(ctx)
	if err != nil {
		return nil, fmt.Errorf("read arp table: %w", classifyRouterOS(err))
	}

	r.collectARP(ctx, c, arp)

	leases, err := r.client.DHCPLeases(ctx)
	if err != nil {
		return nil, fmt.Errorf("read dhcp leases: %w", classifyRouterOS(err))
	}

	r.collectLeases(ctx, c, leases)

	if err := r.readOwn(ctx, c); err != nil {
		return nil, err
	}

	return buildFacts(ctx, r.logger, r.now(), c)
}

// collectARP reads the ARP table into c.
//
// Most of the table proves nothing. The router keeps a failed entry for almost
// every address in every subnet that it serves. So the usable rows are a
// small part of it. collectARP drops the rest. This stops the first run from
// making up a device for each address.
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

		// This identifies nothing and claims nothing.
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

		// A resolved entry keeps the device in the inventory. Only an entry
		// from which the router really heard recently makes it present. In a
		// "stale" entry, the router only remembers a hardware address.
		d.present = d.present || e.Reachable()

		d.set("interface", e.Interface)
		d.set("arp_status", e.Status)
		d.set("arp_dynamic", strconv.FormatBool(bool(e.Dynamic)))
	}
}

// collectLeases reads the DHCP lease table into c. Only a bound lease is a
// sighting. A static lease can name a device that was disconnected a month
// ago.
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

		// Configuration for an address. It claims nothing about a device.
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

			// Standing decides between the leases of this router. Ingest uses
			// the same ladder across sources.
			if d.hostname == "" || from.Rank() > d.nameFrom.Rank() {
				d.hostname = l.HostName
				d.nameFrom = from
			}
		}

		d.set("dhcp_server", l.Server)
		d.set("dhcp_status", l.Status)
		d.set("dhcp_dynamic", strconv.FormatBool(bool(l.Dynamic)))

		// A note. It is never a name. Tables have "Workstation - wired" next
		// to a host-name of "workstation".
		d.set("dhcp_comment", l.Comment)
	}
}

// readOwn reads the own addresses of the router, and the interfaces behind
// them, into c. Neither the ARP table nor the leases list them. A sweep that
// reaches the router through a different segment sees an address with no
// hardware. So without these, the router would be one device for each segment
// that it serves.
func (r *RouterOS) readOwn(ctx context.Context, c claims) error {
	addrs, err := r.client.Addresses(ctx)
	if err != nil {
		return fmt.Errorf("read addresses: %w", classifyRouterOS(err))
	}

	ifaces, err := r.client.Interfaces(ctx)
	if err != nil {
		return fmt.Errorf("read interfaces: %w", classifyRouterOS(err))
	}

	r.collectOwn(ctx, c, addrs, ifaces)

	return nil
}

// collectOwn pairs each address on which the router serves a segment with the
// hardware address of its interface. The router answered this read, so each
// is present. A dynamic address, such as the WAN lease, is on a link that the
// router does not serve. collectOwn leaves it out.
func (r *RouterOS) collectOwn(ctx context.Context, c claims, addrs []routeros.IPAddress, ifaces []routeros.Interface) {
	macs := make(map[string]string, len(ifaces))

	for _, i := range ifaces {
		if mac, ok := hosts.CanonicalMAC(i.MACAddress); ok && mac != "" {
			macs[i.Name] = mac
		}
	}

	for _, a := range addrs {
		if !a.Usable() {
			continue
		}

		// The configured name first, as in lookupVLAN. The resolved name covers
		// an address on a bridge port.
		mac := cmp.Or(macs[a.Interface], macs[a.ActualInterface])
		if mac == "" {
			continue
		}

		p, err := netip.ParsePrefix(a.Address)
		if err != nil {
			r.logger.DebugContext(ctx, "ignoring an own address that does not parse",
				slog.String("address", a.Address),
			)

			continue
		}

		d := draftFor(c, claimKey{mac: mac, addr: p.Addr().String()})
		d.present = true
		d.set("interface", a.Interface)
	}
}

// normaliseAddr renders an address. So a lease and an ARP entry for the same
// address get the same key.
func normaliseAddr(s string) (addr string, ok bool) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", false
	}

	return a.String(), true
}

// classifyRouterOS maps the errors of the client to the errors of this
// package. So no caller needs to import pkg/routeros to tell a failure that a
// retry can fix from one that needs a person. ErrNotFound stays unmapped,
// because it is neither.
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
