package scanner

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// responder answers every query it receives with what reply returns for it,
// and stays silent when reply returns false.
type responder struct {
	listen  *net.UDPConn
	from    *net.UDPConn
	reply   func(query []byte) ([]byte, bool)
	queries atomic.Int32
}

// newResponder starts a responder on addr, sending each answer from from, or
// from the socket it listens on when from is nil. Port 0 picks a free port,
// which the port method reports.
func newResponder(t *testing.T, addr string, from *net.UDPConn, reply func([]byte) ([]byte, bool)) *responder {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp4", addr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = pc.Close() })

	r := &responder{listen: pc.(*net.UDPConn), from: from, reply: reply}
	if r.from == nil {
		r.from = r.listen
	}

	go r.serve()

	return r
}

func (r *responder) port() uint16 {
	return r.listen.LocalAddr().(*net.UDPAddr).AddrPort().Port()
}

func (r *responder) serve() {
	buf := make([]byte, 1500)

	for {
		n, from, err := r.listen.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}

		r.queries.Add(1)

		if raw, ok := r.reply(buf[:n]); ok {
			_, _ = r.from.WriteToUDPAddrPort(raw, from)
		}
	}
}

// mdnsReply answers an mDNS reverse query with a PTR to name.
func mdnsReply(name string) func([]byte) ([]byte, bool) {
	return func(query []byte) ([]byte, bool) {
		q, ok := questionName(query)
		if !ok {
			return nil, false
		}

		raw, err := ptrResponse(q, name)

		return raw, err == nil
	}
}

// netbiosReply answers a NetBIOS node status request with a list holding name
// as the machine's name.
func netbiosReply(name string) func([]byte) ([]byte, bool) {
	return func([]byte) ([]byte, bool) {
		return nodeStatusResponse(nbName{name, nbSuffixMachine, false}), true
	}
}

func TestAskNamesNamesTheHostsThatAnswer(t *testing.T) {
	t.Parallel()

	r := newResponder(t, "127.0.0.1:0", nil, mdnsReply("tv.local."))

	// 127.0.0.2 has nothing listening on the port, so it stays silent.
	addrs := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("127.0.0.2")}

	names, err := askNames(t.Context(), mdns, addrs, r.port(), 1000, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, map[netip.Addr]string{netip.MustParseAddr("127.0.0.1"): "tv.local"}, names)
}

func TestAskNamesReturnsOnceEveryHostAnswered(t *testing.T) {
	t.Parallel()

	r := newResponder(t, "127.0.0.1:0", nil, mdnsReply("tv.local."))

	start := time.Now()

	names, err := askNames(t.Context(), mdns, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, r.port(), 1000, 10*time.Second)
	require.NoError(t, err)

	assert.Len(t, names, 1)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestAskNamesIgnoresAnAnswerFromAnotherAddress(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	other, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.3:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = other.Close() })

	r := newResponder(t, "127.0.0.1:0", other.(*net.UDPConn), mdnsReply("tv.local."))

	names, err := askNames(t.Context(), mdns, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, r.port(), 1000, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Empty(t, names)
}

func TestAskNamesStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()

	// Nothing listens on port 9 of a loopback address, so no answer comes.
	names, err := askNames(ctx, mdns, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, 9, 1000, 10*time.Second)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, names)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestAskNamesSkipsIPv6(t *testing.T) {
	t.Parallel()

	names, err := askNames(t.Context(), mdns, []netip.Addr{netip.MustParseAddr("2001:db8::1")}, 9, 1000, 10*time.Second)
	require.NoError(t, err)

	assert.Empty(t, names)
}

func TestCleanName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{name: "a name with the root dot", in: "tv.local.", want: "tv.local", wantOK: true},
		{name: "a NetBIOS name", in: "DESKTOP-4F2K", want: "DESKTOP-4F2K", wantOK: true},
		{name: "empty", in: ""},
		{name: "the root dot alone", in: "."},
		{name: "a space", in: "living room"},
		{name: "a control character", in: "tv\x07"},
		{name: "invalid UTF-8", in: "tv\xff"},
		{name: "over 253 bytes", in: string(make([]byte, 254))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := cleanName(tt.in)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEnrichAsksEachLookupInTurn(t *testing.T) {
	t.Parallel()

	replies := map[netip.Addr]time.Duration{netip.MustParseAddr("127.0.0.1"): time.Millisecond}

	silent := func([]byte) ([]byte, bool) { return nil, false }

	desc := descriptionServer(t, deviceDescription("Living Room TV"))
	ssdpAnswers := ssdpReply(desc.URL + "/description.xml")

	tests := []struct {
		name         string
		mdns         func([]byte) ([]byte, bool)
		netbios      func([]byte) ([]byte, bool)
		ssdp         func([]byte) ([]byte, bool)
		off          bool
		wantName     string
		wantSource   dbtype.HostnameSource
		wantNetBIOSQ int32
		wantSSDPQ    int32
	}{
		{
			name:       "mDNS answers, so NetBIOS and SSDP are not asked",
			mdns:       mdnsReply("tv.local."),
			netbios:    netbiosReply("TV"),
			ssdp:       ssdpAnswers,
			wantName:   "tv.local",
			wantSource: dbtype.HostnameFromMDNS,
		},
		{
			name:         "mDNS is silent, so NetBIOS is asked",
			mdns:         silent,
			netbios:      netbiosReply("DESKTOP-4F2K"),
			ssdp:         ssdpAnswers,
			wantName:     "DESKTOP-4F2K",
			wantSource:   dbtype.HostnameFromNetBIOS,
			wantNetBIOSQ: 1,
		},
		{
			name:         "mDNS and NetBIOS are silent, so SSDP is asked",
			mdns:         silent,
			netbios:      silent,
			ssdp:         ssdpAnswers,
			wantName:     "Living Room TV",
			wantSource:   dbtype.HostnameFromSSDP,
			wantNetBIOSQ: 1,
			wantSSDPQ:    1,
		},
		{
			name:    "all turned off",
			mdns:    mdnsReply("tv.local."),
			netbios: netbiosReply("TV"),
			ssdp:    ssdpAnswers,
			off:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newResponder(t, "127.0.0.1:0", nil, tt.mdns)
			n := newResponder(t, "127.0.0.1:0", nil, tt.netbios)
			g := newResponder(t, "127.0.0.1:0", nil, tt.ssdp)

			s := New(slog.New(slog.DiscardHandler),
				WithNameResolution(false),
				WithMACResolution(false),
				WithMDNSResolution(!tt.off),
				WithNetBIOSResolution(!tt.off),
				WithSSDPResolution(!tt.off),
			)
			s.mdnsPort = m.port()
			s.netbiosPort = n.port()
			s.ssdpGroup = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), g.port())

			found := s.enrich(t.Context(), replies, time.Now())
			require.Len(t, found, 1)

			assert.Equal(t, tt.wantName, found[0].Hostname())
			assert.Equal(t, tt.wantSource, found[0].NameSource)
			assert.Equal(t, tt.wantNetBIOSQ, n.queries.Load())
			assert.Equal(t, tt.wantSSDPQ, g.queries.Load())
		})
	}
}
