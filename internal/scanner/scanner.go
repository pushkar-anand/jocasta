// Package scanner discovers hosts on a network by sweeping an address range with
// ICMP echo requests and enriching whatever answers with a MAC address and a
// hostname, from reverse DNS or, failing that, from the host itself over mDNS,
// NetBIOS, DNS-SD or SSDP.
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
	"sync/atomic"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/pkg/cidr"
)

// ErrPrefixTooLarge is returned for a range wide enough that sweeping it is
// almost certainly a mistake.
var ErrPrefixTooLarge = errors.New("prefix too large to sweep")

// maxSweepHosts caps a single sweep. A /16 is 65k probes, which is already
// slow and loud; anything wider is almost certainly a typo'd prefix. The cap is
// this scanner's policy, which is why it lives here.
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

	// NameSource is how the sweep learned the name the embedded host carries,
	// and is empty when it carries none.
	NameSource dbtype.HostnameSource

	// Services are what the address advertised over DNS-SD during the sweep,
	// sorted by type and instance. They are empty when it advertised nothing
	// or the browse is turned off.
	Services []hosts.Service
}

// MarshalJSON writes the sweep's fields alongside the embedded host's. Without
// it Go promotes [hosts.Host.MarshalJSON] and silently drops every field
// declared on Host itself.
func (h Host) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Addr       netip.Addr      `json:"addr"`
		MAC        string          `json:"mac,omitempty"`
		Hostname   string          `json:"hostname,omitempty"`
		NameSource string          `json:"name_source,omitempty"`
		RTT        time.Duration   `json:"rtt"`
		SeenAt     time.Time       `json:"seen_at"`
		Vendor     string          `json:"vendor,omitempty"`
		Randomised bool            `json:"randomised,omitempty"`
		Self       bool            `json:"self,omitempty"`
		Interface  string          `json:"interface,omitempty"`
		Services   []hosts.Service `json:"services,omitempty"`
	}{
		Addr:       h.Address(),
		MAC:        h.MAC,
		Hostname:   h.Hostname(),
		NameSource: string(h.NameSource),
		RTT:        h.RTT,
		SeenAt:     h.SeenAt,
		Vendor:     h.ShortName(),
		Randomised: h.Randomised(),
		Self:       h.Self,
		Interface:  h.Interface,
		Services:   h.Services,
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
	// would report them as down.
	wait time.Duration

	// rate caps probes per second so a large sweep does not arrive as a burst
	// that a switch or a cheap IoT device treats as a flood.
	rate int

	resolveNames   bool
	resolveMACs    bool
	resolveMDNS    bool
	resolveNetBIOS bool
	resolveDNSSD   bool
	resolveSSDP    bool

	// mdnsPort, netbiosPort, dnssdGroup and ssdpGroup are where each
	// protocol's queries go, which a test points at a responder of its own.
	mdnsPort    uint16
	netbiosPort uint16
	dnssdGroup  netip.AddrPort
	ssdpGroup   netip.AddrPort

	// dnssdFailed is set while DNS-SD browses keep failing, as ssdpFailed is
	// for SSDP searches.
	dnssdFailed atomic.Bool

	// ssdpFailed is set while SSDP searches keep failing, so only the first
	// failure in a run is logged as a warning. A search that works clears it.
	ssdpFailed atomic.Bool
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

// WithMDNSResolution controls asking hosts that answered, and have no reverse
// DNS name, for their name over mDNS. With reverse DNS turned off, that is
// every host that answered.
func WithMDNSResolution(v bool) Option {
	return func(s *Scanner) { s.resolveMDNS = v }
}

// WithNetBIOSResolution controls asking hosts that answered, and have no name
// from reverse DNS or mDNS, for their name over NetBIOS.
func WithNetBIOSResolution(v bool) Option {
	return func(s *Scanner) { s.resolveNetBIOS = v }
}

// WithDNSSDResolution controls browsing the services hosts that answered
// advertise over DNS-SD, and naming each that has no name from reverse DNS,
// mDNS or NetBIOS after the name it gives them.
func WithDNSSDResolution(v bool) Option {
	return func(s *Scanner) { s.resolveDNSSD = v }
}

// WithSSDPResolution controls naming hosts that answered, and have no name
// from reverse DNS, mDNS, NetBIOS or DNS-SD, after the friendlyName in their UPnP
// description.
func WithSSDPResolution(v bool) Option {
	return func(s *Scanner) { s.resolveSSDP = v }
}

// WithMACResolution controls ARP-table lookups for hosts that answered.
func WithMACResolution(v bool) Option {
	return func(s *Scanner) { s.resolveMACs = v }
}

// New builds a Scanner with defaults tuned for a home LAN: fast enough to sweep
// a /24 in a couple of seconds, gentle enough not to upset IoT firmware.
func New(log *slog.Logger, opts ...Option) *Scanner {
	s := &Scanner{
		log:            log,
		rounds:         2,
		wait:           2 * time.Second,
		rate:           1000,
		resolveNames:   true,
		resolveMACs:    true,
		resolveMDNS:    true,
		resolveNetBIOS: true,
		mdnsPort:       standardMDNSPort,
		netbiosPort:    standardNetBIOSPort,
		resolveDNSSD:   true,
		dnssdGroup:     standardDNSSDGroup,
		resolveSSDP:    true,
		ssdpGroup:      standardSSDPGroup,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Scan sweeps every usable address in p and returns the hosts that answered,
// ordered by address. An empty result with a nil error means nothing answered.
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

	replies, err := sweep(ctx, sweepParams{
		log:     s.log,
		targets: targets,
		count:   count,
		rounds:  s.rounds,
		wait:    s.wait,
		rate:    s.rate,
	})
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
// so one sweep is one observation.
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
		s.log.WarnContext(ctx, "some hosts could not be enriched", logger.Err(err))
	}

	found := make([]Host, 0, len(built))

	for _, h := range built {
		addr := h.Address()

		host := Host{Host: h, RTT: replies[addr], SeenAt: at, Self: self[addr]}
		if h.Hostname() != "" {
			host.NameSource = dbtype.HostnameFromDNS
		}

		found = append(found, host)
	}

	if s.resolveMDNS {
		s.nameOver(ctx, found, s.perHost(mdns, s.mdnsPort), dbtype.HostnameFromMDNS)
	}

	if s.resolveNetBIOS {
		s.nameOver(ctx, found, s.perHost(netbios, s.netbiosPort), dbtype.HostnameFromNetBIOS)
	}

	if s.resolveDNSSD {
		s.browseServices(ctx, found)
	}

	if s.resolveSSDP {
		s.nameOver(ctx, found, s.ssdpLookup, dbtype.HostnameFromSSDP)
	}

	return found
}

// perHost returns the lookup that sends proto's query to port on each host.
func (s *Scanner) perHost(proto nameProtocol, port uint16) nameLookup {
	return func(ctx context.Context, addrs []netip.Addr) (map[netip.Addr]string, error) {
		return askNames(ctx, proto, addrs, port, s.rate, askWait)
	}
}

// ssdpLookup names addrs after their UPnP friendlyName, found with one SSDP
// search. A machine that cannot send the search, such as one without a route
// for multicast, fails it on every sweep, so only the first failure in each
// run of failures is returned and the rest are logged at debug level.
func (s *Scanner) ssdpLookup(ctx context.Context, addrs []netip.Addr) (map[netip.Addr]string, error) {
	names, err := askSSDP(ctx, s.ssdpGroup, addrs, ssdpWait)
	if err == nil {
		s.ssdpFailed.Store(false)

		return names, nil
	}

	if ctx.Err() == nil && !s.ssdpFailed.CompareAndSwap(false, true) {
		s.log.DebugContext(ctx, "could not ask hosts for their names over SSDP", logger.Err(err))

		return names, nil
	}

	return names, err
}

// browseServices records on each host in found the services it advertises over
// DNS-SD, and names each host still without a name after the name it gives
// them (see dnssdName). Every host is browsed, named or not, since what it
// advertises says what it is. A machine that cannot join the mDNS group fails
// on every sweep, so only the first failure in each run of failures is logged
// as a warning.
func (s *Scanner) browseServices(ctx context.Context, found []Host) {
	addrs := make([]netip.Addr, len(found))

	for i, h := range found {
		addrs[i] = h.Address()
	}

	// The services that arrived before a failure are still the hosts' own
	// answers, so they are kept.
	services, err := browseDNSSD(ctx, s.dnssdGroup, addrs, dnssdWait)

	switch {
	case err == nil:
		s.dnssdFailed.Store(false)
	case ctx.Err() == nil && !s.dnssdFailed.CompareAndSwap(false, true):
		s.log.DebugContext(ctx, "could not browse services over DNS-SD", logger.Err(err))
	default:
		s.log.WarnContext(ctx, "could not browse services over DNS-SD", logger.Err(err))
	}

	for i, h := range found {
		found[i].Services = services[h.Address()]

		if h.Hostname() != "" {
			continue
		}

		if name, ok := dnssdName(found[i].Services); ok {
			found[i].Host = h.Named(name)
			found[i].NameSource = dbtype.HostnameFromDNSSD
		}
	}
}

// nameLookup asks addrs for their names, and returns the names that came
// back, with any error that cut the lookup short.
type nameLookup func(ctx context.Context, addrs []netip.Addr) (map[netip.Addr]string, error)

// nameOver asks each host in found that has no name for one with ask. It names
// each host that answers, with standing as the name's source. A host that
// already has a name is not asked, because the lookups run from the highest
// standing down.
func (s *Scanner) nameOver(
	ctx context.Context,
	found []Host,
	ask nameLookup,
	standing dbtype.HostnameSource,
) {
	var nameless []netip.Addr

	for _, h := range found {
		if h.Hostname() == "" {
			nameless = append(nameless, h.Address())
		}
	}

	if len(nameless) == 0 {
		return
	}

	// The names that arrived before a failure are still the hosts' own
	// answers, so they are kept.
	names, err := ask(ctx, nameless)
	if err != nil {
		s.log.WarnContext(ctx, "could not ask hosts for their names",
			slog.String("standing", string(standing)),
			logger.Err(err),
		)
	}

	for i, h := range found {
		name, ok := names[h.Address()]
		if !ok {
			continue
		}

		found[i].Host = h.Named(name)
		found[i].NameSource = standing
	}
}

// hardware reads the two views of who holds an address: the kernel's neighbour
// table and this host's own interfaces. Only on-link hosts appear in the
// neighbour table: an address behind a router is reached through the router's
// own MAC, so a routed network yields no hardware addresses at all.
//
// Either being unreadable costs the MACs it would have supplied and nothing
// else, so both fall back to an empty map.
func (s *Scanner) hardware(ctx context.Context) (map[netip.Addr]string, map[netip.Addr]localInterface) {
	table, err := neighbours()
	if err != nil {
		s.log.WarnContext(ctx, "could not read neighbour table", logger.Err(err))

		table = map[netip.Addr]string{}
	}

	local, err := localAddrs()
	if err != nil {
		s.log.WarnContext(ctx, "could not read local interfaces", logger.Err(err))

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
