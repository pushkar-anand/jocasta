package plugin

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every address in these packets is from a documentation range. Decoding is
// goflow2's and is tested there; these tests cover what this package adds --
// who may send, per-exporter templates, sampling, and the translation to
// Flow -- which needs packets whose every field the test chose.
var (
	nfSrc      = netip.MustParseAddr("192.0.2.10")
	nfDst      = netip.MustParseAddr("198.51.100.20")
	nfExporter = netip.MustParseAddr("127.0.0.1")
	nfExported = time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)
)

// be appends big-endian fields, which is every field on these wires.
type be []byte

func (b be) u8(v uint8) be   { return append(b, v) }
func (b be) u16(v uint16) be { return binary.BigEndian.AppendUint16(b, v) }
func (b be) u32(v uint32) be { return binary.BigEndian.AppendUint32(b, v) }
func (b be) u64(v uint64) be { return binary.BigEndian.AppendUint64(b, v) }

func (b be) ip4(a netip.Addr) be {
	v := a.As4()

	return append(b, v[:]...)
}

// u16len is a length for a 16-bit length field. Test packets are far below
// the limit.
func u16len(n int) uint16 { return uint16(n) } //nolint:gosec // see above.

func exportSecs() uint32 { return uint32(nfExported.Unix()) } //nolint:gosec // a 2026 timestamp fits.

// v5Packet is one NetFlow v5 datagram carrying a single TCP record from nfSrc
// port 51000 to nfDst port 443. The record's last-seen uptime equals the
// header's, so it ended exactly at the export time.
func v5Packet(sampling uint16) []byte {
	const uptime = 600_000

	return be{}.
		u16(5).u16(1).u32(uptime).u32(exportSecs()).u32(0). // version, count, uptime, secs, nsecs
		u32(1).u8(0).u8(0).u16(sampling).                   // sequence, engine type, engine id, sampling
		ip4(nfSrc).ip4(nfDst).ip4(netip.MustParseAddr("192.0.2.1")).
		u16(1).u16(2).                     // input, output interface
		u32(10).u32(1500).                 // packets, octets
		u32(uptime - 5000).u32(uptime).    // first, last
		u16(51000).u16(443).               // ports
		u8(0).u8(0x18).u8(6).u8(0).        // pad, TCP flags, protocol, TOS
		u16(0).u16(0).u8(24).u8(24).u16(0) // AS numbers, masks, pad
}

// ipfixPacket is one IPFIX message with a template set and a data set that
// uses it, as an exporter sends on first contact.
func ipfixPacket(templateID uint16) []byte {
	fields := [][2]uint16{
		{8, 4},   // sourceIPv4Address
		{12, 4},  // destinationIPv4Address
		{7, 2},   // sourceTransportPort
		{11, 2},  // destinationTransportPort
		{4, 1},   // protocolIdentifier
		{1, 8},   // octetDeltaCount
		{2, 8},   // packetDeltaCount
		{153, 8}, // flowEndMilliseconds
	}

	rec := be{}.ip4(nfSrc).ip4(nfDst).u16(51000).u16(443).u8(6).
		u64(2048).u64(4).u64(uint64(nfExported.UnixMilli())) //nolint:gosec // a 2026 timestamp is positive.

	return ipfixMessage(templateID, fields, rec)
}

// nfPublic is the router's public address in the NAT packets: what a reply
// from the internet is addressed to before the router translates it.
var nfPublic = netip.MustParseAddr("203.0.113.1")

// ipfixNATReply is the reply half of a conversation through a NATing router,
// laid out as a MikroTik exports it: addressed to the router's public address,
// delivered to postNAT, whose unspecified value means "not translated".
func ipfixNATReply(postNAT netip.Addr, postPort uint16) []byte {
	fields := [][2]uint16{
		{8, 4},   // sourceIPv4Address
		{12, 4},  // destinationIPv4Address
		{7, 2},   // sourceTransportPort
		{11, 2},  // destinationTransportPort
		{4, 1},   // protocolIdentifier
		{1, 8},   // octetDeltaCount
		{2, 8},   // packetDeltaCount
		{225, 4}, // postNATSourceIPv4Address
		{226, 4}, // postNATDestinationIPv4Address
		{227, 2}, // postNAPTSourceTransportPort
		{228, 2}, // postNAPTDestinationTransportPort
	}

	rec := be{}.ip4(nfDst).ip4(nfPublic).u16(443).u16(40000).u8(6).
		u64(9000).u64(9).
		ip4(nfDst).ip4(postNAT).u16(443).u16(postPort)

	return ipfixMessage(257, fields, rec)
}

// ipfixMessage wraps one template and one record using it into a message.
func ipfixMessage(templateID uint16, fields [][2]uint16, rec []byte) []byte {
	tmpl := be{}.u16(templateID).u16(u16len(len(fields)))
	for _, f := range fields {
		tmpl = tmpl.u16(f[0]).u16(f[1])
	}

	body := set(2, tmpl)
	body = append(body, set(templateID, rec)...)

	hdr := be{}.u16(10).u16(u16len(16 + len(body))).u32(exportSecs()).u32(1).u32(0)

	return append(hdr, body...)
}

// ipfixDataOnly is ipfixPacket without its template set: what an exporter
// sends once it believes the collector already knows the template.
func ipfixDataOnly(templateID uint16) []byte {
	full := ipfixPacket(templateID)
	tmplLen := int(binary.BigEndian.Uint16(full[16+2:]))

	out := append(be{}, full[:16]...)
	out = append(out, full[16+tmplLen:]...)
	binary.BigEndian.PutUint16(out[2:], u16len(len(out)))

	return out
}

// set wraps a body in a set header. The length field counts the header.
func set(id uint16, body []byte) be {
	return append(be{}.u16(id).u16(u16len(4+len(body))), body...)
}

// v9Uptime and v9SourceID are the header values of goflow2's template capture
// below; a data packet must carry the same source ID to find the template.
const (
	v9Uptime   = 0xb3bff683
	v9SourceID = 256
)

// v9Template is a NetFlow v9 template packet from a real exporter: 23 fields,
// template ID 260. It is copied from goflow2's decoder tests
// (decoders/netflow/netflow_test.go, BSD-3-Clause). A template carries field
// layouts and no addresses, so nothing about any network is in it.
var v9Template = []byte{
	0x00, 0x09, 0x00, 0x01, 0xb3, 0xbf, 0xf6, 0x83, 0x61, 0x8a, 0xa3, 0xa8, 0x32, 0x01, 0xee, 0x98,
	0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x64, 0x01, 0x04, 0x00, 0x17, 0x00, 0x02, 0x00, 0x04,
	0x00, 0x01, 0x00, 0x04, 0x00, 0x08, 0x00, 0x04, 0x00, 0x0c, 0x00, 0x04, 0x00, 0x0a, 0x00, 0x04,
	0x00, 0x0e, 0x00, 0x04, 0x00, 0x15, 0x00, 0x04, 0x00, 0x16, 0x00, 0x04, 0x00, 0x07, 0x00, 0x02,
	0x00, 0x0b, 0x00, 0x02, 0x00, 0x10, 0x00, 0x04, 0x00, 0x11, 0x00, 0x04, 0x00, 0x12, 0x00, 0x04,
	0x00, 0x09, 0x00, 0x01, 0x00, 0x0d, 0x00, 0x01, 0x00, 0x04, 0x00, 0x01, 0x00, 0x06, 0x00, 0x01,
	0x00, 0x05, 0x00, 0x01, 0x00, 0x3d, 0x00, 0x01, 0x00, 0x59, 0x00, 0x01, 0x00, 0x30, 0x00, 0x02,
	0x00, 0xea, 0x00, 0x04, 0x00, 0xeb, 0x00, 0x04,
}

// v9Data is one data packet in v9Template's layout: a UDP flow from nfSrc to
// nfDst port 53 whose LAST_SWITCHED equals the header uptime, so it ended at
// the export time.
func v9Data() []byte {
	rec := be{}.
		u32(2).u32(300). // IN_PKTS, IN_BYTES
		ip4(nfSrc).ip4(nfDst).
		u32(1).u32(2).                      // INPUT_SNMP, OUTPUT_SNMP
		u32(v9Uptime).u32(v9Uptime - 1000). // LAST_SWITCHED, FIRST_SWITCHED
		u16(51000).u16(53).
		u32(0).u32(0). // SRC_AS, DST_AS
		ip4(netip.MustParseAddr("192.0.2.1")).
		u8(24).u8(24).      // masks
		u8(17).u8(0).u8(0). // PROTOCOL, TCP_FLAGS, SRC_TOS
		u8(0).u8(0x40).     // DIRECTION, FORWARDING STATUS
		u16(0).             // FLOW_SAMPLER_ID
		u32(0).u32(0)       // fields 234, 235

	hdr := be{}.u16(9).u16(1).u32(v9Uptime).u32(exportSecs()).u32(2).u32(v9SourceID)

	return append(hdr, set(260, rec)...)
}

func testNetFlow(t *testing.T, exporters ...string) *NetFlow {
	t.Helper()

	if len(exporters) == 0 {
		exporters = []string{nfExporter.String()}
	}

	n, err := NewNetFlow("gateway", "127.0.0.1:0", exporters, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	return n
}

func TestNetFlowDecodesEachExportVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		packets [][]byte
		want    Flow
	}{
		{
			name:    "v5",
			packets: [][]byte{v5Packet(0)},
			want: Flow{Src: nfSrc, Dst: nfDst, SrcPort: 51000, DstPort: 443, Protocol: 6, TCPFlags: 0x18,
				Bytes: 1500, Packets: 10, End: nfExported},
		},
		{
			name:    "v9 with a real exporter's template",
			packets: [][]byte{v9Template, v9Data()},
			want: Flow{Src: nfSrc, Dst: nfDst, SrcPort: 51000, DstPort: 53, Protocol: 17,
				Bytes: 300, Packets: 2, End: nfExported},
		},
		{
			name:    "IPFIX",
			packets: [][]byte{ipfixPacket(256)},
			want: Flow{Src: nfSrc, Dst: nfDst, SrcPort: 51000, DstPort: 443, Protocol: 6,
				Bytes: 2048, Packets: 4, End: nfExported},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			n := testNetFlow(t)

			var flows []Flow

			for _, p := range tt.packets {
				got, err := n.decode(nfExporter, p, time.Now())
				require.NoError(t, err)

				flows = append(flows, got...)
			}

			require.Len(t, flows, 1)
			assert.Equal(t, tt.want, flows[0])
		})
	}
}

func TestNetFlowCreditsARepliedPacketToWhereNATDeliveredIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		postNAT  netip.Addr
		postPort uint16
		wantDst  netip.Addr
		wantPort uint16
	}{
		{"translated", nfSrc, 51000, nfSrc, 51000},
		{"not translated", netip.IPv4Unspecified(), 0, nfPublic, 40000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flows, err := testNetFlow(t).decode(nfExporter, ipfixNATReply(tt.postNAT, tt.postPort), nfExported)
			require.NoError(t, err)
			require.Len(t, flows, 1)

			assert.Equal(t, nfDst, flows[0].Src, "the source is never translated back")
			assert.Equal(t, tt.wantDst, flows[0].Dst)
			assert.Equal(t, tt.wantPort, flows[0].DstPort)
		})
	}
}

func TestNetFlowDropsItsOwnFeed(t *testing.T) {
	t.Parallel()

	n := testNetFlow(t)
	feed := Flow{Src: nfExporter, Dst: nfDst, SrcPort: 40000, DstPort: 2055, Protocol: 17}
	fromElsewhere := Flow{Src: nfSrc, Dst: nfDst, SrcPort: 40000, DstPort: 2055, Protocol: 17}
	otherPort := Flow{Src: nfExporter, Dst: nfDst, SrcPort: 40000, DstPort: 53, Protocol: 17}

	got := n.withoutOwnExports([]Flow{feed, fromElsewhere, otherPort}, 2055)

	assert.Equal(t, []Flow{fromElsewhere, otherPort}, got)
}

func TestNetFlowScalesBySamplingRate(t *testing.T) {
	t.Parallel()

	// v5 carries the rate in the low 14 bits of the header's sampling field;
	// the top two are the sampling mode.
	flows, err := testNetFlow(t).decode(nfExporter, v5Packet(0x4000|100), time.Now())
	require.NoError(t, err)
	require.Len(t, flows, 1)
	assert.Equal(t, uint64(150_000), flows[0].Bytes)
	assert.Equal(t, uint64(1000), flows[0].Packets)
}

func TestNetFlowKeepsTemplatesPerExporter(t *testing.T) {
	t.Parallel()

	n := testNetFlow(t)

	_, err := n.decode(nfExporter, ipfixPacket(256), time.Now())
	require.NoError(t, err)

	// The second exporter never sent template 256, so its data set must not
	// decode using the first exporter's.
	flows, err := n.decode(netip.MustParseAddr("192.0.2.1"), ipfixDataOnly(256), time.Now())
	if err == nil {
		assert.Empty(t, flows)
	}

	flows, err = n.decode(nfExporter, ipfixDataOnly(256), time.Now())
	require.NoError(t, err)
	assert.Len(t, flows, 1)
}

func TestNetFlowRejectsWhatItCannotDecode(t *testing.T) {
	t.Parallel()

	n := testNetFlow(t)

	_, err := n.decode(nfExporter, be{}.u16(7).u16(0), time.Now())
	require.Error(t, err, "unknown version")

	_, err = n.decode(nfExporter, []byte{5}, time.Now())
	require.Error(t, err, "too short for a version")
}

func TestNewNetFlowRefusesAnOpenListener(t *testing.T) {
	t.Parallel()

	_, err := NewNetFlow("gateway", ":2055", nil, nil)
	require.ErrorIs(t, err, ErrNoExporters)
}

func TestNewNetFlowRefusesAnExporterThatIsNotAnAddress(t *testing.T) {
	t.Parallel()

	_, err := NewNetFlow("gateway", ":2055", []string{"router.example.com"}, nil)
	require.Error(t, err)
}

func TestNewNetFlowPrefixesTheInstanceName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "netflow:gateway", testNetFlow(t).Name())
}

// listening starts n on a loopback port and returns a connection to it, what
// it emits, and a stop that cancels it and returns what Listen returned.
func listening(t *testing.T, n *NetFlow) (net.Conn, <-chan []Flow, func() error) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	bound := make(chan net.Addr, 1)
	n.ready = func(a net.Addr) { bound <- a }

	got := make(chan []Flow, 8)
	done := make(chan error, 1)

	go func() {
		done <- n.Listen(ctx, func(_ context.Context, f []Flow) { got <- f })
	}()

	var addr net.Addr

	select {
	case addr = <-bound:
	case err := <-done:
		cancel()
		t.Fatalf("listener exited before binding: %v", err)
	}

	var d net.Dialer

	conn, err := d.DialContext(t.Context(), "udp", addr.String())
	require.NoError(t, err)

	var (
		once    sync.Once
		stopErr error
	)

	stop := func() error {
		once.Do(func() {
			cancel()

			stopErr = <-done
		})

		return stopErr
	}

	t.Cleanup(func() {
		_ = conn.Close()
		_ = stop()
	})

	return conn, got, stop
}

func TestNetFlowListenEmitsDecodedFlows(t *testing.T) {
	t.Parallel()

	conn, got, stop := listening(t, testNetFlow(t))

	_, err := conn.Write(ipfixPacket(256))
	require.NoError(t, err)

	select {
	case flows := <-got:
		require.Len(t, flows, 1)
		assert.Equal(t, nfDst, flows[0].Dst)
	case <-time.After(5 * time.Second):
		t.Fatal("no flows emitted")
	}

	require.NoError(t, stop(), "a cancelled listener returns nil")
}

func TestNetFlowListenDropsUnlistedSenders(t *testing.T) {
	t.Parallel()

	// Only a documentation address may send, so loopback is refused.
	conn, got, stop := listening(t, testNetFlow(t, "192.0.2.1"))

	_, err := conn.Write(ipfixPacket(256))
	require.NoError(t, err)

	select {
	case <-got:
		t.Fatal("a datagram from an unlisted sender was decoded")
	case <-time.After(200 * time.Millisecond):
	}

	require.NoError(t, stop())
}
