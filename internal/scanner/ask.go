package scanner

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pushkar-anand/jocasta/internal/hosts"
)

// askWait is how long to read answers after the last query. A host answers a
// unicast name query at once. So a host that is silent for this long will not
// answer.
const askWait = time.Second

// nameProtocol is one way to ask a host for its name.
type nameProtocol struct {
	// query returns the message that asks addr for its name.
	query func(addr netip.Addr) ([]byte, error)

	// answer returns the name from a message from addr. It returns false when
	// the message gives no name that cleanName passes.
	answer func(b []byte, addr netip.Addr) (string, bool)
}

// askNames sends the query of proto to port on each IPv4 address in addrs. It
// returns the names that came back. An address that did not answer, or that
// gave an unusable name, is absent.
//
// Queries go out at rate per second. It returns when every address gave a
// usable name, or wait after the last query. If ctx ends first, it returns the
// names that arrived before, with the error of ctx.
func askNames(
	ctx context.Context,
	proto nameProtocol,
	addrs []netip.Addr,
	port uint16,
	rate int,
	wait time.Duration,
) (map[netip.Addr]string, error) {
	names := make(map[netip.Addr]string)

	// asked holds each target. Its key is where the answer must come from.
	asked := make(map[netip.AddrPort]bool, len(addrs))
	targets := make([]netip.AddrPort, 0, len(addrs))

	for _, a := range addrs {
		if !a.Is4() {
			continue
		}

		ap := netip.AddrPortFrom(a, port)
		asked[ap] = true
		targets = append(targets, ap)
	}

	if len(targets) == 0 {
		return names, nil
	}

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return names, fmt.Errorf("open name query socket: %w", err)
	}
	defer func() { _ = pc.Close() }()

	var reader sync.WaitGroup

	// Only the reader writes names until Wait returns. It returns by itself
	// when every target gave a usable name.
	reader.Go(func() { readAnswers(pc, proto, asked, names) })

	sendErr := sendQueries(ctx, pc, proto, targets, rate)

	// The read deadline ends the reader. So set it only after the last query
	// went out. Make it earlier when ctx ends.
	deadline := time.Now().Add(wait)
	if ctx.Err() != nil {
		deadline = time.Now()
	}

	_ = pc.SetReadDeadline(deadline)

	stop := context.AfterFunc(ctx, func() { _ = pc.SetReadDeadline(time.Now()) })
	defer stop()

	reader.Wait()

	if sendErr != nil {
		return names, sendErr
	}

	return names, ctx.Err()
}

// sendQueries writes the query of proto to each target, at rate per second. It
// returns an error only when a query cannot be built. When ctx ends, it stops
// and returns nil. The caller handles the error of ctx.
func sendQueries(
	ctx context.Context,
	pc net.PacketConn,
	proto nameProtocol,
	targets []netip.AddrPort,
	rate int,
) error {
	ticker := time.NewTicker(time.Second / time.Duration(max(rate, 1)))
	defer ticker.Stop()

	for _, ap := range targets {
		q, err := proto.query(ap.Addr())
		if err != nil {
			return fmt.Errorf("build name query: %w", err)
		}

		// A failed write tells something only about that address. So the
		// others still get the query.
		_, _ = pc.WriteTo(q, net.UDPAddrFromAddrPort(ap))

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
	}

	return nil
}

// readAnswers records in names the first usable name of each target. It
// returns when every target gave one, or when a read fails.
func readAnswers(pc net.PacketConn, proto nameProtocol, asked map[netip.AddrPort]bool, names map[netip.Addr]string) {
	// An mDNS message can fill a jumbo frame (RFC 6762, section 17). A NetBIOS
	// answer is much smaller.
	buf := make([]byte, 9000)

	for len(names) < len(asked) {
		// The read deadline from askNames fails this read when the wait is
		// over, or when ctx ends.
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		udp, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}

		// Only the asked address can answer for itself, from the port of the
		// query.
		ap := udp.AddrPort()
		ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())

		if !asked[ap] {
			continue
		}

		if _, done := names[ap.Addr()]; done {
			continue
		}

		if name, ok := proto.answer(buf[:n], ap.Addr()); ok {
			names[ap.Addr()] = name
		}
	}
}

// cleanName removes the root dot from name. It reports whether the rest has no
// space and passes [hosts.CleanName]. A host writes its own answer. So the
// name can contain any byte.
func cleanName(name string) (string, bool) {
	name = strings.TrimSuffix(name, ".")

	// Check this before hosts.CleanName. hosts.CleanName trims whitespace at
	// the ends, but this rule refuses it.
	if strings.ContainsFunc(name, unicode.IsSpace) {
		return "", false
	}

	return hosts.CleanName(name)
}
