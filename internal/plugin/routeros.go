package plugin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// routerOSPrefix keeps an instance key from colliding with another kind
const routerOSPrefix = "routeros:"

// zeroMAC parses perfectly well and identifies nothing, so it must not become
// an identity two devices share.
const zeroMAC = "00:00:00:00:00:00"

// RouterOS reads the devices a MikroTik router knows about.
//
// It reads two tables because neither is sufficient: ARP resolves hardware
// across every VLAN but carries no names, and a lease carries a name but
// outlives the device it names.
type RouterOS struct {
	name   string
	client *routeros.RouterOS
	logger *slog.Logger

	// now is a field so tests can pin the timestamp their facts carry.
	now func() time.Time
}

// ErrNoInstanceName is refused rather than defaulted: the name is a database
// key, and one default would file two routers' facts under one source.
var ErrNoInstanceName = errors.New("plugin: routeros instance has no name")

// NewRouterOS builds the plugin for one router. name is the instance key from
// config -- "gateway", "switch_rack".
//
// It performs no I/O, which is why the name comes from config and not from
// /system/identity: a router that is down at startup is one to retry, not a
// reason to refuse to start.
func NewRouterOS(
	name string,
	client *routeros.RouterOS,
	log *slog.Logger,
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

	return r, nil
}

func (r *RouterOS) Name() string { return r.name }

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

	facts, err := r.build(ctx, c)
	if err != nil {
		errs = append(errs, err)
	}

	return facts, errors.Join(errs...)
}

// claimKey keys both tables the same way, so a lease's name reaches the ARP
// entry for the same device rather than arriving as a second claim that
// overwrites it with an empty name.
//
// The address is part of it because each address is separately current.
type claimKey struct {
	mac  string
	addr string
}

type claims map[claimKey]*draft

func draftFor(c claims, k claimKey) *draft {
	d, ok := c[k]
	if !ok {
		d = &draft{claimKey: k}
		c[k] = d
	}

	return d
}

// draft accumulates what both tables say about one device at one address.
type draft struct {
	claimKey

	present  bool
	hostname string
	nameFrom dbtype.HostnameSource
	detail   map[string]string
}

// set skips empty values: RouterOS renders an absent field as one, and a detail
// key with nothing behind it reads as the source having something to say.
func (d *draft) set(key, value string) {
	if value == "" {
		return
	}

	if d.detail == nil {
		d.detail = make(map[string]string, 4)
	}

	d.detail[key] = value
}

// collectARP reads the ARP table into c.
//
// Most of the table is not evidence of anything -- a live read returned 794
// rows of which 45 were usable, the rest being failed entries the router keeps
// for nearly every address it serves. Dropping them here is what stops the
// first run inventing 750 devices.
func (r *RouterOS) collectARP(ctx context.Context, c claims, entries []routeros.ARPEntry) {
	for _, e := range entries {
		mac, ok := normaliseMAC(e.MACAddress)
		if !ok {
			r.logger.DebugContext(ctx, "ignoring arp entry with an unusable hardware address",
				slog.String("address", e.Address),
				slog.String("mac", e.MACAddress),
			)

			continue
		}

		usable := e.Usable()

		// Identifies nothing and claims nothing.
		if mac == "" && !usable {
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
		d.present = d.present || usable

		d.set("interface", e.Interface)
		d.set("arp_status", e.Status)
		d.set("arp_dynamic", strconv.FormatBool(bool(e.Dynamic)))
	}
}

// collectLeases reads the DHCP lease table into c. Only a bound lease is a
// sighting: a static one can name something unplugged a month ago.
func (r *RouterOS) collectLeases(ctx context.Context, c claims, leases []routeros.DHCPLease) {
	for _, l := range leases {
		mac, ok := normaliseMAC(cmp.Or(l.ActiveMACAddress, l.MACAddress))
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

		// Configuration for an address, not a claim about a device.
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

			// Standing decides within one router's tables; the ladder across
			// sources is ingest's to apply.
			if d.hostname == "" || leaseRank(from) > leaseRank(d.nameFrom) {
				d.hostname = l.HostName
				d.nameFrom = from
			}
		}

		d.set("dhcp_server", l.Server)
		d.set("dhcp_status", l.Status)
		d.set("dhcp_dynamic", strconv.FormatBool(bool(l.Dynamic)))

		// A note, never a name: live tables read "My PC - Resolute 2.5g eth"
		// beside a host-name of "resolute".
		d.set("dhcp_comment", l.Comment)
	}
}

// build turns the merged drafts into facts.
//
// Name resolution stays off: a nameless row would otherwise come back carrying
// whatever this host's resolver said, filed under the router's claim.
func (r *RouterOS) build(ctx context.Context, c claims) ([]Fact, error) {
	seenAt := r.now()

	facts := make([]Fact, 0, len(c))

	var errs []error

	for _, d := range c {
		h, err := hosts.BuildHost(ctx, hosts.HostInput{
			IP:       d.addr,
			MAC:      d.mac,
			Hostname: d.hostname,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("build host %s: %w", d.addr, err))

			continue
		}

		facts = append(facts, Fact{
			Host:           h,
			Present:        d.present,
			HostnameSource: d.nameFrom,
			Detail:         d.detail,
			SeenAt:         seenAt,
		})
	}

	// Map iteration is randomised, and a run that reports its rows in a
	// different sequence every time cannot be diffed against the last one.
	slices.SortFunc(facts, func(a, b Fact) int {
		return cmp.Or(
			a.Host.Address().Compare(b.Host.Address()),
			strings.Compare(a.Host.MAC, b.Host.MAC),
		)
	})

	if len(errs) > 0 {
		r.logger.DebugContext(ctx, "some rows did not build", slog.Int("failed", len(errs)))
	}

	return facts, errors.Join(errs...)
}

// shareByDevice gives every draft for one device the name and detail that all
// of its addresses carry between them.
//
// A device holding two addresses is two facts, but the claim they write is
// keyed on the device alone, so the last one to land decides what the claim
// says. Without this a nameless address clears the name another found, and a
// thinner detail map overwrites a richer one -- bespin loses its lease comment
// to a stale ARP entry for an address it has already given up.
func shareByDevice(c claims) {
	// Sorted, so a key two addresses disagree on resolves the same way every
	// run rather than however the map happened to iterate.
	keys := slices.SortedFunc(maps.Keys(c), func(a, b claimKey) int {
		return cmp.Or(strings.Compare(a.addr, b.addr), strings.Compare(a.mac, b.mac))
	})

	names := make(map[string]*draft, len(c))
	details := make(map[string]map[string]string, len(c))

	for _, k := range keys {
		d := c[k]
		if d.mac == "" {
			continue
		}

		if d.hostname != "" {
			if best, ok := names[d.mac]; !ok || leaseRank(d.nameFrom) > leaseRank(best.nameFrom) {
				names[d.mac] = d
			}
		}

		merged, ok := details[d.mac]
		if !ok {
			merged = make(map[string]string, len(d.detail))
			details[d.mac] = merged
		}

		// Union rather than overwrite: detail is per address and the claim is
		// per device, so only the union loses nothing. Where two addresses
		// disagree the lower one wins, which is arbitrary but stable.
		for key, value := range d.detail {
			if _, seen := merged[key]; !seen {
				merged[key] = value
			}
		}
	}

	for _, d := range c {
		if best, ok := names[d.mac]; ok {
			d.hostname = best.hostname
			d.nameFrom = best.nameFrom
		}

		// Cloned, so two facts sharing a device do not share a mutable map.
		if merged, ok := details[d.mac]; ok {
			d.detail = maps.Clone(merged)
		}
	}
}

// leaseRank orders the two standings a lease's name can carry. It is local to
// one router's tables; the ladder across sources lives in ingest.
func leaseRank(s dbtype.HostnameSource) int {
	switch s {
	case dbtype.HostnameFromDHCPStatic:
		return 2
	case dbtype.HostnameFromDHCPLease:
		return 1
	default:
		return 0
	}
}

// normaliseMAC renders a hardware address so both tables key on it alike.
//
// Carrying none is not an error -- an incomplete ARP entry has no mac-address
// member at all -- but carrying something that is not one means drop the row.
func normaliseMAC(s string) (mac string, ok bool) {
	if s == "" {
		return "", true
	}

	hw, err := net.ParseMAC(s)
	if err != nil {
		return "", false
	}

	if hw.String() == zeroMAC {
		return "", true
	}

	return hw.String(), true
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
// needs a human. ErrNotFound stays unmapped -- it is neither.
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
)
