package mcp

import (
	"log/slog"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// recordRouter records a router read that learned the printer on ether4 in
// VLAN 10, on a gigabit link that came up at 100 Mbps, and the nas on its
// radio.
func recordRouter(t *testing.T, store *inventory.Store) {
	t.Helper()

	require.NoError(t, store.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, plugin.Topology{
		Identity: "router",
		Gateway:  true,
		Own:      []string{"00:00:5e:00:53:a0"},
		Ports: []plugin.TopologyPort{
			{
				Name: "ether4", Kind: plugin.PortWired, PVID: 10, Untagged: []int{10}, Running: true,
				Rate: 100_000_000, Capable: 1_000_000_000, FullDuplex: true,
			},
			{Name: "wifi1", Kind: plugin.PortWiFi, PVID: 10, Running: true},
		},
		Seen: []plugin.Sighting{
			{Port: "ether4", MAC: macA, VLAN: 10},
			{
				Port: "wifi1", MAC: macB, VLAN: 10, WiFi: true, SSID: "home", Band: "5ghz-ax",
				TxRate: 866_700_000, RxRate: 650_000_000, Signal: -58,
			},
		},
		ReadAt: time.Now(),
	}))
}

func TestGetDeviceSaysWhereTheDeviceIsConnected(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	recordRouter(t, store)

	cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
		listDevices(store)(s, log)
		getDevice(store)(s, log)
	})

	t.Run("a wired device, on a slow link", func(t *testing.T) {
		t.Parallel()

		id := deviceID(t, cs, "printer")
		c := decodeAs[getDeviceOutput](t, callTool(t, cs, "get_device", map[string]any{"id": id})).Connection
		require.NotNil(t, c)

		assert.True(t, c.Placed)
		assert.True(t, c.Current)
		assert.Equal(t, []hop{{Name: "router", Kind: "read"}}, c.Path)
		assert.Equal(t, "ether4", c.Port)
		assert.Equal(t, []int{10}, c.VLANs)
		assert.False(t, c.WiFi)
		assert.Nil(t, c.Radio)

		require.NotNil(t, c.Link)
		assert.Equal(t, link{RateBps: 100_000_000, CapableBps: 1_000_000_000, FullDuplex: true, Slow: true}, *c.Link)
	})

	t.Run("a Wi-Fi device, with its rates and signal", func(t *testing.T) {
		t.Parallel()

		id := deviceID(t, cs, "nas")
		c := decodeAs[getDeviceOutput](t, callTool(t, cs, "get_device", map[string]any{"id": id})).Connection
		require.NotNil(t, c)

		assert.True(t, c.Placed)
		assert.True(t, c.WiFi)
		assert.Equal(t, "home", c.SSID)
		assert.Equal(t, "5ghz-ax", c.Band)
		assert.Nil(t, c.Link)

		require.NotNil(t, c.Radio)
		assert.Equal(t, radio{DownBps: 866_700_000, UpBps: 650_000_000, SignalDBm: -58}, *c.Radio)
	})
}

func TestGetDeviceLeavesTheConnectionOut(t *testing.T) {
	t.Parallel()

	t.Run("before any router is read", func(t *testing.T) {
		t.Parallel()

		store := seededStore(t)
		cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
			listDevices(store)(s, log)
			getDevice(store)(s, log)
		})

		id := deviceID(t, cs, "printer")
		out := decodeAs[getDeviceOutput](t, callTool(t, cs, "get_device", map[string]any{"id": id}))
		assert.Nil(t, out.Connection)
	})

	// The tree leaves ignored devices out, so saying the device was never
	// seen on a port would be wrong.
	t.Run("for an ignored device", func(t *testing.T) {
		t.Parallel()

		store := seededStore(t)
		recordRouter(t, store)

		cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
			listDevices(store)(s, log)
			getDevice(store)(s, log)
		})

		id := deviceID(t, cs, "printer")
		_, err := store.UpdateCuration(t.Context(), id, inventory.Curation{Ignored: true})
		require.NoError(t, err)

		out := decodeAs[getDeviceOutput](t, callTool(t, cs, "get_device", map[string]any{"id": id}))
		assert.Nil(t, out.Connection)
	})
}

// A device the router has not seen on any port is reported as not placed,
// which is different from nothing being read.
func TestGetDeviceSaysWhenNoPortHasSeenTheDevice(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	require.NoError(t, store.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, plugin.Topology{
		Identity: "router",
		Gateway:  true,
		Seen:     []plugin.Sighting{{Port: "ether4", MAC: macA}},
		ReadAt:   time.Now(),
	}))

	cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
		listDevices(store)(s, log)
		getDevice(store)(s, log)
	})

	id := deviceID(t, cs, "nas")
	out := decodeAs[getDeviceOutput](t, callTool(t, cs, "get_device", map[string]any{"id": id}))

	require.NotNil(t, out.Connection)
	assert.Equal(t, connection{}, *out.Connection)
}

func TestGetTopology(t *testing.T) {
	t.Parallel()

	t.Run("before any router is read", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[getTopologyOutput](t, callTool(t, connect(t, getTopology(seededStore(t))), "get_topology", nil))

		assert.False(t, out.Recorded)
		assert.Empty(t, out.Nodes)
		assert.NotNil(t, out.Devices, "an empty list should still be a list, not null")
	})

	// The router learned the printer on ether4 and the nas behind the switch
	// that announced itself on sfp1, so the nas hangs from the switch.
	t.Run("each node after its parent, with the devices on it", func(t *testing.T) {
		t.Parallel()

		store := seededStore(t)
		require.NoError(t, store.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, plugin.Topology{
			Identity: "router",
			Gateway:  true,
			Own:      []string{"00:00:5e:00:53:a0"},
			Ports: []plugin.TopologyPort{
				{Name: "ether4", Kind: plugin.PortWired, PVID: 10, Untagged: []int{10}, Running: true},
				{
					Name: "sfp1", Kind: plugin.PortWired, Tagged: []int{10, 20}, Running: true,
					Rate: 10_000_000_000, FullDuplex: true,
				},
			},
			Seen: []plugin.Sighting{
				{Port: "ether4", MAC: macA, VLAN: 10},
				{Port: "sfp1", MAC: macB, VLAN: 20},
				{Port: "sfp1", MAC: "00:00:5e:00:53:b0", VLAN: 10},
			},
			Neighbours: []plugin.Neighbour{{Port: "sfp1", MAC: "00:00:5e:00:53:b0", Identity: "switch-a", Board: "CRS326"}},
			ReadAt:     time.Now(),
		}))

		out := decodeAs[getTopologyOutput](t, callTool(t, connect(t, getTopology(store)), "get_topology", nil))

		assert.True(t, out.Recorded)
		require.Len(t, out.Nodes, 2)

		router, sw := out.Nodes[0], out.Nodes[1]
		assert.Equal(t, "router", router.Name)
		assert.Equal(t, "read", router.Kind)
		assert.Empty(t, router.Parent)

		assert.Equal(t, "switch-a", sw.Name)
		assert.Equal(t, "seen", sw.Kind)
		assert.Equal(t, "CRS326", sw.Board)
		assert.Equal(t, router.Key, sw.Parent)
		assert.Equal(t, "sfp1", sw.ParentPort)
		assert.Equal(t, []int{10, 20}, sw.VLANs)
		assert.True(t, sw.Trunk)
		require.NotNil(t, sw.Link)
		assert.Equal(t, int64(10_000_000_000), sw.Link.RateBps)

		require.Len(t, out.Devices, 2)
		assert.Equal(t, "printer.local", out.Devices[0].Name)
		assert.Equal(t, router.Key, out.Devices[0].Node)
		assert.Equal(t, "ether4", out.Devices[0].Port)
		assert.True(t, out.Devices[0].Current)

		assert.Equal(t, "nas.local", out.Devices[1].Name)
		assert.Equal(t, sw.Key, out.Devices[1].Node)
		assert.Equal(t, []int{20}, out.Devices[1].VLANs)
		assert.Nil(t, out.Devices[1].Link, "the router's port speed is the switch's link")

		assert.Empty(t, out.Unplaced)
	})

	t.Run("an online device no port has seen", func(t *testing.T) {
		t.Parallel()

		store := seededStore(t)
		require.NoError(t, store.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, plugin.Topology{
			Identity: "router",
			Gateway:  true,
			Seen:     []plugin.Sighting{{Port: "ether4", MAC: macA}},
			ReadAt:   time.Now(),
		}))

		out := decodeAs[getTopologyOutput](t, callTool(t, connect(t, getTopology(store)), "get_topology", nil))

		require.Len(t, out.Devices, 1)
		require.Len(t, out.Unplaced, 1)
		assert.Equal(t, "nas.local", out.Unplaced[0].Name)
	})
}
