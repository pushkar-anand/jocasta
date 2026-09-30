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
	"unicode/utf8"
)

// askWait is how long to keep reading answers after the last query is sent. A
// host answers a unicast name query at once, so a host silent this long is one
// that will not answer.
const askWait = time.Second

// maxNameLength is the longest name DNS can carry, written without the root
// dot.
const maxNameLength = 253

// nameProtocol is one way to ask a host for its name.
type nameProtocol struct {
	// query returns the message that asks addr for its name.
	query func(addr netip.Addr) ([]byte, error)

	// answer returns the name a message from addr gives, and false when the
	// message gives none that cleanName passes.
	answer func(b []byte, addr netip.Addr) (string, bool)
}

// askNames sends proto's query to port on each IPv4 address in addrs, and
// returns the names that came back. An address that did not answer, or
// answered with something unusable as a name, is absent.
//
// Queries go out at rate per second. It returns once every address has given
// a usable name, or wait after the last query. When ctx ends first, it
// returns the names that arrived before then, with ctx's error.
func askNames(
	ctx context.Context,
	proto nameProtocol,
	addrs []netip.Addr,
	port uint16,
	rate int,
	wait time.Duration,
) (map[netip.Addr]string, error) {
	names := make(map[netip.Addr]string)

	// asked holds each target, keyed by where its answer must come from.
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

	// The reader alone writes names until Wait returns, and returns by itself
	// once every target has given a usable name.
	reader.Go(func() { readAnswers(pc, proto, asked, names) })

	sendErr := sendQueries(ctx, pc, proto, targets, rate)

	// The read deadline is what ends the reader, so it is set only after the
	// last query has gone out, and cut short when ctx ends.
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

// sendQueries writes proto's query to each target, paced to rate per second.
// It returns an error only when a query cannot be built. When ctx ends it
// stops and returns nil, leaving ctx's error to the caller.
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

		// A write that fails says something about that address alone, so the
		// rest are still asked.
		_, _ = pc.WriteTo(q, net.UDPAddrFromAddrPort(ap))

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
	}

	return nil
}

// readAnswers records in names the first usable name each target gives. It
// returns once every target has given one, or a read fails.
func readAnswers(pc net.PacketConn, proto nameProtocol, asked map[netip.AddrPort]bool, names map[netip.Addr]string) {
	// An mDNS message can fill a jumbo frame (RFC 6762, section 17), and a
	// NetBIOS answer is far smaller.
	buf := make([]byte, 9000)

	for len(names) < len(asked) {
		// The read deadline askNames sets is what fails this read once the
		// wait is over or ctx ends.
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}

		udp, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}

		// Only the asked address, from the port it was asked on, can answer
		// for itself.
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

// cleanName trims the root dot off name and reports whether what is left is
// valid UTF-8 of 1 to 253 bytes with no control character or space. A host
// writes its own answer, so the name can hold any byte.
func cleanName(name string) (string, bool) {
	name = strings.TrimSuffix(name, ".")

	if name == "" || len(name) > maxNameLength || !utf8.ValidString(name) {
		return "", false
	}

	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", false
		}
	}

	return name, true
}
