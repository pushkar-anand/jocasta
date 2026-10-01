package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/pkg/openwrt"
)

// openWrtPrefix namespaces an instance key so it cannot collide with another
// source kind's.
const openWrtPrefix = "openwrt:"

// OpenWrt reads the devices an OpenWrt router knows about.
//
// It reads three tables because no one of them is enough. The neighbour table
// resolves hardware across every segment but carries no names. A lease carries
// a name but outlives the device it names. A static host is the name an
// operator chose, and is the only one of the three that says so.
type OpenWrt struct {
	name   string
	client *openwrt.OpenWrt
	logger *slog.Logger

	// now is a field so tests can pin the timestamp their facts carry.
	now func() time.Time
}

// ErrNoOpenWrtName refuses a missing name: the name is a database key, and one
// default would file two routers' facts under one source.
var ErrNoOpenWrtName = errors.New("plugin: openwrt instance has no name")

// NewOpenWrt builds the plugin for one router. name is the instance key from
// config, such as "gateway".
//
// It performs no I/O, so a router that is down at startup is retried later
// while the server still starts.
func NewOpenWrt(name string, client *openwrt.OpenWrt, log *slog.Logger) (*OpenWrt, error) {
	if name == "" {
		return nil, ErrNoOpenWrtName
	}

	if client == nil {
		return nil, fmt.Errorf("plugin: openwrt %q has no client", name)
	}

	if log == nil {
		log = slog.Default()
	}

	return &OpenWrt{
		name:   openWrtPrefix + name,
		client: client,
		logger: log.With(slog.String("plugin", openWrtPrefix+name)),
		now:    time.Now,
	}, nil
}

// Name returns the plugin's configured name.
func (o *OpenWrt) Name() string { return o.name }

// Kind identifies this plugin as a router source.
func (o *OpenWrt) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

// Discover merges the three tables into one fact per device per address. One
// table failing does not cost the others, so a partial read returns its facts
// alongside the error.
func (o *OpenWrt) Discover(ctx context.Context) ([]Fact, error) {
	var errs []error

	// The static hosts are read first, since they decide the standing of the
	// names the leases carry.
	static, err := o.client.StaticHosts(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read static hosts: %w", classifyOpenWrt(err)))
	}

	neigh, err := o.client.Neighbours(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read neighbours: %w", classifyOpenWrt(err)))
	}

	leases, err := o.client.DHCPLeases(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read dhcp leases: %w", classifyOpenWrt(err)))
	}

	facts, err := o.facts(ctx, static, neigh, leases)
	if err != nil {
		errs = append(errs, err)
	}

	return facts, errors.Join(errs...)
}

// facts merges what the three tables said into one fact per device per
// address.
func (o *OpenWrt) facts(
	ctx context.Context,
	static []openwrt.StaticHost,
	neigh []openwrt.Neighbour,
	leases []openwrt.Lease,
) ([]Fact, error) {
	c := make(claims)
	named := staticNames(static)

	o.collectNeighbours(ctx, c, neigh)
	o.collectLeases(ctx, c, leases, named)
	o.collectStatic(ctx, c, static)

	nameStatic(c, named)
	shareByDevice(c)

	return buildFacts(ctx, o.logger, o.now(), c)
}

// collectNeighbours reads the neighbour tables into c.
//
// An entry that never resolved names nothing, and a link-local IPv6 address is
// one every device holds on every segment, which a sweep never records. Both
// are dropped.
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

		// A reachable entry is the router having heard from the device
		// lately. A stale one keeps the device in the inventory without
		// making it read as online.
		d.present = d.present || e.Reachable()

		d.set("interface", e.Device)

		// Lowercase, as RouterOS words its ARP status.
		d.set("neigh_state", strings.ToLower(e.State))
	}
}

// collectLeases reads the active leases into c. dnsmasq and odhcpd list only
// leases a client holds now, so every one is a sighting.
func (o *OpenWrt) collectLeases(ctx context.Context, c claims, leases []openwrt.Lease, named map[string]string) {
	for _, l := range leases {
		mac, ok := hosts.CanonicalMAC(l.MAC)
		if !ok || mac == "" {
			// Without a hardware address the lease cannot be matched to a
			// device, which happens to an IPv6 lease whose client built its
			// DUID some other way.
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

		// The static name, when there is one, is applied by nameStatic. dnsmasq
		// reports it as the lease's hostname too, but a lease alone cannot say
		// whether the name came from the operator or the device.
		if l.Hostname != "" && d.hostname == "" {
			d.hostname = l.Hostname
			d.nameFrom = dbtype.HostnameFromDHCPLease
		}
	}
}

// collectStatic files each static host that reserves an address as a claim
// that is not present: configuration names a device whether or not it is
// plugged in.
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

// staticNames maps each hardware address a static host names to that name.
// Where two hosts name one address, the first in configuration order wins,
// as it does in dnsmasq.
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

// nameStatic gives every draft for a hardware address a static host names that
// name, at the standing an operator's choice has.
func nameStatic(c claims, named map[string]string) {
	for _, d := range c {
		if name, ok := named[d.mac]; ok {
			d.hostname = name
			d.nameFrom = dbtype.HostnameFromDHCPStatic
		}
	}
}

// deviceAddr renders an address a device can hold, so a lease and a neighbour
// entry for the same one meet at the same key. Link-local, loopback and
// multicast addresses are refused: none is an address a device is inventoried
// at.
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

// classifyOpenWrt maps the client's errors onto this package's, so nothing
// above has to import pkg/openwrt to tell a retryable failure from one that
// needs a human. ErrNotFound stays unmapped, because it is neither: it is
// most often a package the router lacks, such as rpcd-mod-luci, and its own
// message says which object is missing.
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
)
