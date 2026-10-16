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

// The router, a device behind it, the outside address of the router, and a host
// on the internet that tries things.
const (
	probeRouter  = "192.0.2.1"
	probeDevice  = "192.0.2.10"
	probeOutside = "203.0.113.1"
	probeScanner = "1.1.1.1"
)

type probeRow struct {
	Device             int64
	Peer               string
	Protocol           int64
	Attempts, Answered int64
	Ports              string
	Outside            int64
}

func probeRows(t *testing.T, conn *sql.DB) []probeRow {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), `
		SELECT device_id, peer_ip, protocol, attempts, answered, ports, outside
		FROM probes_hourly
		ORDER BY device_id, peer_ip, protocol`)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var out []probeRow

	for rows.Next() {
		var r probeRow
		require.NoError(t, rows.Scan(&r.Device, &r.Peer, &r.Protocol, &r.Attempts, &r.Answered, &r.Ports, &r.Outside))
		out = append(out, r)
	}

	require.NoError(t, rows.Err())

	return out
}

// exported marks flows as the router at probeRouter reported them.
func exported(flows ...plugin.Flow) []plugin.Flow {
	for i := range flows {
		flows[i].Exporter = netip.MustParseAddr(probeRouter)
	}

	return flows
}

// outbound is the device that reaches the internet through the NAT of the
// router. It names the outside address with which it left.
func outbound(at time.Time) plugin.Flow {
	f := flow(probeDevice, "8.8.8.8", 51000, 443, 2_000, at)
	f.NATSrc = netip.MustParseAddr(probeOutside)

	return f
}

func probeStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	s, conn := newStore(t)
	sweep(t, s, host(probeRouter, macA, ""), host(probeDevice, macB, ""))

	return s, conn
}

// A knock on the outside address goes under the router, as a probe from the
// internet. A knock on a port forwarded to a device goes under that device.
// The answer of the knock counts as for any attempt.
func TestTheInternetsTriesOnTheNetworkAreProbes(t *testing.T) {
	t.Parallel()

	s, conn := probeStore(t)
	at := s.now()

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, exported(outbound(at)))
	rec.Add(trafficSource{}, exported(
		knock(probeScanner, probeOutside, 22, "", at)[0],
		knock(probeScanner, probeOutside, 23, "", at)[0],
	))
	rec.Add(trafficSource{}, exported(knock(probeScanner, probeDevice, 443, "closed", at)...))
	require.NoError(t, rec.Flush(t.Context()))

	assert.Equal(t, []probeRow{
		{Device: 1, Peer: probeScanner, Protocol: protoTCP, Attempts: 2, Answered: 0, Ports: "22,23", Outside: 1},
		{Device: 2, Peer: probeScanner, Protocol: protoTCP, Attempts: 1, Answered: 0, Ports: "443", Outside: 0},
	}, probeRows(t, conn))

	assert.Empty(t, attemptRows(t, conn), "no device made them")
}

// An answer of a device one flush after the probe marks the probe as answered.
// A late answer to the own knock of a device does the same.
func TestAProbeAnsweredInTheNextFlushIsAnswered(t *testing.T) {
	t.Parallel()

	s, conn := probeStore(t)
	flows := exported(knock(probeScanner, probeDevice, 443, "open", s.now())...)

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, flows[:1])
	require.NoError(t, rec.Flush(t.Context()))
	rec.Add(trafficSource{}, flows[1:])
	require.NoError(t, rec.Flush(t.Context()))

	rows := probeRows(t, conn)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1), rows[0].Answered)
}

// Real traffic with the outside address, such as a VPN that ends on the router,
// belongs to the router. The peer started it.
func TestTrafficWithTheOutsideAddressIsTheRouters(t *testing.T) {
	t.Parallel()

	s, conn := probeStore(t)
	at := s.now()

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, exported(
		outbound(at),
		flow(probeScanner, probeOutside, 40000, 51820, 90_000, at),
		flow(probeOutside, probeScanner, 51820, 40000, 70_000, at),
	))
	require.NoError(t, rec.Flush(t.Context()))

	var in, out, connsIn int64

	err := conn.QueryRowContext(t.Context(), `
		SELECT bytes_in, bytes_out, connections_in FROM traffic_hourly
		WHERE device_id = 1 AND peer_ip = ? AND service_port = 51820`, probeScanner).Scan(&in, &out, &connsIn)
	require.NoError(t, err)
	assert.Equal(t, int64(90_000), in)
	assert.Equal(t, int64(70_000), out)
	assert.Equal(t, int64(1), connsIn)
}

// Until an outbound flow names the outside address, nothing is known as that
// address. A knock on it has no device for the count. Also, a knock from a
// local address is not a probe from the internet.
func TestNoProbesWithoutAnOutsideAddressOrFromTheNetwork(t *testing.T) {
	t.Parallel()

	s, conn := probeStore(t)
	at := s.now()

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, exported(
		knock(probeScanner, probeOutside, 22, "", at)[0],
		knock("192.0.2.99", probeDevice, 22, "", at)[0],
	))
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, probeRows(t, conn))
}

func TestPruneDeletesProbesWithTraffic(t *testing.T) {
	t.Parallel()

	s, conn := probeStore(t)
	old := s.now().Add(-48 * time.Hour)

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, exported(knock(probeScanner, probeDevice, 443, "", old)[0]))
	require.NoError(t, rec.Flush(t.Context()))
	require.Len(t, probeRows(t, conn), 1)

	res, err := s.Prune(t.Context(), Retention{Traffic: 24 * time.Hour})
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Probes)
	assert.Empty(t, probeRows(t, conn))
}

// The store keeps the outside addresses that the router named. So a view can
// tell a router with a public address from a router behind a different NAT.
func TestOutsideAddressesAreRecorded(t *testing.T) {
	t.Parallel()

	s, _ := probeStore(t)
	at := s.now()

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, exported(outbound(at), outbound(at)))
	require.NoError(t, rec.Flush(t.Context()))

	got, err := s.OutsideAddresses(t.Context(), at.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr(probeOutside)}, got)

	got, err = s.OutsideAddresses(t.Context(), at.Add(time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got, "not named since")
}
