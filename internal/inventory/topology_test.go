package inventory

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/topology"
)

// switchRead is one read of a switch: a trunk to the router, a desktop on
// ether4 with a gigabit link, a Wi-Fi client, and the router announcing
// itself on the trunk.
func switchRead(at time.Time) plugin.Topology {
	return plugin.Topology{
		Identity: "switch-a",
		Own:      []string{"00:00:5e:00:53:a1"},
		Ports: []plugin.TopologyPort{
			{
				Name: "ether4", Kind: plugin.PortWired, PVID: 10, Untagged: []int{10}, Running: true,
				Rate: 1_000_000_000, Capable: 1_000_000_000, FullDuplex: true,
			},
			{Name: "sfp1", Kind: plugin.PortWired, PVID: 1, Tagged: []int{10, 20}, Running: true},
		},
		Seen: []plugin.Sighting{
			{Port: "ether4", MAC: macA, VLAN: 10},
			{
				Port: "wifi1", MAC: macB, VLAN: 20, WiFi: true, SSID: "iot", Band: "2ghz-ax",
				TxRate: 28_900_000, RxRate: 54_000_000, Signal: -55,
			},
		},
		Neighbours: []plugin.Neighbour{{
			Port: "sfp1", MAC: "00:00:5e:00:53:c1", Addr: netip.MustParseAddr("192.0.2.1"),
			Identity: "router", Platform: "MikroTik", Board: "RB5009", TheirPort: "ether3",
		}},
		ReadAt: at,
	}
}

func TestRecordTopologyFilesEveryTable(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

	require.NoError(t, s.RecordTopology(t.Context(), "routeros:switch_a", dbtype.SourceRouter, switchRead(at)))

	nodes, err := s.q.ListTopologyNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "routeros:switch_a", nodes[0].SourceName)
	assert.Equal(t, "switch-a", nodes[0].Identity.String)
	assert.False(t, nodes[0].Gateway)
	assert.Equal(t, "00:00:5e:00:53:a1", nodes[0].OwnMacs)
	assert.True(t, at.Equal(nodes[0].ReadAt.Time))

	ports, err := s.q.ListTopologyPorts(t.Context())
	require.NoError(t, err)
	require.Len(t, ports, 2)
	assert.Equal(t, "sfp1", ports[1].Name)
	assert.Equal(t, "10,20", ports[1].Tagged)
	assert.Empty(t, ports[1].Untagged)

	assert.Equal(t, int64(2), countRows(t, conn, `SELECT COUNT(*) FROM topology_sightings`))
	assert.Equal(t, int64(1), countRows(t, conn,
		`SELECT COUNT(*) FROM topology_sightings WHERE mac = ? AND wifi = 1 AND ssid = 'iot' AND band = '2ghz-ax'`, macB))

	ns, err := s.q.ListTopologyNeighbours(t.Context())
	require.NoError(t, err)
	require.Len(t, ns, 1)
	assert.Equal(t, "router", ns[0].Identity)
	assert.Equal(t, "192.0.2.1", ns[0].Address.String)
	assert.Equal(t, "ether3", ns[0].TheirPort.String)
}

// A second read replaces the ports and neighbours, moves the sightings it
// repeats on, and keeps the one it no longer lists.
func TestRecordTopologyReplacesPortsAndKeepsSightings(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	first := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Minute)

	require.NoError(t, s.RecordTopology(t.Context(), "routeros:switch_a", dbtype.SourceRouter, switchRead(first)))

	again := switchRead(second)
	again.Ports = again.Ports[:1]
	again.Seen = again.Seen[:1]
	again.Neighbours = nil

	require.NoError(t, s.RecordTopology(t.Context(), "routeros:switch_a", dbtype.SourceRouter, again))

	assert.Equal(t, int64(1), countRows(t, conn, `SELECT COUNT(*) FROM topology_ports`))
	assert.Zero(t, countRows(t, conn, `SELECT COUNT(*) FROM topology_neighbours`))

	sightings, err := s.q.ListTopologySightings(t.Context())
	require.NoError(t, err)
	require.Len(t, sightings, 2)

	for _, seen := range sightings {
		assert.True(t, first.Equal(seen.FirstSeen.Time), seen.MAC)

		want := first
		if seen.MAC == macA {
			want = second
		}

		assert.True(t, want.Equal(seen.LastSeen.Time), seen.MAC)
	}
}

func TestLastTopologyReadAt(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	_, err := s.LastTopologyReadAt(t.Context())
	require.ErrorIs(t, err, ErrNotFound)

	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	require.NoError(t, s.RecordTopology(t.Context(), "routeros:switch_a", dbtype.SourceRouter, switchRead(at)))
	require.NoError(t, s.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, switchRead(at.Add(time.Minute))))

	got, err := s.LastTopologyReadAt(t.Context())
	require.NoError(t, err)
	assert.True(t, at.Add(time.Minute).Equal(got))
}

// A sighting older than the history window goes; the node that made it stays.
func TestPruneDeletesSightingsPastRetention(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	require.NoError(t, s.RecordTopology(t.Context(), "routeros:switch_a", dbtype.SourceRouter, switchRead(time.Time{})))

	advance(testRetention + time.Hour)

	res, err := s.Prune(t.Context(), Retention{History: testRetention})
	require.NoError(t, err)

	assert.Equal(t, int64(2), res.Sightings)
	assert.Zero(t, countRows(t, conn, `SELECT COUNT(*) FROM topology_sightings`))
	assert.Equal(t, int64(1), countRows(t, conn, `SELECT COUNT(*) FROM topology_nodes`))
}

// A recorded read comes back as a tree, with a swept device placed on the port
// the read learned it on.
func TestTopologyPlacesARecordedDevice(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	at := time.Now().UTC()
	read := switchRead(at)
	read.Gateway = true

	require.NoError(t, s.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, read))

	tree, err := s.Topology(t.Context())
	require.NoError(t, err)
	require.NotNil(t, tree.Root)
	assert.Equal(t, "switch-a", tree.Root.Name)

	id := deviceIDByMAC(t, conn, macA)

	leaf, ok := tree.Leaf(id)
	require.True(t, ok)
	assert.Equal(t, "ether4", leaf.Port)
	assert.Equal(t, 10, leaf.VLAN)
	assert.True(t, leaf.Current)
	assert.Same(t, tree.Root, leaf.Owner)
	assert.Equal(t, topology.Speed{Rate: 1_000_000_000, Capable: 1_000_000_000, FullDuplex: true}, leaf.Speed)
}

// A Wi-Fi client's rates and signal come back as its speed.
func TestTopologyKeepsAWiFiClientsRates(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.11", macB, "phone.local"))

	read := switchRead(time.Now().UTC())
	read.Gateway = true

	require.NoError(t, s.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, read))

	tree, err := s.Topology(t.Context())
	require.NoError(t, err)

	leaf, ok := tree.Leaf(deviceIDByMAC(t, conn, macB))
	require.True(t, ok)
	assert.Equal(t, topology.Radio{Down: 28_900_000, Up: 54_000_000, Signal: -55}, leaf.Radio)
}
