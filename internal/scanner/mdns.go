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

	"golang.org/x/net/dns/dnsmessage"
)

// mdnsPort is where an mDNS responder listens. It is a variable so a test can
// point queries at a responder of its own.
var mdnsPort uint16 = 5353

// mdnsWait is how long to keep reading answers after the last query is sent. A
// responder answers a unicast query at once, so a device silent this long is
// one that will not answer.
const mdnsWait = time.Second

// maxNameLength is the longest name DNS can carry, written without the root
// dot.
const maxNameLength = 253

// askMDNS asks each IPv4 address in addrs for its name with an mDNS reverse
// lookup sent to the device itself (RFC 6762, section 5.5), and returns the
// names that came back. An address that did not answer, or answered with
// something unusable as a name, is absent.
//
// Queries go out at rate per second. It returns once every address has
// answered, or wait after the last query. When ctx ends first, it returns the
// names that arrived before then, with ctx's error.
func askMDNS(
	ctx context.Context,
	addrs []netip.Addr,
	port uint16,
	rate int,
	wait time.Duration,
) (map[netip.Addr]string, error) {
	names := make(map[netip.Addr]string)

	// want holds the reverse name each target is asked about, keyed by where
	// its answer must come from.
	want := make(map[netip.AddrPort]string, len(addrs))
	targets := make([]netip.AddrPort, 0, len(addrs))

	for _, a := range addrs {
		if !a.Is4() {
			continue
		}

		ap := netip.AddrPortFrom(a, port)
		want[ap] = reverseName(a)
		targets = append(targets, ap)
	}

	if len(targets) == 0 {
		return names, nil
	}

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return names, fmt.Errorf("open mDNS socket: %w", err)
	}
	defer func() { _ = pc.Close() }()

	var reader sync.WaitGroup

	// The reader alone writes names until Wait returns, and returns by itself
	// once every target has answered.
	reader.Go(func() { readMDNS(pc, want, names) })

	sendErr := sendMDNS(ctx, pc, targets, want, rate)

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

// sendMDNS writes one reverse query to each target, paced to rate per second.
// When ctx ends it stops and returns nil, leaving ctx's error to the caller.
func sendMDNS(
	ctx context.Context,
	pc net.PacketConn,
	targets []netip.AddrPort,
	want map[netip.AddrPort]string,
	rate int,
) error {
	ticker := time.NewTicker(time.Second / time.Duration(max(rate, 1)))
	defer ticker.Stop()

	for _, ap := range targets {
		q, err := reverseQuery(want[ap])
		if err != nil {
			return fmt.Errorf("build mDNS query: %w", err)
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

// readMDNS records the first usable answer from each target in names. It
// returns once every target has answered or a read fails.
func readMDNS(pc net.PacketConn, want map[netip.AddrPort]string, names map[netip.Addr]string) {
	// An mDNS message can fill a jumbo frame (RFC 6762, section 17).
	buf := make([]byte, 9000)

	for len(names) < len(want) {
		// The read deadline askMDNS sets is what fails this read once the
		// wait is over.
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

		q, ok := want[ap]
		if !ok {
			continue
		}

		if _, done := names[ap.Addr()]; done {
			continue
		}

		if name, ok := parseReverseAnswer(buf[:n], q); ok {
			names[ap.Addr()] = name
		}
	}
}

// reverseName returns the in-addr.arpa name that a reverse lookup of a asks
// about.
func reverseName(a netip.Addr) string {
	b := a.As4()

	return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", b[3], b[2], b[1], b[0])
}

// reverseQuery builds a PTR query for name.
func reverseQuery(name string) ([]byte, error) {
	n, err := dnsmessage.NewName(name)
	if err != nil {
		return nil, err
	}

	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})

	if err := b.StartQuestions(); err != nil {
		return nil, err
	}

	if err := b.Question(dnsmessage.Question{Name: n, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}

	return b.Finish()
}

// parseReverseAnswer returns the name a response gives for the reverse name q,
// and false when it is no response, has no PTR answer for q, or names
// something unusable as a name.
func parseReverseAnswer(b []byte, q string) (string, bool) {
	var p dnsmessage.Parser

	// The message ID is left unchecked. A responder may echo the query's or
	// send zero, and the query sends zero, so the ID tells nothing apart.
	h, err := p.Start(b)
	if err != nil || !h.Response {
		return "", false
	}

	if err := p.SkipAllQuestions(); err != nil {
		return "", false
	}

	for {
		ah, err := p.AnswerHeader()
		if err != nil {
			return "", false
		}

		if ah.Type != dnsmessage.TypePTR || !strings.EqualFold(ah.Name.String(), q) {
			if err := p.SkipAnswer(); err != nil {
				return "", false
			}

			continue
		}

		ptr, err := p.PTRResource()
		if err != nil {
			return "", false
		}

		return cleanName(ptr.PTR.String())
	}
}

// cleanName trims the root dot off name and reports whether what is left can
// be shown as a device's name. A device writes its own answer, so the name can
// hold any byte; a control character or space is refused.
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
