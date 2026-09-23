package inventory

import (
	"context"
	"database/sql"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// trafficSource stands in for a traffic plugin: the recorder only needs to
// know what to file the flows under.
type trafficSource struct{}

func (trafficSource) Name() string            { return "netflow:test" }
func (trafficSource) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

var trafficHour = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func flow(src, dst string, srcPort, dstPort uint16, bytes uint64, at time.Time) plugin.Flow {
	return plugin.Flow{
		Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: srcPort, DstPort: dstPort, Protocol: 6,
		Bytes: bytes, Packets: bytes / 100, End: at,
	}
}

type trafficRow struct {
	Device, PeerDevice   int64
	Peer, PeerName       string
	Service              int64
	Out, In, Connections int64
	Hour                 string
}

func trafficRows(t *testing.T, conn *sql.DB) []trafficRow {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), `
		SELECT device_id, COALESCE(peer_device_id, 0), peer_ip, COALESCE(peer_name, ''),
		       service_port, bytes_out, bytes_in, connections, hour
		FROM traffic_hourly
		ORDER BY device_id, hour, peer_ip, service_port`)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var out []trafficRow

	for rows.Next() {
		var r trafficRow
		require.NoError(t, rows.Scan(&r.Device, &r.PeerDevice, &r.Peer, &r.PeerName,
			&r.Service, &r.Out, &r.In, &r.Connections, &r.Hour))
		out = append(out, r)
	}

	require.NoError(t, rows.Err())

	return out
}

func newRecorder(s *Store, resolve func(context.Context, netip.Addr) string) *TrafficRecorder {
	return NewTrafficRecorder(s, slog.New(slog.DiscardHandler), resolve)
}

func TestTrafficLandsOnEachDeviceInTheConversation(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	a, b := deviceIDByMAC(t, conn, macA), deviceIDByMAC(t, conn, macB)
	rec := newRecorder(s, nil)

	rec.Add(trafficSource{}, []plugin.Flow{
		// A connects to B's SSH, and B replies.
		flow("192.0.2.10", "192.0.2.11", 51000, 22, 1000, trafficHour.Add(5*time.Minute)),
		flow("192.0.2.11", "192.0.2.10", 22, 51000, 4000, trafficHour.Add(5*time.Minute)),
		// Two addresses no device holds: nothing to record.
		flow("198.51.100.1", "198.51.100.2", 51000, 443, 999, trafficHour),
	})
	require.NoError(t, rec.Flush(t.Context()))

	hour := trafficHour.Format(dbtype.Layout)

	assert.Equal(t, []trafficRow{
		{Device: a, PeerDevice: b, Peer: "192.0.2.11", Service: 22, Out: 1000, In: 4000, Connections: 1, Hour: hour},
		{Device: b, PeerDevice: a, Peer: "192.0.2.10", Service: 22, Out: 4000, In: 1000, Connections: 0, Hour: hour},
	}, trafficRows(t, conn))
}

func TestTrafficSkipsBroadcastAndMulticast(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	rec := newRecorder(s, nil)

	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "255.255.255.255", 9999, 9999, 57, trafficHour),
		flow("192.0.2.10", "224.0.0.251", 5353, 5353, 90, trafficHour),
		flow("192.0.2.10", "ff02::fb", 5353, 5353, 90, trafficHour),
		flow("0.0.0.0", "192.0.2.10", 68, 67, 300, trafficHour),
	})
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, trafficRows(t, conn))
}

// The unanswered addresses are private-use because that is what the rule is
// about: a sweep of the home network's own ranges. They are picked from the
// top of 10/8, which no router hands out by default.
func TestTrafficDropsUnansweredProbesOfLocalAddressesNoDeviceHolds(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	a := deviceIDByMAC(t, conn, macA)
	rec := newRecorder(s, nil)

	ping := func(src, dst string) plugin.Flow {
		f := flow(src, dst, 0, 0, 84, trafficHour)
		f.Protocol = 1

		return f
	}

	rec.Add(trafficSource{}, []plugin.Flow{
		// Nothing answers at .1 or .2.
		ping("192.0.2.10", "10.255.255.1"),
		ping("192.0.2.10", "10.255.255.2"),
		// .3 does, so it is a peer even though no device holds it.
		ping("192.0.2.10", "10.255.255.3"),
		ping("10.255.255.3", "192.0.2.10"),
		// Off the local network, an unanswered connection is kept.
		flow("192.0.2.10", "203.0.113.5", 51000, 443, 60, trafficHour),
	})
	require.NoError(t, rec.Flush(t.Context()))

	hour := trafficHour.Format(dbtype.Layout)

	assert.Equal(t, []trafficRow{
		{Device: a, Peer: "10.255.255.3", Out: 84, In: 84, Connections: 1, Hour: hour},
		{Device: a, Peer: "203.0.113.5", Service: 443, Out: 60, Connections: 1, Hour: hour},
	}, trafficRows(t, conn))
}

func TestTrafficAddsUpAcrossFlushes(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	rec := newRecorder(s, nil)

	for range 3 {
		rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.10", "203.0.113.5", 51000, 443, 100, trafficHour)})
		require.NoError(t, rec.Flush(t.Context()))
	}

	rows := trafficRows(t, conn)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(300), rows[0].Out)
	assert.Equal(t, int64(3), rows[0].Connections)
	assert.Equal(t, int64(443), rows[0].Service)
}

func TestTrafficCountsEachFlowInTheHourItEnded(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "203.0.113.5", 51000, 443, 100, trafficHour.Add(59*time.Minute)),
		flow("192.0.2.10", "203.0.113.5", 51000, 443, 200, trafficHour.Add(61*time.Minute)),
	})
	require.NoError(t, rec.Flush(t.Context()))

	rows := trafficRows(t, conn)
	require.Len(t, rows, 2)
	assert.Equal(t, trafficHour.Format(dbtype.Layout), rows[0].Hour)
	assert.Equal(t, trafficHour.Add(time.Hour).Format(dbtype.Layout), rows[1].Hour)
}

// The reason traffic is resolved to a device when it arrives: the address is a
// lease, and history recorded under it belongs to whoever held it then.
func TestTrafficStaysWithTheDeviceAfterItsAddressMoves(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	a := deviceIDByMAC(t, conn, macA)
	rec := newRecorder(s, nil)

	rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.10", "203.0.113.5", 51000, 443, 100, trafficHour)})
	require.NoError(t, rec.Flush(t.Context()))

	// A moves, and another device takes its old address.
	sweep(t, s, host("192.0.2.20", macA, ""), host("192.0.2.10", macB, ""))

	b := deviceIDByMAC(t, conn, macB)

	rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.20", "203.0.113.5", 51000, 443, 50, trafficHour)})
	rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.10", "203.0.113.5", 51000, 443, 7, trafficHour)})
	require.NoError(t, rec.Flush(t.Context()))

	total := map[int64]int64{}
	for _, r := range trafficRows(t, conn) {
		total[r.Device] += r.Out
	}

	assert.Equal(t, map[int64]int64{a: 150, b: 7}, total)
}

func TestTrafficNamesPeersOnceAndOnlyAcrossFromADevice(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	var lookups atomic.Int32

	rec := newRecorder(s, func(_ context.Context, a netip.Addr) string {
		lookups.Add(1)

		if a == netip.MustParseAddr("203.0.113.5") {
			return "cdn.example.com"
		}

		return ""
	})

	for range 2 {
		rec.Add(trafficSource{}, []plugin.Flow{
			flow("192.0.2.10", "203.0.113.5", 51000, 443, 100, trafficHour),
			flow("198.51.100.1", "198.51.100.2", 51000, 443, 100, trafficHour),
		})
		require.NoError(t, rec.Flush(t.Context()))
	}

	rows := trafficRows(t, conn)
	require.Len(t, rows, 1)
	assert.Equal(t, "cdn.example.com", rows[0].PeerName)
	assert.Equal(t, int32(1), lookups.Load(), "cached after the first flush, never asked about the unrelated pair")
}

func TestTrafficDropsFlowsPastTheBufferCap(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	rec := newRecorder(s, nil)

	flows := make([]plugin.Flow, 0, maxPendingTraffic+10)
	for i := range maxPendingTraffic + 10 {
		flows = append(flows, flow("192.0.2.10", "203.0.113.5", uint16(i%60000)+1024, 443, 1, //nolint:gosec // bounded above.
			trafficHour.Add(time.Duration(i/60000)*time.Hour)))
	}

	rec.Add(trafficSource{}, flows)

	rec.mu.Lock()
	defer rec.mu.Unlock()

	assert.LessOrEqual(t, len(rec.pending), maxPendingTraffic)
}

func TestPruneDeletesTrafficPastItsOwnRetention(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	rec := newRecorder(s, nil)
	start := s.now().UTC().Truncate(time.Hour)

	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "203.0.113.5", 51000, 443, 100, start),
		flow("192.0.2.10", "203.0.113.5", 51000, 443, 100, start.Add(48*time.Hour)),
	})
	require.NoError(t, rec.Flush(t.Context()))

	advance(49 * time.Hour)

	res, err := s.Prune(t.Context(), 0, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Traffic)
	assert.Zero(t, res.Events, "zero retention keeps events")
	assert.Len(t, trafficRows(t, conn), 1)
}

func TestServicePortPicksTheServerSide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		protocol uint8
		src, dst uint16
		want     uint16
	}{
		{"client to a known service", 6, 51000, 443, 443},
		{"reply from a known service", 6, 443, 51000, 443},
		{"known over unknown, both high", 6, 40000, 32400, 32400},
		{"privileged over unprivileged", 17, 20002, 999, 999},
		{"outside the ephemeral range over inside it", 6, 50000, 12345, 12345},
		{"lower when nothing decides", 6, 20001, 20000, 20000},
		{"a client's source port that happens to be known", 17, 51820, 7777, 7777},
		{"a server on a known port inside the ephemeral range", 17, 45000, 51820, 51820},
		{"a privileged client port to a known service", 6, 700, 2049, 2049},
		{"ICMP has no ports", 1, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, servicePort(tt.protocol, tt.src, tt.dst))
		})
	}
}

func TestDeviceTrafficSplitsLocalFromInternetByOrganisation(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, "nas.example"))

	a, b := deviceIDByMAC(t, conn, macA), deviceIDByMAC(t, conn, macB)
	now := s.now()

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "192.0.2.11", 51000, 445, 5000, now),
		// A local address no device holds stays local.
		flow("192.0.2.10", "192.0.2.99", 51000, 80, 10, now),
		// Two addresses one organisation announces fold into one entry.
		// These are public resolvers, the addresses the asn tests use too.
		flow("192.0.2.10", "1.1.1.1", 51000, 443, 700, now),
		flow("192.0.2.10", "1.0.0.1", 51000, 443, 300, now),
		// Outside the window.
		flow("192.0.2.10", "1.1.1.1", 51000, 443, 999_999, now.Add(-72*time.Hour)),
	})
	require.NoError(t, rec.Flush(t.Context()))

	got, err := s.DeviceTraffic(t.Context(), a, now.Add(-24*time.Hour))
	require.NoError(t, err)

	require.Len(t, got.Local, 2)
	assert.Equal(t, b, got.Local[0].DeviceID)
	assert.Equal(t, "nas.example", got.Local[0].DeviceName)
	assert.Equal(t, int64(5000), got.Local[0].Sent)
	assert.Equal(t, "smb", got.Local[0].Service)
	assert.Zero(t, got.Local[1].DeviceID)
	assert.Equal(t, "192.0.2.99", got.Local[1].IP.String())

	require.Len(t, got.Internet, 1)
	org := got.Internet[0]
	assert.Equal(t, uint32(13335), org.ASN)
	assert.Equal(t, "Cloudflare", org.Short)
	assert.Equal(t, int64(1000), org.Sent)
	assert.Len(t, org.Peers, 2)

	// The other side sees the same conversation as received.
	other, err := s.DeviceTraffic(t.Context(), b, now.Add(-24*time.Hour))
	require.NoError(t, err)
	require.Len(t, other.Local, 1)
	assert.Equal(t, int64(5000), other.Local[0].Received)

	recorded, err := s.TrafficRecorded(t.Context())
	require.NoError(t, err)
	assert.True(t, recorded)
}

func TestDeviceTrafficIsEmptyNotNil(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	got, err := s.DeviceTraffic(t.Context(), 1, s.now())
	require.NoError(t, err)
	assert.True(t, got.Empty())
	assert.NotNil(t, got.Local)
	assert.NotNil(t, got.Internet)

	recorded, err := s.TrafficRecorded(t.Context())
	require.NoError(t, err)
	assert.False(t, recorded)
}

func TestNetworkTrafficSummaries(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)
	sweep(t, s, host("192.0.2.10", macA, "laptop.example"), host("192.0.2.11", macB, "tv.example"))

	a, b := deviceIDByMAC(t, conn, macA), deviceIDByMAC(t, conn, macB)
	rec := newRecorder(s, nil)

	// Ten days ago the laptop already talked to Cloudflare.
	rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.10", "1.1.1.1", 51000, 443, 100, s.now())})
	require.NoError(t, rec.Flush(t.Context()))

	advance(10 * 24 * time.Hour)

	now := s.now()

	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "1.1.1.1", 51000, 443, 2_000, now),
		flow("192.0.2.10", "8.8.8.8", 51000, 53, 50, now),
		flow("192.0.2.11", "8.8.8.8", 51000, 443, 7_000, now),
	})
	require.NoError(t, rec.Flush(t.Context()))

	busiest, err := s.BusiestDevices(t.Context(), now.Add(-24*time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, busiest, 2)
	assert.Equal(t, b, busiest[0].DeviceID)
	assert.Equal(t, "tv.example", busiest[0].DeviceName)
	assert.Equal(t, int64(7_000), busiest[0].Sent)

	orgs, err := s.TopOrganisations(t.Context(), now.Add(-24*time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, orgs, 2)
	assert.Equal(t, "Google", orgs[0].Short)
	assert.Equal(t, int64(2), orgs[0].Devices)
	assert.Equal(t, "Cloudflare", orgs[1].Short)

	first, err := s.FirstContacts(t.Context(), now.Add(-7*24*time.Hour), 10)
	require.NoError(t, err)
	assert.False(t, first.Partial, "ten days of records cover a seven-day look back")

	// Cloudflare is not new to the laptop; Google is new to both.
	got := map[int64][]string{}
	for _, c := range first.Contacts {
		got[c.DeviceID] = append(got[c.DeviceID], c.Short)
	}

	assert.Equal(t, map[int64][]string{a: {"Google"}, b: {"Google"}}, got)

	// An ignored device drops out of every summary.
	_, err = s.UpdateCuration(t.Context(), b, Curation{Ignored: true})
	require.NoError(t, err)

	busiest, err = s.BusiestDevices(t.Context(), now.Add(-24*time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, busiest, 1)
	assert.Equal(t, a, busiest[0].DeviceID)
}

func TestFirstContactsSaysWhenRecordsAreShorterThanTheLookBack(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.10", "1.1.1.1", 51000, 443, 100, s.now())})
	require.NoError(t, rec.Flush(t.Context()))

	first, err := s.FirstContacts(t.Context(), s.now().Add(-7*24*time.Hour), 10)
	require.NoError(t, err)
	assert.True(t, first.Partial)
	assert.False(t, first.Started.IsZero())
	assert.Len(t, first.Contacts, 1)
}
