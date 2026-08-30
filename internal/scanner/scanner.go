// Package scanner discovers hosts on a network by sweeping an address range with
// ICMP echo requests and enriching whatever answers with a MAC address and a
// hostname.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/netip"
	"slices"
	"time"

	"github.com/pushkar-anand/jocasta/pkg/cidr"
	"github.com/pushkar-anand/jocasta/pkg/oui"
)

// ErrPrefixTooLarge is returned for a range wide enough that sweeping it would
// be a mistake rather than an intent.
var ErrPrefixTooLarge = errors.New("prefix too large to sweep")

// maxSweepHosts caps a single sweep. A /16 is 65k probes, which is already
// slow and loud; anything wider is almost certainly a typo'd prefix. The cap is
// this scanner's policy, which is why it lives here and not in pkg/cidr.
const maxSweepHosts = 65536

// Host is an address that answered a sweep, with whatever identifying detail
// could be resolved for it.
type Host struct {
	Addr     netip.Addr    `json:"addr"`
	MAC      string        `json:"mac,omitempty"`
	Hostname string        `json:"hostname,omitempty"`
	RTT      time.Duration `json:"rtt"`
	SeenAt   time.Time     `json:"seen_at"`

	// Vendor is the organisation that registered the MAC address, where one
	// is known. Randomised marks an address the device generated for itself,
	// which belongs to no vendor and identifies no hardware.
	Vendor     string `json:"vendor,omitempty"`
	Randomised bool   `json:"randomised,omitempty"`

	// Self marks an address belonging to the host running the scan, and
	// Interface names the interface holding it. Both are empty for every other
	// host, whose interfaces are not visible from here.
	Self      bool   `json:"self,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// Scanner sweeps address ranges. It holds no per-scan state, so a single
// instance is safe for concurrent use.
type Scanner struct {
	log *slog.Logger

	// rounds is how many times an address is probed before it is called dead.
	// A single dropped packet on a busy wireless VLAN should not retire a host.
	rounds int

	// wait is how long to keep reading replies after the final probe is sent.
	// Cheap IoT devices can take over a second to answer, so a short window
	// reports them as down rather than slow.
	wait time.Duration

	// rate caps probes per second so a large sweep does not arrive as a burst
	// that a switch or a cheap IoT device treats as a flood.
	rate int

	resolveNames bool
	resolveMACs  bool
}

// Option configures a Scanner.
type Option func(*Scanner)

// WithRounds sets how many probes an address gets before it is considered down.
func WithRounds(n int) Option {
	return func(s *Scanner) {
		if n > 0 {
			s.rounds = n
		}
	}
}

// WithWait sets how long to listen for replies after the last probe is sent.
func WithWait(d time.Duration) Option {
	return func(s *Scanner) {
		if d > 0 {
			s.wait = d
		}
	}
}

// WithRate sets the ceiling on probes sent per second.
func WithRate(n int) Option {
	return func(s *Scanner) {
		if n > 0 {
			s.rate = n
		}
	}
}

// WithNameResolution controls reverse-DNS lookups for hosts that answered.
func WithNameResolution(v bool) Option {
	return func(s *Scanner) { s.resolveNames = v }
}

// WithMACResolution controls ARP-table lookups for hosts that answered.
func WithMACResolution(v bool) Option {
	return func(s *Scanner) { s.resolveMACs = v }
}

// New builds a Scanner with defaults tuned for a home LAN: fast enough to sweep
// a /24 in a couple of seconds, gentle enough not to upset IoT firmware.
func New(log *slog.Logger, opts ...Option) *Scanner {
	s := &Scanner{
		log:          log,
		rounds:       2,
		wait:         2 * time.Second,
		rate:         1000,
		resolveNames: true,
		resolveMACs:  true,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Scan sweeps every usable address in p and returns the hosts that answered,
// ordered by address. An empty result is a valid answer, not an error.
func (s *Scanner) Scan(ctx context.Context, p netip.Prefix) ([]Host, error) {
	count, err := cidr.Count(p)
	if err != nil {
		return nil, err
	}

	if count > maxSweepHosts {
		return nil, fmt.Errorf("%w: %s has %d addresses (max %d)", ErrPrefixTooLarge, p, count, maxSweepHosts)
	}

	targets, err := cidr.Hosts(p)
	if err != nil {
		return nil, err
	}

	s.log.DebugContext(ctx, "starting sweep",
		slog.String("prefix", p.String()),
		slog.Int("targets", count),
	)

	replies, err := sweep(ctx, s.log, targets, count, s.rounds, s.wait, s.rate)
	if err != nil {
		return nil, fmt.Errorf("sweep %s: %w", p, err)
	}

	now := time.Now()
	ordered := slices.SortedFunc(maps.Keys(replies), netip.Addr.Compare)
	hosts := make([]Host, 0, len(ordered))

	for _, addr := range ordered {
		hosts = append(hosts, Host{Addr: addr, RTT: replies[addr], SeenAt: now})
	}

	if s.resolveMACs {
		s.enrichHardware(ctx, hosts)
	}

	if s.resolveNames {
		resolveNames(ctx, hosts)
	}

	s.log.DebugContext(ctx, "sweep complete",
		slog.String("prefix", p.String()),
		slog.Int("found", len(hosts)),
	)

	return hosts, nil
}

// enrichHardware fills in MAC addresses from the kernel's neighbour table and
// from this host's own interfaces. Only on-link hosts appear in the neighbour
// table: an address behind a router is reached through the router's own MAC, so
// a routed network yields no hardware addresses at all.
func (s *Scanner) enrichHardware(ctx context.Context, hosts []Host) {
	table, err := neighbours()
	if err != nil {
		s.log.WarnContext(ctx, "could not read neighbour table", slog.Any("error", err))

		table = map[netip.Addr]string{}
	}

	local, err := localAddrs()
	if err != nil {
		s.log.WarnContext(ctx, "could not read local interfaces", slog.Any("error", err))

		local = map[netip.Addr]localInterface{}
	}

	applyHardware(hosts, table, local)
}

// applyHardware merges the two sources into hosts. A local interface wins: it
// is the kernel describing its own address, and the neighbour table has no
// entry to disagree with in the first place.
func applyHardware(hosts []Host, table map[netip.Addr]string, local map[netip.Addr]localInterface) {
	for i := range hosts {
		if iface, ok := local[hosts[i].Addr]; ok {
			hosts[i].Self = true
			hosts[i].Interface = iface.Name

			if iface.MAC != "" {
				hosts[i].MAC = iface.MAC
			}

			continue
		}

		if mac, ok := table[hosts[i].Addr]; ok {
			hosts[i].MAC = mac
		}
	}

	for i := range hosts {
		applyVendor(&hosts[i])
	}
}

// applyVendor resolves the vendor behind a host's MAC address.
//
// A randomised address is recorded as such rather than left blank: it is not a
// gap in the table that a newer one would close, and treating it as one leads
// to hunting for a vendor that does not exist.
func applyVendor(h *Host) {
	if h.MAC == "" {
		return
	}

	hw, err := net.ParseMAC(h.MAC)
	if err != nil {
		return
	}

	if oui.IsLocallyAdministered(hw) {
		h.Randomised = true

		return
	}

	if v, ok := oui.Lookup(hw); ok {
		h.Vendor = v.Short
	}
}
