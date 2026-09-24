package inventory

import (
	"database/sql"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/plugin"
)

type broadcastRow struct {
	Device         int64
	Dst, Kind      string
	Protocol, Port int64
	Bytes, Packets int64
}

func broadcastRows(t *testing.T, conn *sql.DB) []broadcastRow {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), `
		SELECT device_id, dst_ip, kind, protocol, port, bytes, packets
		FROM broadcasts_hourly
		ORDER BY device_id, dst_ip, protocol, port`)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var out []broadcastRow

	for rows.Next() {
		var r broadcastRow
		require.NoError(t, rows.Scan(&r.Device, &r.Dst, &r.Kind, &r.Protocol, &r.Port, &r.Bytes, &r.Packets))
		out = append(out, r)
	}

	require.NoError(t, rows.Err())

	return out
}

func udp(src, dst string, srcPort, dstPort uint16, bytes uint64, at time.Time) plugin.Flow {
	return plugin.Flow{
		Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: srcPort, DstPort: dstPort, Protocol: protoUDP, Bytes: bytes, Packets: 1, End: at,
	}
}

// What a device sends to everyone is kept, per group and port, instead of as
// a conversation with an address nobody holds: to a multicast group, to
// 255.255.255.255, and to its subnet's broadcast address, which only the
// recorded networks tell apart from a host.
func TestBroadcastsAreRecordedPerGroupAndPort(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	at := s.now()

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		udp("192.0.2.10", "224.0.0.251", 5353, 5353, 300, at),
		udp("192.0.2.10", "224.0.0.251", 5353, 5353, 200, at),
		udp("192.0.2.10", "255.255.255.255", 68, 67, 330, at),
		// Both ways, as a router exported a discovery protocol's broadcasts:
		// the one from the broadcast address is not anyone talking.
		udp("192.0.2.10", "192.0.2.255", 21027, 21027, 150, at),
		udp("192.0.2.255", "192.0.2.10", 21027, 21027, 150, at),
	})
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, trafficRows(t, conn), "none of it is a conversation")
	assert.Equal(t, []broadcastRow{
		{Device: 1, Dst: "192.0.2.255", Kind: "subnet", Protocol: protoUDP, Port: 21027, Bytes: 150, Packets: 1},
		{Device: 1, Dst: "224.0.0.251", Kind: "multicast", Protocol: protoUDP, Port: 5353, Bytes: 500, Packets: 2},
		{Device: 1, Dst: "255.255.255.255", Kind: "all", Protocol: protoUDP, Port: 67, Bytes: 330, Packets: 1},
	}, broadcastRows(t, conn))

	// The hour's totals grow with each flush.
	rec.Add(trafficSource{}, []plugin.Flow{udp("192.0.2.10", "224.0.0.251", 5353, 5353, 100, at)})
	require.NoError(t, rec.Flush(t.Context()))

	rows := broadcastRows(t, conn)
	require.Len(t, rows, 3)
	assert.Equal(t, int64(600), rows[1].Bytes)
	assert.Equal(t, int64(3), rows[1].Packets)
}

// A sender that is no device, or has no address yet -- a DHCP client asking
// for one -- cannot be filed under anything in the inventory.
func TestBroadcastsFromNoDeviceAreNotKept(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	at := s.now()

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		udp("192.0.2.99", "224.0.0.251", 5353, 5353, 300, at),
		udp("0.0.0.0", "255.255.255.255", 68, 67, 330, at),
	})
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, broadcastRows(t, conn))
}

func TestPruneDeletesBroadcastsWithTraffic(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{udp("192.0.2.10", "224.0.0.251", 5353, 5353, 300, s.now().Add(-48*time.Hour))})
	require.NoError(t, rec.Flush(t.Context()))
	require.Len(t, broadcastRows(t, conn), 1)

	res, err := s.Prune(t.Context(), 0, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Broadcasts)
	assert.Empty(t, broadcastRows(t, conn))
}

func TestNetworksBroadcast(t *testing.T) {
	t.Parallel()

	ns := networks{
		{id: 1, prefix: netip.MustParsePrefix("192.0.2.0/24")},
		{id: 2, prefix: netip.MustParsePrefix("198.51.100.0/31")},
		{id: 3, prefix: netip.MustParsePrefix("2001:db8::/64")},
	}

	for addr, want := range map[string]bool{
		"192.0.2.255":       true,
		"192.0.2.254":       false,
		"192.0.2.0":         false,
		"198.51.100.1":      false, // a /31 has no broadcast address
		"203.0.113.255":     false, // on no recorded network
		"2001:db8::ffff:ff": false,
	} {
		assert.Equal(t, want, ns.broadcast(netip.MustParseAddr(addr)), addr)
	}
}
