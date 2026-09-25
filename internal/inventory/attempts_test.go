package inventory

import (
	"database/sql"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// knock is one TCP connection attempt from src to dst's port and what came
// back: a SYN and ACK when the port was open, a RST when it was closed, and
// nothing when a firewall dropped it.
func knock(src, dst string, port uint16, reply string, at time.Time) []plugin.Flow {
	out := []plugin.Flow{{
		Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: 50000, DstPort: port, Protocol: protoTCP, TCPFlags: tcpSYN,
		Bytes: 60, Packets: 1, End: at,
	}}

	flags := map[string]uint8{"open": tcpSYN | tcpACK, "closed": 0x14} // RST|ACK
	if f, ok := flags[reply]; ok {
		out = append(out, plugin.Flow{
			Src: netip.MustParseAddr(dst), Dst: netip.MustParseAddr(src),
			SrcPort: port, DstPort: 50000, Protocol: protoTCP, TCPFlags: f,
			Bytes: 40, Packets: 1, End: at,
		})
	}

	return out
}

func ping(src, dst string, answered bool, at time.Time) []plugin.Flow {
	out := []plugin.Flow{{
		Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		Protocol: protoICMP, ICMPType: icmpEchoRequest, Bytes: 84, Packets: 1, End: at,
	}}

	if answered {
		out = append(out, plugin.Flow{
			Src: netip.MustParseAddr(dst), Dst: netip.MustParseAddr(src),
			Protocol: protoICMP, ICMPType: icmpEchoReply, Bytes: 84, Packets: 1, End: at,
		})
	}

	return out
}

type attemptRow struct {
	Device             int64
	Peer               string
	Protocol           int64
	Attempts, Answered int64
	PortCount          int64
	Ports              string
}

func attemptRows(t *testing.T, conn *sql.DB) []attemptRow {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), `
		SELECT device_id, peer_ip, protocol, attempts, answered, port_count, ports
		FROM attempts_hourly
		ORDER BY device_id, peer_ip, protocol`)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var out []attemptRow

	for rows.Next() {
		var r attemptRow
		require.NoError(t, rows.Scan(&r.Device, &r.Peer, &r.Protocol, &r.Attempts, &r.Answered, &r.PortCount, &r.Ports))
		out = append(out, r)
	}

	require.NoError(t, rows.Err())

	return out
}

func TestAPortScanIsOneAttemptRowNotATrafficRowPerPort(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	a, b := deviceIDByMAC(t, conn, macA), deviceIDByMAC(t, conn, macB)
	at := s.now()

	var flows []plugin.Flow

	for port := uint16(1); port <= 25; port++ {
		reply := "closed"
		if port == 22 || port == 23 || port == 25 {
			reply = "open"
		}

		flows = append(flows, knock("192.0.2.10", "192.0.2.11", port, reply, at)...)
	}

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, flows)
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, trafficRows(t, conn), "no port gets a conversation of its own")
	assert.Equal(t, []attemptRow{{
		Device: a, Peer: "192.0.2.11", Protocol: protoTCP,
		Attempts: 25, Answered: 3, PortCount: 25,
		Ports: "1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20",
	}}, attemptRows(t, conn))

	probers, err := s.ProbingDevices(t.Context(), at.Add(-time.Hour), "")
	require.NoError(t, err)
	require.Len(t, probers, 1)
	assert.Equal(t, a, probers[0].DeviceID)
	assert.Equal(t, int64(25), probers[0].MaxPorts)
	assert.Equal(t, int64(1), probers[0].Peers)

	attempts, err := s.DeviceAttempts(t.Context(), a, at.Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, b, attempts[0].PeerDeviceID)
	assert.Len(t, attempts[0].Ports, attemptPortSample)
}

func TestAPingSweepFlagsTheDeviceAndKeepsEveryAddressItTried(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	a := deviceIDByMAC(t, conn, macA)
	at := s.now()

	var flows []plugin.Flow

	// Nothing answers but every tenth address; no device holds any of them.
	for i := 1; i <= 30; i++ {
		flows = append(flows, ping("192.0.2.10", fmt.Sprintf("198.51.100.%d", i), i%10 == 0, at)...)
	}

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, flows)
	require.NoError(t, rec.Flush(t.Context()))

	rows := attemptRows(t, conn)
	require.Len(t, rows, 30)

	var answered int64

	for _, r := range rows {
		assert.Equal(t, a, r.Device)
		assert.Equal(t, int64(1), r.Attempts)
		assert.Empty(t, r.Ports, "a ping has no port")

		answered += r.Answered
	}

	assert.Equal(t, int64(3), answered)
	assert.Empty(t, trafficRows(t, conn), "a ping carries no data")

	probers, err := s.ProbingDevices(t.Context(), at.Add(-time.Hour), "")
	require.NoError(t, err)
	require.Len(t, probers, 1)
	assert.Equal(t, int64(30), probers[0].Peers)
	assert.Equal(t, int64(3), probers[0].Answered)
}

func TestConversationsAreNotAttempts(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	at := s.now()
	a := netip.MustParseAddr("192.0.2.10")
	b := netip.MustParseAddr("192.0.2.11")

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		// A connection that carried data, flagged as a MikroTik reports it:
		// the first packet's flags only, so no PSH anywhere.
		{Src: a, Dst: b, SrcPort: 50001, DstPort: 445, Protocol: protoTCP, TCPFlags: tcpSYN, Bytes: 9000, Packets: 12, End: at},
		{Src: b, Dst: a, SrcPort: 445, DstPort: 50001, Protocol: protoTCP, TCPFlags: tcpSYN | tcpACK, Bytes: 90000, Packets: 70, End: at},
		// One small request and its answer: handshake, a little data, close.
		{Src: a, Dst: b, SrcPort: 50005, DstPort: 80, Protocol: protoTCP, TCPFlags: tcpSYN, Bytes: 256, Packets: 4, End: at},
		{Src: b, Dst: a, SrcPort: 80, DstPort: 50005, Protocol: protoTCP, TCPFlags: tcpSYN | tcpACK, Bytes: 256, Packets: 4, End: at},
		// A long connection that only traded keepalives this minute: no SYN.
		{Src: a, Dst: b, SrcPort: 50002, DstPort: 22, Protocol: protoTCP, TCPFlags: tcpACK, Bytes: 52, Packets: 1, End: at},
		// A DNS question and its answer.
		{Src: a, Dst: b, SrcPort: 50003, DstPort: 53, Protocol: protoUDP, Bytes: 70, Packets: 1, End: at},
		{Src: b, Dst: a, SrcPort: 53, DstPort: 50003, Protocol: protoUDP, Bytes: 120, Packets: 1, End: at},
		// One-way UDP that is a stream.
		{Src: a, Dst: b, SrcPort: 50004, DstPort: 514, Protocol: protoUDP, Bytes: 9000, Packets: 40, End: at},
	})
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, attemptRows(t, conn))

	services := map[int64]bool{}
	for _, r := range trafficRows(t, conn) {
		services[r.Service] = true
	}

	assert.Equal(t, map[int64]bool{445: true, 80: true, 22: true, 53: true, 514: true}, services)
}

func TestAConnectionOpenedAndClosedAtOnceIsAnAnsweredAttempt(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	at := s.now()
	a := netip.MustParseAddr("192.0.2.10")
	b := netip.MustParseAddr("192.0.2.11")

	// A scanner's connect(): SYN, ACK, FIN, ACK out; SYN-ACK, FIN, ACK back.
	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		{Src: a, Dst: b, SrcPort: 50000, DstPort: 8080, Protocol: protoTCP, TCPFlags: tcpSYN, Bytes: 216, Packets: 4, End: at},
		{Src: b, Dst: a, SrcPort: 8080, DstPort: 50000, Protocol: protoTCP, TCPFlags: tcpSYN | tcpACK, Bytes: 164, Packets: 3, End: at},
	})
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, trafficRows(t, conn))

	rows := attemptRows(t, conn)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1), rows[0].Attempts)
	assert.Equal(t, int64(1), rows[0].Answered)
}

func TestAnUnansweredUDPDatagramIsAnAttempt(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{{
		Src: netip.MustParseAddr("192.0.2.10"), Dst: netip.MustParseAddr("198.51.100.9"),
		SrcPort: 50000, DstPort: 161, Protocol: protoUDP, Bytes: 80, Packets: 1, End: s.now(),
	}})
	require.NoError(t, rec.Flush(t.Context()))

	rows := attemptRows(t, conn)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(0), rows[0].Answered)
	assert.Equal(t, "161", rows[0].Ports)
}

// The router exports each direction as its own flow, and a knock's answer can
// land a flush after the knock. The knock is counted then as unanswered; its
// answer is not a conversation, and an open port marks the knock answered.
func TestAKnockAnsweredInTheNextFlushIsNotTraffic(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		reply    string
		answered int64
	}{
		{reply: "closed", answered: 0},
		{reply: "open", answered: 1},
	} {
		t.Run(tt.reply, func(t *testing.T) {
			t.Parallel()

			s, conn := newStore(t)
			sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

			flows := knock("192.0.2.10", "192.0.2.11", 22, tt.reply, s.now())

			rec := newRecorder(s, nil)
			rec.Add(trafficSource{}, flows[:1])
			require.NoError(t, rec.Flush(t.Context()))
			rec.Add(trafficSource{}, flows[1:])
			require.NoError(t, rec.Flush(t.Context()))

			assert.Empty(t, trafficRows(t, conn), "neither half is a conversation")

			rows := attemptRows(t, conn)
			require.Len(t, rows, 1)
			assert.Equal(t, int64(1), rows[0].Attempts)
			assert.Equal(t, tt.answered, rows[0].Answered)
		})
	}
}

// A ping's reply can land a flush after the ping, as a knock's can. The ping
// is counted then as unanswered, and the reply marks it answered.
func TestAPingAnsweredInTheNextFlushIsAnswered(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	flows := ping("192.0.2.10", "192.0.2.11", true, s.now())

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, flows[:1])
	require.NoError(t, rec.Flush(t.Context()))
	rec.Add(trafficSource{}, flows[1:])
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, trafficRows(t, conn))

	rows := attemptRows(t, conn)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1), rows[0].Attempts)
	assert.Equal(t, int64(1), rows[0].Answered)
}

// An answer whose knock is not on record, because it fell in the previous hour
// or before this process started, has nothing to mark and is dropped.
func TestALateAnswerWithNoKnockOnRecordIsDropped(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, knock("192.0.2.10", "192.0.2.11", 22, "open", s.now())[1:])
	require.NoError(t, rec.Flush(t.Context()))

	assert.Empty(t, trafficRows(t, conn))
	assert.Empty(t, attemptRows(t, conn))
}

// The router answers DHCP by broadcast, or from itself, and exports neither,
// so a renewal is a one-way request every time. It counts as a conversation;
// one-way UDP to another of the router's ports is still a try.
func TestDHCPIsNeverAnAttempt(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	udp := func(src, dst uint16) plugin.Flow {
		return plugin.Flow{
			Src: netip.MustParseAddr("192.0.2.10"), Dst: netip.MustParseAddr("192.0.2.1"),
			SrcPort: src, DstPort: dst, Protocol: protoUDP, Bytes: 330, Packets: 1, End: s.now(),
		}
	}

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{udp(68, 67), udp(546, 547), udp(50000, 161)})
	require.NoError(t, rec.Flush(t.Context()))

	rows := attemptRows(t, conn)
	require.Len(t, rows, 1)
	assert.Equal(t, "161", rows[0].Ports)

	var services []int64
	for _, r := range trafficRows(t, conn) {
		services = append(services, r.Service)
	}

	assert.ElementsMatch(t, []int64{67, 546}, services, "DHCP and DHCPv6 are kept as traffic")
}

func TestAttemptPortsAddUpAcrossFlushesAndRestarts(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	at := s.now()
	scan := func(rec *TrafficRecorder, ports ...uint16) {
		var flows []plugin.Flow
		for _, p := range ports {
			flows = append(flows, knock("192.0.2.10", "192.0.2.11", p, "closed", at)...)
		}

		rec.Add(trafficSource{}, flows)
		require.NoError(t, rec.Flush(t.Context()))
	}

	rec := newRecorder(s, nil)
	scan(rec, 80, 443)
	scan(rec, 443, 8080) // 443 again: tried twice, one distinct port.

	// A new process remembers nothing, but the stored sample and count stay.
	scan(newRecorder(s, nil), 22)

	rows := attemptRows(t, conn)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(5), rows[0].Attempts)
	assert.Equal(t, int64(4), rows[0].PortCount, "the restarted process remembers nothing, but the merged sample shows 22 was new")
	assert.Equal(t, "22,80,443,8080", rows[0].Ports)
}

func TestAFewNeighboursAreNotProbing(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	at := s.now()

	var flows []plugin.Flow
	for i := 1; i <= ProbeMinPeers-1; i++ {
		flows = append(flows, ping("192.0.2.10", fmt.Sprintf("198.51.100.%d", i), false, at)...)
	}

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, flows)
	require.NoError(t, rec.Flush(t.Context()))

	probers, err := s.ProbingDevices(t.Context(), at.Add(-time.Hour), "")
	require.NoError(t, err)
	assert.Empty(t, probers)
}

func TestProbingDevicesNarrowsToAGroup(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	a := deviceIDByMAC(t, conn, macA)
	at := s.now()

	var flows []plugin.Flow
	for i := 1; i <= ProbeMinPeers; i++ {
		flows = append(flows, ping("192.0.2.10", fmt.Sprintf("198.51.100.%d", i), false, at)...)
	}

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, flows)
	require.NoError(t, rec.Flush(t.Context()))

	_, err := s.UpdateCuration(t.Context(), a, Curation{Group: "tv"})
	require.NoError(t, err)

	got, err := s.ProbingDevices(t.Context(), at.Add(-time.Hour), "tv")
	require.NoError(t, err)
	assert.Len(t, got, 1)

	got, err = s.ProbingDevices(t.Context(), at.Add(-time.Hour), "office")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDeviceProbingIsThatDevicesEntry(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	at := s.now()

	var flows []plugin.Flow
	for i := 1; i <= ProbeMinPeers; i++ {
		flows = append(flows, ping("192.0.2.10", fmt.Sprintf("198.51.100.%d", i), false, at)...)
	}

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, flows)
	require.NoError(t, rec.Flush(t.Context()))

	a, b := deviceIDByMAC(t, conn, macA), deviceIDByMAC(t, conn, macB)

	got, err := s.DeviceProbing(t.Context(), a, at.Add(-time.Hour))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, a, got.DeviceID)

	got, err = s.DeviceProbing(t.Context(), b, at.Add(-time.Hour))
	require.NoError(t, err)
	assert.Nil(t, got, "the other device probed nothing")
}

func TestPruneDeletesAttemptsWithTraffic(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, ping("192.0.2.10", "198.51.100.1", false, s.now()))
	require.NoError(t, rec.Flush(t.Context()))

	advance(49 * time.Hour)

	res, err := s.Prune(t.Context(), 0, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Attempts)
	assert.Empty(t, attemptRows(t, conn))
}
