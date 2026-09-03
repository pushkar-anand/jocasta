// Package scanner discovers hosts on a network by sweeping an address range with
// ICMP echo requests and enriching whatever answers with a MAC address and a
// hostname.
package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/pkg/cidr"
)

// ErrPrefixTooLarge is returned for a range wide enough that sweeping it would
// be a mistake rather than an intent.
var ErrPrefixTooLarge = errors.New("prefix too large to sweep")

// maxSweepHosts caps a single sweep. A /16 is 65k probes, which is already
// slow and loud; anything wider is almost certainly a typo'd prefix. The cap is
// this scanner's policy, which is why it lives here and not in pkg/cidr.
const maxSweepHosts = 65536

// Host is an address that answered a sweep. The identifying detail is carried
// by the embedded [hosts.Host], so a sweep and a plugin describe the same
// device the same way; what is added here is what only a probe can know.
type Host struct {
	*hosts.Host

	// RTT is how long the address took to answer.
	RTT time.Duration

	// SeenAt is when the sweep ran, taken once for the whole sweep so every
	// host it found carries the same observation time.
	SeenAt time.Time

	// Self marks an address belonging to the host running the scan; the
	// embedded Interface names the interface holding it. Both are empty for
	// every other host, whose interfaces are not visible from here.
	Self bool
}

// MarshalJSON writes the sweep's fields alongside the embedded host's. Without
// it Go promotes [hosts.Host.MarshalJSON] and silently drops RTT, SeenAt and
// Self.
func (h Host) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Addr       netip.Addr    `json:"addr"`
		MAC        string        `json:"mac,omitempty"`
		Hostname   string        `json:"hostname,omitempty"`
		RTT        time.Duration `json:"rtt"`
		SeenAt     time.Time     `json:"seen_at"`
		Vendor     string        `json:"vendor,omitempty"`
		Randomised bool          `json:"randomised,omitempty"`
		Self       bool          `json:"self,omitempty"`
		Interface  string        `json:"interface,omitempty"`
	}{
		Addr:       h.Address(),
		MAC:        h.MAC,
		Hostname:   h.Hostname(),
		RTT:        h.RTT,
		SeenAt:     h.SeenAt,
		Vendor:     h.ShortName(),
		Randomised: h.Randomised(),
		Self:       h.Self,
		Interface:  h.Interface,
	})
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

	found := s.enrich(ctx, replies, time.Now())

	s.log.DebugContext(ctx, "sweep complete",
		slog.String("prefix", p.String()),
		slog.Int("found", len(found)),
	)

	return found, nil
}

// enrich turns the addresses that answered into hosts, adding what only a
// probe knows to what [hosts.BuildHost] can work out. at stamps every host,
// so one sweep is one observation rather than a row of nearly-equal times.
func (s *Scanner) enrich(ctx context.Context, replies map[netip.Addr]time.Duration, at time.Time) []Host {
	ordered := slices.SortedFunc(maps.Keys(replies), netip.Addr.Compare)

	// Hardware first: a host takes its vendor from its MAC at build time, so
	// the MAC has to be known before the host is built.
	var (
		table map[netip.Addr]string
		local map[netip.Addr]localInterface
	)

	if s.resolveMACs {
		table, local = s.hardware(ctx)
	}

	inputs := make([]hosts.HostInput, len(ordered))
	self := make(map[netip.Addr]bool, len(local))

	for i, addr := range ordered {
		mac, iface, own := hardwareFor(addr, table, local)
		if own {
			self[addr] = true
		}

		inputs[i] = hosts.HostInput{
			IP:          addr.String(),
			MAC:         mac,
			Interface:   iface,
			ResolveName: s.resolveNames,
		}
	}

	// An address that answered is the sweep's finding, so enrichment failing
	// for one host is not a reason to lose the rest. Both tables feeding this
	// have already parsed every MAC they yield, which leaves little to fail.
	built, err := hosts.BulkBuild(ctx, inputs)
	if err != nil {
		s.log.WarnContext(ctx, "some hosts could not be enriched", slog.Any("error", err))
	}

	found := make([]Host, 0, len(built))

	for _, h := range built {
		addr := h.Address()
		found = append(found, Host{Host: h, RTT: replies[addr], SeenAt: at, Self: self[addr]})
	}

	return found
}

// hardware reads the two views of who holds an address: the kernel's neighbour
// table and this host's own interfaces. Only on-link hosts appear in the
// neighbour table -- an address behind a router is reached through the router's
// own MAC, so a routed network yields no hardware addresses at all.
//
// Either being unreadable costs the MACs it would have supplied and nothing
// else, so both fall back to an empty map.
func (s *Scanner) hardware(ctx context.Context) (map[netip.Addr]string, map[netip.Addr]localInterface) {
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

	return table, local
}

// hardwareFor reports the MAC and interface an address is known by, and whether
// it is one of this host's own.
//
// A local interface wins over the neighbour table: it is the kernel describing
// its own address, and a host never ARPs for itself, so any entry the table
// holds is stale. An interface with no hardware address still counts as local.
func hardwareFor(
	addr netip.Addr,
	table map[netip.Addr]string,
	local map[netip.Addr]localInterface,
) (mac, iface string, self bool) {
	if l, ok := local[addr]; ok {
		return l.MAC, l.Name, true
	}

	return table[addr], "", false
}
