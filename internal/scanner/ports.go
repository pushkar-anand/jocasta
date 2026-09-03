package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Default knobs for a homelab: a listening service on a LAN answers in
// single-digit milliseconds, so half a second is really the budget for a
// firewalled port that will never answer.
const defaultDialTimeout = 500 * time.Millisecond

// DefaultConcurrency caps connections in flight when nothing configures a
// ceiling. A burst of new flows is what a cheap router's connection table
// handles worst, and the scan runs on a long interval where finishing sooner
// buys nothing, so the default stays well below where consumer gear strains.
const DefaultConcurrency = 64

// PortScan is what a probe found at one address.
type PortScan struct {
	// Addr is the address probed.
	Addr netip.Addr

	// Open lists the ports that completed a TCP handshake, ascending.
	Open []uint16

	// Scanned lists every port probed, ascending, so a port missing from Open
	// can be told from one that was never checked. Ingest needs the difference
	// to know a port has closed rather than dropped out of the set.
	Scanned []uint16

	// SeenAt is when the scan ran, taken once for the whole scan so every
	// address it touched carries the same observation time.
	SeenAt time.Time
}

// MarshalJSON expands the open ports with the service each port usually
// carries, and reduces Scanned to its length: the scanned list is the same
// preset or spec for every address in a run, so its size is the only part
// worth repeating per result.
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

// PortScanner probes TCP ports on addresses discovery has already found. It
// holds no per-scan state, so a single instance is safe for concurrent use.
type PortScanner struct {
	log *slog.Logger

	// ports is the set every address is probed for, ascending and unique. It
	// defaults to the curated preset; WithPorts replaces it wholesale.
	ports []uint16

	// timeout bounds a single connect. A port that has not completed a
	// handshake in this long is recorded not-open.
	timeout time.Duration

	// concurrency caps connections in flight across the whole scan.
	concurrency int
}

// PortOption configures a PortScanner.
type PortOption func(*PortScanner)

// WithPorts sets the ports every address is probed for. An empty list leaves
// the preset in place, the same zero-value guard the other options use. The
// list is sorted and deduplicated, so a spec from [ParsePortSpec] and a
// hand-built slice behave the same.
func WithPorts(ports []uint16) PortOption {
	return func(ps *PortScanner) {
		if len(ports) == 0 {
			return
		}

		ps.ports = normalisePorts(ports)
	}
}

// WithDialTimeout sets how long a single connect may take before the port is
// called not-open.
func WithDialTimeout(d time.Duration) PortOption {
	return func(ps *PortScanner) {
		if d > 0 {
			ps.timeout = d
		}
	}
}

// WithConcurrency sets the ceiling on connections open at once.
func WithConcurrency(n int) PortOption {
	return func(ps *PortScanner) {
		if n > 0 {
			ps.concurrency = n
		}
	}
}

// NewPortScanner builds a PortScanner with homelab defaults: the curated
// preset, a half-second dial timeout and [DefaultConcurrency] connections in
// flight.
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

// Ports returns the set this scanner probes, as a copy.
func (ps *PortScanner) Ports() []uint16 {
	return slices.Clone(ps.ports)
}

// Scan probes every port on every target and returns one PortScan per target,
// in the order they were given. at stamps every result. A target with nothing
// open still comes back, carrying the scanned set, because ingest reads that to
// tell a port that has closed from one it never looked at.
//
// A cancelled context stops new connections; the ones in flight finish, so
// every result stays coherent. It is not reported as an error: a short scan is
// still a true account of what answered before it stopped.
func (ps *PortScanner) Scan(ctx context.Context, targets []netip.Addr, at time.Time) []PortScan {
	scanned := slices.Clone(ps.ports)
	found := newPortResults(len(targets))
	dialer := &net.Dialer{Timeout: ps.timeout}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(ps.concurrency)

	ps.log.DebugContext(ctx, "starting port scan",
		slog.Int("targets", len(targets)),
		slog.Int("ports", len(ps.ports)),
	)

feed:
	for i, addr := range targets {
		for _, port := range ps.ports {
			if gctx.Err() != nil {
				break feed
			}

			g.Go(func() error {
				if probe(gctx, dialer, addr, port) {
					found.add(i, port)
				}

				return nil
			})
		}
	}

	// A worker never returns an error -- a refused port is data, not a failure
	// -- so Wait has nothing to report and only marks the group done.
	_ = g.Wait()

	results := make([]PortScan, len(targets))

	for i, addr := range targets {
		results[i] = PortScan{
			Addr:    addr,
			Open:    found.sorted(i),
			Scanned: scanned,
			SeenAt:  at,
		}
	}

	ps.log.DebugContext(ctx, "port scan complete", slog.Int("targets", len(targets)))

	return results
}

// portResults collects the open ports found for each target by index. The
// workers all write to it at once, so every method takes the lock -- the same
// shape as the sweep's results collector.
type portResults struct {
	mu   sync.Mutex
	open [][]uint16
}

func newPortResults(targets int) *portResults {
	return &portResults{open: make([][]uint16, targets)}
}

// add records that target i had port open.
func (r *portResults) add(i int, port uint16) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.open[i] = append(r.open[i], port)
}

// sorted returns target i's open ports in ascending order.
func (r *portResults) sorted(i int) []uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()

	slices.Sort(r.open[i])

	return r.open[i]
}

// probe reports whether a TCP connection to addr:port completes. A refused
// connection, an unreachable host and a timeout are all "not open" -- the scan
// does not distinguish a closed port from a filtered one.
func probe(ctx context.Context, d *net.Dialer, addr netip.Addr, port uint16) bool {
	conn, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(addr, port).String())
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

// ParsePortSpec turns a spec like "22,80,443,8000-8100" into a sorted,
// deduplicated port list. A spec is a comma-separated list of single ports and
// N-M inclusive ranges. "1-65535" is allowed and is how a full scan is asked
// for.
//
// An empty spec is an error rather than an empty list or the preset: a caller
// that wants the preset does not pass a spec at all, so an empty one is a
// misconfiguration worth naming.
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

	ports := make([]uint16, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}

	slices.Sort(ports)

	return ports, nil
}

// parsePortRange reads one spec entry -- a single port or a "lo-hi" range --
// and returns its inclusive bounds.
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

// parsePort reads one port number, rejecting anything outside the range the
// device_ports CHECK admits. The bitSize of 16 does the upper bound; the zero
// check does the lower.
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

// normalisePorts sorts, deduplicates and drops the zero port from a list, so a
// hand-built slice reaching the scanner behaves like a parsed spec.
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
