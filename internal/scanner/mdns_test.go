package scanner

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// responder answers every reverse query it receives with a PTR to name.
type responder struct {
	listen *net.UDPConn
	reply  *net.UDPConn
	name   string
}

// newResponder starts a responder on addr that answers with name, sending each
// answer from reply, or from the socket it listens on when reply is nil. Port 0
// picks a free port, which port reports.
func newResponder(t *testing.T, addr, name string, reply *net.UDPConn) *responder {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp4", addr)
	require.NoError(t, err)

	t.Cleanup(func() { _ = pc.Close() })

	r := &responder{listen: pc.(*net.UDPConn), reply: reply, name: name}
	if r.reply == nil {
		r.reply = r.listen
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

		var p dnsmessage.Parser

		if _, err := p.Start(buf[:n]); err != nil {
			continue
		}

		q, err := p.Question()
		if err != nil {
			continue
		}

		raw, err := ptrResponse(q.Name.String(), r.name)
		if err != nil {
			continue
		}

		_, _ = r.reply.WriteToUDPAddrPort(raw, from)
	}
}

// ptrResponse builds a response answering the reverse name q with a PTR to
// name.
func ptrResponse(q, name string) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})

	if err := b.StartAnswers(); err != nil {
		return nil, err
	}

	err := b.PTRResource(
		dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(q), Class: dnsmessage.ClassINET, TTL: 120},
		dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(name)},
	)
	if err != nil {
		return nil, err
	}

	return b.Finish()
}

func TestAskMDNSNamesTheHostsThatAnswer(t *testing.T) {
	t.Parallel()

	r := newResponder(t, "127.0.0.1:0", "tv.local.", nil)

	// 127.0.0.2 has nothing listening on the port, so it stays silent.
	addrs := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("127.0.0.2")}

	names, err := askMDNS(t.Context(), addrs, r.port(), 1000, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, map[netip.Addr]string{netip.MustParseAddr("127.0.0.1"): "tv.local"}, names)
}

func TestAskMDNSReturnsOnceEveryHostAnswered(t *testing.T) {
	t.Parallel()

	r := newResponder(t, "127.0.0.1:0", "tv.local.", nil)

	start := time.Now()

	names, err := askMDNS(t.Context(), []netip.Addr{netip.MustParseAddr("127.0.0.1")}, r.port(), 1000, 10*time.Second)
	require.NoError(t, err)

	assert.Len(t, names, 1)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestAskMDNSIgnoresAnAnswerFromAnotherAddress(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	other, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.3:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = other.Close() })

	r := newResponder(t, "127.0.0.1:0", "tv.local.", other.(*net.UDPConn))

	names, err := askMDNS(t.Context(), []netip.Addr{netip.MustParseAddr("127.0.0.1")}, r.port(), 1000, 200*time.Millisecond)
	require.NoError(t, err)

	assert.Empty(t, names)
}

func TestAskMDNSStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()

	// Nothing listens on port 9 of a loopback address, so no answer comes.
	names, err := askMDNS(ctx, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, 9, 1000, 10*time.Second)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, names)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestAskMDNSSkipsIPv6(t *testing.T) {
	t.Parallel()

	names, err := askMDNS(t.Context(), []netip.Addr{netip.MustParseAddr("2001:db8::1")}, 9, 1000, 10*time.Second)
	require.NoError(t, err)

	assert.Empty(t, names)
}

func TestReverseName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "10.2.0.192.in-addr.arpa.", reverseName(netip.MustParseAddr("192.0.2.10")))
}

func TestParseReverseAnswer(t *testing.T) {
	t.Parallel()

	const q = "10.2.0.192.in-addr.arpa."

	// answer builds a response with one record of type typ for name.
	answer := func(t *testing.T, name string, typ dnsmessage.Type, ptr string) []byte {
		t.Helper()

		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
		require.NoError(t, b.StartAnswers())

		h := dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Class: dnsmessage.ClassINET}

		switch typ {
		case dnsmessage.TypePTR:
			require.NoError(t, b.PTRResource(h, dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(ptr)}))
		case dnsmessage.TypeA:
			require.NoError(t, b.AResource(h, dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}}))
		}

		raw, err := b.Finish()
		require.NoError(t, err)

		return raw
	}

	query, err := reverseQuery(q)
	require.NoError(t, err)

	tests := []struct {
		name   string
		msg    []byte
		want   string
		wantOK bool
	}{
		{name: "a PTR for the asked name", msg: answer(t, q, dnsmessage.TypePTR, "tv.local."), want: "tv.local", wantOK: true},
		{name: "the asked name in another case", msg: answer(t, "10.2.0.192.IN-ADDR.ARPA.", dnsmessage.TypePTR, "tv.local."), want: "tv.local", wantOK: true},
		{name: "a PTR for another address", msg: answer(t, "11.2.0.192.in-addr.arpa.", dnsmessage.TypePTR, "tv.local.")},
		{name: "an A record only", msg: answer(t, q, dnsmessage.TypeA, "")},
		{name: "a name with a space", msg: answer(t, q, dnsmessage.TypePTR, "living room.local.")},
		{name: "a name with a control character", msg: answer(t, q, dnsmessage.TypePTR, "tv\x07.local.")},
		{name: "the root name", msg: answer(t, q, dnsmessage.TypePTR, ".")},
		{name: "a query", msg: query},
		{name: "a truncated message", msg: answer(t, q, dnsmessage.TypePTR, "tv.local.")[:20]},
		{name: "nothing", msg: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseReverseAnswer(tt.msg, q)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestEnrichNamesNamelessHostsOverMDNS changes mdnsPort, so it does not run
// in parallel.
func TestEnrichNamesNamelessHostsOverMDNS(t *testing.T) {
	r := newResponder(t, "127.0.0.1:0", "tv.local.", nil)

	saved := mdnsPort
	mdnsPort = r.port()

	t.Cleanup(func() { mdnsPort = saved })

	replies := map[netip.Addr]time.Duration{netip.MustParseAddr("127.0.0.1"): time.Millisecond}

	tests := []struct {
		name       string
		mdns       bool
		wantName   string
		wantSource dbtype.HostnameSource
	}{
		{name: "asked", mdns: true, wantName: "tv.local", wantSource: dbtype.HostnameFromMDNS},
		{name: "turned off", mdns: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(slog.New(slog.DiscardHandler),
				WithNameResolution(false),
				WithMACResolution(false),
				WithMDNSResolution(tt.mdns),
			)

			found := s.enrich(t.Context(), replies, time.Now())
			require.Len(t, found, 1)

			assert.Equal(t, tt.wantName, found[0].Hostname())
			assert.Equal(t, tt.wantSource, found[0].NameSource)
		})
	}
}
