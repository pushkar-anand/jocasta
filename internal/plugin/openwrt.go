package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/pkg/openwrt"
)

// openWrtPrefix puts an instance key in a namespace. So it cannot collide
// with the key of a different source kind.
const openWrtPrefix = "openwrt:"

// OpenWrt reads the devices that an OpenWrt router knows.
//
// It reads three tables, because no single table is enough. The neighbour
// table resolves hardware across every segment, but has no names. A lease has
// a name, but stays after its device goes. A static host is the name that an
// operator chose. Only this table tells that. None of them lists the router
// itself. So the plugin also reads its interfaces.
type OpenWrt struct {
	name   string
	client *openwrt.OpenWrt
	logger *slog.Logger

	// topologyOnly marks an access point or switch that the plugin reads only
	// for its switching tables. The router of the network does not have it.
	topologyOnly bool

	// now is a field, so tests can fix the timestamp of their facts.
	now func() time.Time
}

// OpenWrtOption configures an OpenWrt source.
type OpenWrtOption func(*OpenWrt)

// OpenWrtTopologyOnly marks the source as an access point or switch. Jocasta
// reads it for what is connected to it. The network does not hang from it.
func OpenWrtTopologyOnly() OpenWrtOption {
	return func(o *OpenWrt) { o.topologyOnly = true }
}

// IsTopologyOnly reports whether Jocasta reads the source only for its
// switching tables. If so, device discovery leaves it out.
func (o *OpenWrt) IsTopologyOnly() bool { return o.topologyOnly }

// ErrNoOpenWrtName refuses a missing name. The name is a database key. With one
// default, the facts of two routers would go under one source.
var ErrNoOpenWrtName = errors.New("plugin: openwrt instance has no name")

// NewOpenWrt builds the plugin for one router. name is the instance key from
// config, such as "gateway".
//
// It does no I/O. So if the router is down at startup, the server still starts
// and tries again later.
func NewOpenWrt(name string, client *openwrt.OpenWrt, log *slog.Logger, opts ...OpenWrtOption) (*OpenWrt, error) {
	if name == "" {
		return nil, ErrNoOpenWrtName
	}

	if client == nil {
		return nil, fmt.Errorf("plugin: openwrt %q has no client", name)
	}

	if log == nil {
		log = slog.Default()
	}

	o := &OpenWrt{
		name:   openWrtPrefix + name,
		client: client,
		logger: log.With(slog.String("plugin", openWrtPrefix+name)),
		now:    time.Now,
	}

	for _, opt := range opts {
		opt(o)
	}

	return o, nil
}

// Name returns the configured name of the plugin.
func (o *OpenWrt) Name() string { return o.name }

// Kind identifies this plugin as a router source.
func (o *OpenWrt) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

// Discover merges the three tables and the own addresses of the router into
// one fact for each device and address.
func (o *OpenWrt) Discover(ctx context.Context) ([]Fact, error) {
	// Read the static hosts first, because they decide the standing of the
	// names of the leases.
	static, err := o.client.StaticHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read static hosts: %w", classifyOpenWrt(err))
	}

	neigh, err := o.client.Neighbours(ctx)
	if err != nil {
		return nil, fmt.Errorf("read neighbours: %w", classifyOpenWrt(err))
	}

	leases, err := o.client.DHCPLeases(ctx)
	if err != nil {
		return nil, fmt.Errorf("read dhcp leases: %w", classifyOpenWrt(err))
	}

	// The own addresses of the router: its interfaces and the devices under
	// them.
	ifaces, err := o.client.Interfaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("read interfaces: %w", classifyOpenWrt(err))
	}

	devices, err := o.client.NetworkDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("read network devices: %w", classifyOpenWrt(err))
	}

	return o.facts(ctx, static, neigh, leases, ifaces, devices)
}

// facts merges what the three tables said, and the own addresses of the
// router, into one fact for each device and address.
func (o *OpenWrt) facts(
	ctx context.Context,
	static []openwrt.StaticHost,
	neigh []openwrt.Neighbour,
	leases []openwrt.Lease,
	ifaces []openwrt.Interface,
	devices map[string]openwrt.NetDevice,
) ([]Fact, error) {
	c := make(claims)
	named := staticNames(static)

	o.collectNeighbours(ctx, c, neigh)
	o.collectLeases(ctx, c, leases, named)
	o.collectStatic(ctx, c, static)
	o.collectOwn(c, ifaces, devices)

	nameStatic(c, named)

	return buildFacts(ctx, o.logger, o.now(), c)
}

// collectNeighbours reads the neighbour tables into c.
//
// An entry that never resolved names nothing. Every device has a link-local
// IPv6 address on every segment, and a sweep never records it. The plugin
// drops both.
func (o *OpenWrt) collectNeighbours(ctx context.Context, c claims, entries []openwrt.Neighbour) {
	for _, e := range entries {
		if !e.Usable() {
			continue
		}

		mac, ok := hosts.CanonicalMAC(e.MAC)
		if !ok || mac == "" {
			o.logger.DebugContext(ctx, "ignoring a neighbour with an unusable hardware address",
				slog.String("address", e.Address),
				slog.String("mac", e.MAC),
			)

			continue
		}

		addr, ok := deviceAddr(e.Address)
		if !ok {
			continue
		}

		d := draftFor(c, claimKey{mac: mac, addr: addr})

		// A reachable entry means that the router heard from the device
		// recently. A stale entry keeps the device in the inventory, but does
		// not make it show as online.
		d.present = d.present || e.Reachable()

		d.set("interface", e.Device)

		// Lowercase, as RouterOS writes its ARP status.
		d.set("neigh_state", strings.ToLower(e.State))
	}
}

// collectLeases reads the active leases into c. dnsmasq and odhcpd list only
// leases that a client holds now. So each lease is a sighting.
func (o *OpenWrt) collectLeases(ctx context.Context, c claims, leases []openwrt.Lease, named map[string]string) {
	for _, l := range leases {
		mac, ok := hosts.CanonicalMAC(l.MAC)
		if !ok || mac == "" {
			// Without a hardware address, the lease cannot match a device. This
			// happens to an IPv6 lease whose client built its DUID in a
			// different way.
			o.logger.DebugContext(ctx, "ignoring a lease with no usable hardware address",
				slog.String("address", l.Address),
				slog.String("mac", l.MAC),
			)

			continue
		}

		addr, ok := deviceAddr(l.Address)
		if !ok {
			continue
		}

		d := draftFor(c, claimKey{mac: mac, addr: addr})
		d.present = true

		_, static := named[mac]
		d.set("dhcp_dynamic", strconv.FormatBool(!static))

		// nameStatic applies the static name, if there is one. dnsmasq also
		// reports it as the hostname of the lease. But a lease alone cannot
		// tell whether the name came from the operator or the device.
		if l.Hostname != "" && d.hostname == "" {
			d.hostname = l.Hostname
			d.nameFrom = dbtype.HostnameFromDHCPLease
		}
	}
}

// collectStatic records each static host that reserves an address as a claim
// that is not present. Configuration names a device, whether it is connected
// or not.
func (o *OpenWrt) collectStatic(ctx context.Context, c claims, static []openwrt.StaticHost) {
	for _, h := range static {
		addr, ok := deviceAddr(h.Address)
		if !ok {
			continue
		}

		for _, raw := range h.MACs {
			mac, ok := hosts.CanonicalMAC(raw)
			if !ok || mac == "" {
				o.logger.DebugContext(ctx, "ignoring a static host with an unusable hardware address",
					slog.String("name", h.Name),
					slog.String("mac", raw),
				)

				continue
			}

			d := draftFor(c, claimKey{mac: mac, addr: addr})
			d.set("dhcp_dynamic", "false")
		}
	}
}

// collectOwn pairs each address on which the router serves a segment with the
// hardware address of its device. None of the three tables lists the router
// itself. A sweep that reaches it through a different segment sees an address
// with no hardware. So without these, the router would be one device for each
// segment that it serves. The router answered this read, so each is present.
// An interface that is down has no address. An interface that is not static,
// such as a dhcp or pppoe uplink, is on a link that the router does not
// serve. collectOwn leaves out both, as buildOpenWrtNetworks does.
func (o *OpenWrt) collectOwn(c claims, ifaces []openwrt.Interface, devices map[string]openwrt.NetDevice) {
	for _, i := range ifaces {
		if !i.Up || i.Proto != openwrt.ProtoStatic {
			continue
		}

		// First the device that has the addresses. The configured device
		// covers a router that reports no l3_device.
		dev := cmp.Or(i.L3Device, i.Device)

		mac, ok := hosts.CanonicalMAC(devices[dev].MAC)
		if !ok || mac == "" {
			continue
		}

		for _, a := range slices.Concat(i.IPv4, i.IPv6) {
			addr, ok := deviceAddr(a.Address)
			if !ok {
				continue
			}

			d := draftFor(c, claimKey{mac: mac, addr: addr})
			d.present = true

			// The device, with the name of a neighbour entry.
			d.set("interface", dev)
		}
	}
}

// staticNames maps each hardware address that a static host names to that
// name. If two hosts name one address, the first in configuration order wins,
// as in dnsmasq.
func staticNames(static []openwrt.StaticHost) map[string]string {
	out := make(map[string]string)

	for _, h := range static {
		if h.Name == "" {
			continue
		}

		for _, raw := range h.MACs {
			mac, ok := hosts.CanonicalMAC(raw)
			if !ok || mac == "" {
				continue
			}

			if _, seen := out[mac]; !seen {
				out[mac] = h.Name
			}
		}
	}

	return out
}

// nameStatic gives the static name to every draft for a hardware address that
// a static host names. The name gets the standing of a choice of an
// operator.
func nameStatic(c claims, named map[string]string) {
	for _, d := range c {
		if name, ok := named[d.mac]; ok {
			d.hostname = name
			d.nameFrom = dbtype.HostnameFromDHCPStatic
		}
	}
}

// deviceAddr renders an address that a device can have. So a lease and a
// neighbour entry for the same address get the same key. It refuses
// link-local, loopback and multicast addresses. The inventory records no
// device at such an address.
func deviceAddr(s string) (string, bool) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", false
	}

	a = a.Unmap()

	if a.IsLinkLocalUnicast() || a.IsLoopback() || a.IsMulticast() || a.IsUnspecified() {
		return "", false
	}

	return a.String(), true
}

// classifyOpenWrt maps the errors of the client to the errors of this package.
// So no caller needs to import pkg/openwrt to tell a failure that a retry can
// fix from one that needs a person. ErrNotFound stays unmapped, because it is
// neither. Usually, it is a package that the router does not have, such as
// rpcd-mod-luci. Its own message names the missing object.
func classifyOpenWrt(err error) error {
	switch {
	case errors.Is(err, openwrt.ErrUnauthorized):
		return fmt.Errorf("%w: %w", ErrAuth, err)
	case errors.Is(err, openwrt.ErrUnreachable), errors.Is(err, openwrt.ErrTLS):
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	default:
		return err
	}
}

var (
	_ Plugin         = (*OpenWrt)(nil)
	_ HostDiscoverer = (*OpenWrt)(nil)
	_ TopologyScoped = (*OpenWrt)(nil)
)
