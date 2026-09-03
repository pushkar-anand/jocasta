package scanner

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

func echoPacket(t *testing.T, typ icmp.Type, payload []byte) []byte {
	t.Helper()

	msg := icmp.Message{Type: typ, Body: &icmp.Echo{ID: 1, Seq: 1, Data: payload}}

	b, err := msg.Marshal(nil)
	require.NoError(t, err)

	return b
}

func payloadFor(token []byte, sent time.Time) []byte {
	p := make([]byte, payloadSize)
	copy(p, token)
	binary.BigEndian.PutUint64(p[8:], uint64(sent.UnixNano()))

	return p
}

func TestParseReply(t *testing.T) {
	t.Parallel()

	token := []byte("12345678")
	sent := time.Now().Truncate(time.Nanosecond)
	peer := &net.IPAddr{IP: net.ParseIP("192.0.2.30")}

	addr, gotSent, ok := parseReply(echoPacket(t, ipv4.ICMPTypeEchoReply, payloadFor(token, sent)), peer, token)

	require.True(t, ok)
	assert.Equal(t, "192.0.2.30", addr.String())
	assert.Equal(t, sent.UnixNano(), gotSent.UnixNano())
}

// TestParseReplyFromUDPSocket covers the unprivileged socket, which reports the
// peer as a UDPAddr rather than an IPAddr.
func TestParseReplyFromUDPSocket(t *testing.T) {
	t.Parallel()

	token := []byte("12345678")
	peer := &net.UDPAddr{IP: net.ParseIP("192.0.2.30")}

	addr, _, ok := parseReply(echoPacket(t, ipv4.ICMPTypeEchoReply, payloadFor(token, time.Now())), peer, token)

	require.True(t, ok)
	assert.Equal(t, "192.0.2.30", addr.String())
}

func TestParseReplyRejects(t *testing.T) {
	t.Parallel()

	token := []byte("12345678")
	peer := &net.IPAddr{IP: net.ParseIP("192.0.2.30")}

	tests := []struct {
		name   string
		packet []byte
		peer   net.Addr
		token  []byte
	}{
		{
			name:   "another process's ping",
			packet: echoPacket(t, ipv4.ICMPTypeEchoReply, payloadFor([]byte("87654321"), time.Now())),
			peer:   peer,
			token:  token,
		},
		{
			name:   "our own outbound request read back",
			packet: echoPacket(t, ipv4.ICMPTypeEcho, payloadFor(token, time.Now())),
			peer:   peer,
			token:  token,
		},
		{
			name:   "payload too short to carry a token",
			packet: echoPacket(t, ipv4.ICMPTypeEchoReply, []byte("short")),
			peer:   peer,
			token:  token,
		},
		{
			name:   "not an icmp message",
			packet: []byte{0xff},
			peer:   peer,
			token:  token,
		},
		{
			name:   "peer address of an unexpected type",
			packet: echoPacket(t, ipv4.ICMPTypeEchoReply, payloadFor(token, time.Now())),
			peer:   &net.TCPAddr{IP: net.ParseIP("192.0.2.30")},
			token:  token,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, ok := parseReply(tt.packet, tt.peer, tt.token)
			assert.False(t, ok)
		})
	}
}

// TestSweepLoopback exercises the whole send/read path against an address that
// is guaranteed to answer, so a green run means the socket, the payload token
// and the reply matching all agree.
func TestSweepLoopback(t *testing.T) {
	t.Parallel()

	if _, err := listen(); err != nil {
		t.Skipf("no ICMP socket available: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	loopback := netip.MustParseAddr("127.0.0.1")

	targets := slices.Values([]netip.Addr{loopback})

	got, err := sweep(ctx, sweepParams{
		log:     slog.New(slog.DiscardHandler),
		targets: targets,
		count:   1,
		rounds:  1,
		wait:    time.Second,
		rate:    100,
	})
	require.NoError(t, err)

	require.Contains(t, got, loopback)
	assert.Positive(t, got[loopback])
}

func TestSweepNoTargets(t *testing.T) {
	t.Parallel()

	got, err := sweep(t.Context(), sweepParams{
		log:    slog.New(slog.DiscardHandler),
		count:  0,
		rounds: 1,
		wait:   time.Millisecond,
		rate:   100,
	})
	require.NoError(t, err)
	assert.Empty(t, got)
}
