package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Default settings for a homelab. A listening service on a LAN answers in a few
// milliseconds. So half a second is really the time for a port behind a
// firewall that never answers.
const defaultDialTimeout = 500 * time.Millisecond

// DefaultConcurrency limits the open connections when no setting gives a
// limit. The connection table of a cheap router handles a burst of new flows
// worst. The scan runs on a long interval, where an earlier finish gives
// nothing. So the default stays well below the load that consumer equipment
// can handle.
const DefaultConcurrency = 64

// PortTarget is one address to probe. Extra are more ports to probe on it, in
// addition to the set of every address. An example is the ports of the
// services that it advertises.
type PortTarget struct {
	Addr  netip.Addr
	Extra []uint16
}

// Targets wraps addrs as targets with no extra ports.
func Targets(addrs []netip.Addr) []PortTarget {
	targets := make([]PortTarget, len(addrs))
	for i, a := range addrs {
		targets[i] = PortTarget{Addr: a}
	}

	return targets
}

// PortScan is what a probe found at one address.
type PortScan struct {
	// Addr is the address probed.
	Addr netip.Addr

	// Open lists the ports that completed a TCP handshake, ascending.
	Open []uint16

	// Scanned lists every port probed at this address, ascending. So a port
	// that is not in Open is different from a port that was never checked.
	// Ingest needs the difference to know that a port closed. It is the set of
	// the scanner, plus the Extra of the target.
	Scanned []uint16

	// Extra lists the ports in Scanned that are not in the set of the
	// scanner, ascending. The scan probed them only because the target asked
	// for them.
	Extra []uint16

	// Answered reports whether the address replied on any port. A reply is an
	// accepted or a refused connection. If every probe to an address timed
	// out, it can be asleep, gone or filtering. So its Scanned tells nothing
	// about which ports closed.
	Answered bool

	// SeenAt is when the scan ran. It is taken once for the whole scan. So
	// every address of the scan has the same observation time.
	SeenAt time.Time
}

// MarshalJSON adds the usual service of each open port. It reduces Scanned to
// its length. The scanned list is the preset or spec for every address in a
// run, plus a few ports for some. So only its size is worth a repeat in each
// result.
func (s PortScan) MarshalJSON() ([]byte, error) {
	type openPort struct {
		Port    uint16 `json:"port"`
		Service string `json:"service,omitempty"`
	}

	open := make([]openPort, len(s.Open))
	for i, p := range s.Open {
		open[i] = openPort{Port: p, Service: ServiceName(p)}
	}

	return json.Marshal(struct {
		Addr    string     `json:"addr"`
		Open    []openPort `json:"open"`
		Scanned int        `json:"scanned"`
		SeenAt  time.Time  `json:"seen_at"`
	}{
		Addr:    s.Addr.String(),
		Open:    open,
		Scanned: len(s.Scanned),
		SeenAt:  s.SeenAt,
	})
}

// PortScanner probes TCP ports on addresses that discovery already found. It
// holds no state for each scan. So a single instance is safe for concurrent
// use.
type PortScanner struct {
	log *slog.Logger

	// ports is the set of ports to probe on every address, ascending and
	// unique. The default is the preset. WithPorts replaces all of it.
	ports []uint16

	// timeout limits a single connect. If a port does not complete a
	// handshake in this time, the scan records it as not open.
	timeout time.Duration

	// concurrency limits the open connections across the whole scan.
	concurrency int
}

// PortOption configures a PortScanner.
type PortOption func(*PortScanner)

// WithPorts sets the ports to probe on every address. An empty list keeps the
// preset, as the zero-value guard of the other options does. The list is
// sorted, with no duplicates. So a spec from [ParsePortSpec] and a hand-built
// slice work in the same way.
func WithPorts(ports []uint16) PortOption {
	return func(ps *PortScanner) {
		if len(ports) == 0 {
			return
		}

		ps.ports = normalisePorts(ports)
	}
}

// WithDialTimeout sets how long a single connect can take. After that time,
// the port is not open.
func WithDialTimeout(d time.Duration) PortOption {
	return func(ps *PortScanner) {
		if d > 0 {
			ps.timeout = d
		}
	}
}

// WithConcurrency sets the maximum number of connections open at once.
func WithConcurrency(n int) PortOption {
	return func(ps *PortScanner) {
		if n > 0 {
			ps.concurrency = n
		}
	}
}

// NewPortScanner builds a PortScanner with homelab defaults: the preset, a
// half-second dial timeout, and [DefaultConcurrency] open connections.
func NewPortScanner(log *slog.Logger, opts ...PortOption) *PortScanner {
	ps := &PortScanner{
		log:         log,
		ports:       presetPorts,
		timeout:     defaultDialTimeout,
		concurrency: DefaultConcurrency,
	}

	for _, opt := range opts {
		opt(ps)
	}

	return ps
}

// Ports returns the set that this scanner probes, as a copy.
func (ps *PortScanner) Ports() []uint16 {
	return slices.Clone(ps.ports)
}

// Scan probes every port in the set of the scanner, and the Extra of the
// target, on every target. It returns one PortScan for each target, in the
// given order. at is the time of every result. A target with nothing open
// still returns, with the scanned set. Ingest reads that set to tell a closed
// port from a port that it never checked.
//
// A cancelled context stops new connections. The open connections finish. So
// every result stays consistent. Scan does not report this as an error. A
// short scan is still a true record of what answered before it stopped.
func (ps *PortScanner) Scan(ctx context.Context, targets []PortTarget, at time.Time) []PortScan {
	scanned := make([][]uint16, len(targets))
	extra := make([][]uint16, len(targets))

	for i, t := range targets {
		scanned[i] = normalisePorts(append(slices.Clone(ps.ports), t.Extra...))
		extra[i] = slices.DeleteFunc(normalisePorts(t.Extra), func(port uint16) bool {
			_, inSet := slices.BinarySearch(ps.ports, port)

			return inSet
		})
	}

	found := newPortResults(len(targets))
	dialer := &net.Dialer{Timeout: ps.timeout}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(ps.concurrency)

	ps.log.DebugContext(ctx, "starting port scan",
		slog.Int("targets", len(targets)),
		slog.Int("ports", len(ps.ports)),
	)

feed:
	for i, t := range targets {
		for _, port := range scanned[i] {
			if gctx.Err() != nil {
				break feed
			}

			g.Go(func() error {
				found.add(i, port, probe(gctx, dialer, t.Addr, port))

				return nil
			})
		}
	}

	// A worker never returns an error, because a refused port is a result. So
	// Wait has nothing to report, and only marks the group done.
	_ = g.Wait()

	results := make([]PortScan, len(targets))

	for i, t := range targets {
		results[i] = PortScan{
			Addr:     t.Addr,
			Open:     found.sorted(i),
			Scanned:  scanned[i],
			Extra:    extra[i],
			Answered: found.answered[i],
			SeenAt:   at,
		}
	}

	ps.log.DebugContext(ctx, "port scan complete", slog.Int("targets", len(targets)))

	return results
}

// portResults collects what the probes of each target found, by index. All
// workers write to it at once. So every method takes the lock, as the results
// collector of the sweep does.
type portResults struct {
	mu       sync.Mutex
	open     [][]uint16
	answered []bool
}

func newPortResults(targets int) *portResults {
	return &portResults{open: make([][]uint16, targets), answered: make([]bool, targets)}
}

// add records what the probe of port on target i found.
func (r *portResults) add(i int, port uint16, got probeResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if got == portOpen {
		r.open[i] = append(r.open[i], port)
	}

	if got != portSilent {
		r.answered[i] = true
	}
}

// sorted returns the open ports of target i in ascending order.
func (r *portResults) sorted(i int) []uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()

	slices.Sort(r.open[i])

	return r.open[i]
}

// probeResult is what one connection attempt found.
type probeResult int

const (
	// portSilent is a port that gave no answer: a timeout, an unreachable
	// host, or a firewall that drops the attempt. It cannot tell a closed port
	// from a host that is not there.
	portSilent probeResult = iota

	// portRefused is a port that the host refused. So the host is up, and the
	// port is closed.
	portRefused

	// portOpen is a port that completed a handshake.
	portOpen
)

// probe tries a TCP connection to addr:port.
func probe(ctx context.Context, d *net.Dialer, addr netip.Addr, port uint16) probeResult {
	conn, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(addr, port).String())

	switch {
	case err == nil:
		_ = conn.Close()

		return portOpen
	case refused(err):
		return portRefused
	default:
		return portSilent
	}
}

// ParsePortSpec turns a spec such as "22,80,443,8000-8100" into a sorted port
// list, with no duplicates. A spec is a comma-separated list of single ports
// and N-M inclusive ranges. "1-65535" is allowed. It asks for a full scan.
//
// An empty spec is an error. A caller that wants the preset sends no spec. So
// an empty spec is a configuration error with a name.
func ParsePortSpec(spec string) ([]uint16, error) {
	fields := strings.Split(spec, ",")

	seen := make(map[uint16]struct{})

	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, fmt.Errorf("port spec %q has an empty entry", spec)
		}

		lo, hi, err := parsePortRange(field)
		if err != nil {
			return nil, err
		}

		for p := lo; ; p++ {
			seen[p] = struct{}{}

			if p == hi {
				break // p++ on 65535 wraps to 0, so the guard has to be here.
			}
		}
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// parsePortRange reads one spec entry, a single port or a "lo-hi" range. It
// returns its inclusive limits.
func parsePortRange(entry string) (lo, hi uint16, err error) {
	before, after, isRange := strings.Cut(entry, "-")

	lo, err = parsePort(before)
	if err != nil {
		return 0, 0, err
	}

	if !isRange {
		return lo, lo, nil
	}

	hi, err = parsePort(after)
	if err != nil {
		return 0, 0, err
	}

	if hi < lo {
		return 0, 0, fmt.Errorf("port range %q ends before it starts", entry)
	}

	return lo, hi, nil
}

// parsePort reads one port number. It refuses a number outside the range that
// the device_ports CHECK allows. The bitSize of 16 sets the upper limit. The
// zero check sets the lower limit.
func parsePort(s string) (uint16, error) {
	s = strings.TrimSpace(s)

	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("port %q is out of range (1-65535)", s)
		}

		return 0, fmt.Errorf("port %q is not a number", s)
	}

	if n == 0 {
		return 0, fmt.Errorf("port %q is out of range (1-65535)", s)
	}

	return uint16(n), nil
}

// normalisePorts sorts a list, and removes duplicates and the zero port. So a
// hand-built slice works as a parsed spec does.
func normalisePorts(ports []uint16) []uint16 {
	out := make([]uint16, 0, len(ports))

	for _, p := range ports {
		if p != 0 {
			out = append(out, p)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}
