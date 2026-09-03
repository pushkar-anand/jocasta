package scanner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"math"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// payloadSize is the echo body: an 8-byte run token, so a reply to some other
// program's ping is ignored, then the send time, so a reply carries its own
// RTT and needs no table to match it back.
const payloadSize = 16

// readSlice bounds a single blocking read so the reader goroutine notices a
// cancelled context instead of sitting in ReadFrom until the socket closes.
const readSlice = 200 * time.Millisecond

// conn is an ICMP socket plus the addressing mode it needs. A datagram socket
// wants a UDPAddr and a raw socket wants an IPAddr, and reads come back the
// same way, so the mode has to travel with the connection.
type conn struct {
	pc  *icmp.PacketConn
	udp bool
}

// listen opens an ICMP socket, preferring a raw one. The datagram fallback needs
// no privileges wherever net.ipv4.ping_group_range covers the running user,
// which is what lets an unprivileged binary sweep at all.
func listen() (*conn, error) {
	pc, rawErr := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if rawErr == nil {
		return &conn{pc: pc}, nil
	}

	pc, udpErr := icmp.ListenPacket("udp4", "0.0.0.0")
	if udpErr == nil {
		return &conn{pc: pc, udp: true}, nil
	}

	return nil, fmt.Errorf("no usable ICMP socket (raw: %v; unprivileged: %w)", rawErr, udpErr)
}

func (c *conn) dst(addr netip.Addr) net.Addr {
	ip := net.IP(addr.AsSlice())
	if c.udp {
		return &net.UDPAddr{IP: ip}
	}

	return &net.IPAddr{IP: ip}
}

// peerAddr pulls the source address out of whichever address type this socket
// mode hands back on read.
func peerAddr(a net.Addr) (netip.Addr, bool) {
	switch v := a.(type) {
	case *net.UDPAddr:
		addr, ok := netip.AddrFromSlice(v.IP)
		return addr.Unmap(), ok
	case *net.IPAddr:
		addr, ok := netip.AddrFromSlice(v.IP)
		return addr.Unmap(), ok
	default:
		return netip.Addr{}, false
	}
}

// results collects the round-trip time of the first reply seen from each
// address. The reader goroutine and the send loop both touch it, so every
// method takes the lock.
type results struct {
	mu sync.Mutex
	m  map[netip.Addr]time.Duration
}

func newResults(size int) *results {
	return &results{m: make(map[netip.Addr]time.Duration, size)}
}

// record stores rtt as addr's time unless an earlier reply already set one. It
// reports whether this was the first reply from addr: a later duplicate says
// nothing new, and retry rounds mean a host that answered twice would otherwise
// report the slower of the two.
func (r *results) record(addr netip.Addr, rtt time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, seen := r.m[addr]; seen {
		return false
	}

	r.m[addr] = rtt

	return true
}

// answered reports whether addr has replied, which is how a retry round skips
// it without a second list of pending addresses being built.
func (r *results) answered(addr netip.Addr) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.m[addr]

	return ok
}

// count is how many distinct addresses have replied.
func (r *results) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.m)
}

// take returns a copy of the collected times, to hand back once the sweep ends.
func (r *results) take() map[netip.Addr]time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	return maps.Clone(r.m)
}

// sweepParams is one call to sweep: the addresses to probe, how many there are,
// and the pacing the Scanner is configured with.
type sweepParams struct {
	log     *slog.Logger
	targets iter.Seq[netip.Addr]
	count   int
	rounds  int
	wait    time.Duration
	rate    int
}

// sweep probes every target and returns the round-trip time of the first reply
// from each. Addresses that never answer are simply absent from the result.
func sweep(ctx context.Context, p sweepParams) (map[netip.Addr]time.Duration, error) {
	if p.count == 0 {
		return map[netip.Addr]time.Duration{}, nil
	}

	c, err := listen()
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.pc.Close() }()

	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("generate run token: %w", err)
	}

	res := newResults(p.count)

	readerCtx, stopReader := context.WithCancel(ctx)
	defer stopReader()

	var reader sync.WaitGroup

	reader.Go(func() {
		readReplies(readerCtx, p.log, c, token, res)
	})

	// Echo IDs only survive on a raw socket; a datagram socket has the kernel
	// rewrite them. The run token in the payload is what actually identifies
	// this run's replies, so the ID is just conventional here.
	snd := sender{
		conn:     c,
		targets:  p.targets,
		answered: res.answered,
		token:    token,
		id:       os.Getpid() & 0xffff,
		interval: time.Second / time.Duration(max(p.rate, 1)),
	}

	for round := range p.rounds {
		if round > 0 && res.count() == p.count {
			break
		}

		if err := snd.round(ctx, round); err != nil {
			return nil, err
		}
	}

	// Give the last probes their full flight time before giving up on them.
	select {
	case <-time.After(p.wait):
	case <-ctx.Done():
	}

	stopReader()
	reader.Wait()

	return res.take(), ctx.Err()
}

// sender writes echo requests for one sweep. Everything it needs but the round
// number is fixed for the sweep's life.
type sender struct {
	conn     *conn
	targets  iter.Seq[netip.Addr]
	answered func(netip.Addr) bool
	token    []byte
	id       int
	interval time.Duration
}

// round writes one echo request per target that has not answered yet, paced to
// the configured rate. seq is the retry round, carried as the echo sequence
// number.
func (s sender) round(ctx context.Context, seq int) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for addr := range s.targets {
		if s.answered(addr) {
			continue
		}

		payload := make([]byte, payloadSize)
		copy(payload, s.token)
		binary.BigEndian.PutUint64(payload[8:], uint64(time.Now().UnixNano()))

		msg := icmp.Message{
			Type: ipv4.ICMPTypeEcho,
			Body: &icmp.Echo{ID: s.id, Seq: seq, Data: payload},
		}

		b, err := msg.Marshal(nil)
		if err != nil {
			return fmt.Errorf("marshal echo request: %w", err)
		}

		// A host that is unreachable right now fails the write outright. That is
		// information about that address, not a reason to abandon the sweep.
		if _, err := s.conn.pc.WriteTo(b, s.conn.dst(addr)); err != nil {
			continue
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// readReplies records every echo reply carrying this run's token until the
// context is cancelled.
func readReplies(ctx context.Context, log *slog.Logger, c *conn, token []byte, res *results) {
	buf := make([]byte, 512)

	for {
		if ctx.Err() != nil {
			return
		}

		if err := c.pc.SetReadDeadline(time.Now().Add(readSlice)); err != nil {
			return
		}

		n, peer, err := c.pc.ReadFrom(buf)
		if err != nil {
			// A read deadline lapsing is the loop's own pacing, not a failure.
			if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
				continue
			}

			return
		}

		addr, sent, ok := parseReply(buf[:n], peer, token)
		if !ok {
			continue
		}

		rtt := time.Since(sent)
		if res.record(addr, rtt) {
			log.Debug("host replied", slog.String("addr", addr.String()), slog.Duration("rtt", rtt))
		}
	}
}

// parseReply validates one received packet and returns the address that sent it
// with the time its payload was stamped.
func parseReply(b []byte, peer net.Addr, token []byte) (netip.Addr, time.Time, bool) {
	msg, err := icmp.ParseMessage(ipv4.ICMPTypeEchoReply.Protocol(), b)
	if err != nil {
		return netip.Addr{}, time.Time{}, false
	}

	if msg.Type != ipv4.ICMPTypeEchoReply {
		return netip.Addr{}, time.Time{}, false
	}

	echo, ok := msg.Body.(*icmp.Echo)
	if !ok || len(echo.Data) < payloadSize {
		return netip.Addr{}, time.Time{}, false
	}

	if !bytes.Equal(echo.Data[:8], token) {
		return netip.Addr{}, time.Time{}, false
	}

	addr, ok := peerAddr(peer)
	if !ok {
		return netip.Addr{}, time.Time{}, false
	}

	raw := binary.BigEndian.Uint64(echo.Data[8:16])
	if raw > math.MaxInt64 {
		return netip.Addr{}, time.Time{}, false
	}

	sent := time.Unix(0, int64(raw))

	return addr, sent, true
}
