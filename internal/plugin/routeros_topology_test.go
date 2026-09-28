package plugin

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// switchA is a switch as its tables describe it: an uplink trunk on sfp1, a
// desktop on ether4 in VLAN 10, a radio with one client in VLAN 20, and the
// router it hangs from announcing itself on the uplink.
var switchA = switchTables{
	identity: "switch-a",
	ifaces: []routeros.Interface{
		{Name: "sfp1", Type: "ether", MACAddress: "00:00:5E:00:53:A1", Running: true},
		{Name: "ether4", Type: "ether", MACAddress: "00:00:5E:00:53:A4", Running: true},
		{Name: "ether5", Type: "ether", MACAddress: "00:00:5E:00:53:A5"},
		{Name: "wifi1", Type: "wifi", MACAddress: "00:00:5E:00:53:AF", Running: true},
		{Name: "bridge", Type: "bridge", MACAddress: "00:00:5E:00:53:A1", Running: true},
		{Name: "off", Type: "ether", MACAddress: "00:00:5E:00:53:A9", Disabled: true},
	},
	ports: []routeros.BridgePort{
		{Interface: "sfp1", Bridge: "bridge", PVID: "1"},
		{Interface: "ether4", Bridge: "bridge", PVID: "10"},
		{Interface: "wifi1", Bridge: "bridge", PVID: "20"},
	},
	vlans: []routeros.BridgeVLAN{
		{VLANIDs: "10", CurrentTagged: "bridge,sfp1", CurrentUntagged: "ether4"},
		{VLANIDs: "20", CurrentTagged: "bridge,sfp1", CurrentUntagged: "wifi1"},
		{VLANIDs: "30", CurrentTagged: "sfp1", Disabled: true},
	},
	hosts: []routeros.BridgeHost{
		{MACAddress: "00:00:5E:00:53:A1", OnInterface: "bridge", Local: true},
		{MACAddress: "00:00:5E:00:53:01", OnInterface: "sfp1", VID: "10"},
		{MACAddress: "00:00:5E:00:53:01", OnInterface: "sfp1", VID: "20"},
		{MACAddress: "00:00:5e:00:53:10", OnInterface: "ether4", VID: "10"},
		{MACAddress: "00:00:5E:00:53:20", OnInterface: "wifi1", VID: "20"},
		{MACAddress: "00:00:5E:00:53:99", OnInterface: "ether4", Invalid: true},
		{MACAddress: "not-a-mac", OnInterface: "ether4"},
	},
	neighbors: []routeros.Neighbor{
		{Interface: "sfp1,bridge", MACAddress: "00:00:5E:00:53:01", Address4: "192.0.2.1",
			Identity: "router", Platform: "MikroTik", Board: "RB5009", InterfaceName: "ether3"},
	},
	regs: []routeros.Registration{
		{Interface: "wifi1", MACAddress: "00:00:5E:00:53:20", SSID: "iot", Band: "2ghz-ax"},
		// Joined, but has sent nothing the bridge learned.
		{Interface: "wifi1", MACAddress: "00:00:5E:00:53:21", SSID: "iot", Band: "2ghz-ax"},
	},
}

func TestBuildTopologyNamesTheDevice(t *testing.T) {
	t.Parallel()

	r := testRouterOS(t)
	r.topologyOnly = true

	got := r.buildTopology(t.Context(), switchA)

	assert.Equal(t, "switch-a", got.Identity)
	assert.False(t, got.Gateway, "a topology-only source is not the gateway")
	assert.Equal(t, pinned, got.ReadAt)
	assert.Equal(t, []string{
		"00:00:5e:00:53:a1", "00:00:5e:00:53:a4", "00:00:5e:00:53:a5",
		"00:00:5e:00:53:a9", "00:00:5e:00:53:af",
	}, got.Own)
}

func TestBuildTopologyMarksTheRouterAsTheGateway(t *testing.T) {
	t.Parallel()

	got := testRouterOS(t).buildTopology(t.Context(), switchTables{})
	assert.True(t, got.Gateway)
}

func TestBuildTopologyListsPortsWithTheirVLANs(t *testing.T) {
	t.Parallel()

	got := testRouterOS(t).buildTopology(t.Context(), switchA)

	assert.Equal(t, []TopologyPort{
		{Name: "ether4", Kind: PortWired, PVID: 10, Untagged: []int{10}, Running: true},
		{Name: "ether5", Kind: PortWired},
		{Name: "sfp1", Kind: PortWired, PVID: 1, Tagged: []int{10, 20}, Running: true},
		{Name: "wifi1", Kind: PortWiFi, PVID: 20, Untagged: []int{20}, Running: true},
	}, got.Ports)
}

func TestBuildTopologyListsWhatEachPortLearned(t *testing.T) {
	t.Parallel()

	got := testRouterOS(t).buildTopology(t.Context(), switchA)

	assert.Equal(t, []Sighting{
		{Port: "ether4", MAC: "00:00:5e:00:53:10", VLAN: 10},
		{Port: "sfp1", MAC: "00:00:5e:00:53:01", VLAN: 10},
		{Port: "sfp1", MAC: "00:00:5e:00:53:01", VLAN: 20},
		{Port: "wifi1", MAC: "00:00:5e:00:53:20", VLAN: 20, WiFi: true, SSID: "iot", Band: "2ghz-ax"},
		{Port: "wifi1", MAC: "00:00:5e:00:53:21", VLAN: 20, WiFi: true, SSID: "iot", Band: "2ghz-ax"},
	}, got.Seen)
}

func TestBuildTopologyListsNeighbours(t *testing.T) {
	t.Parallel()

	got := testRouterOS(t).buildTopology(t.Context(), switchA)

	assert.Equal(t, []Neighbour{{
		Port: "sfp1", MAC: "00:00:5e:00:53:01", Addr: netip.MustParseAddr("192.0.2.1"),
		Identity: "router", Platform: "MikroTik", Board: "RB5009", TheirPort: "ether3",
	}}, got.Neighbours)
}

// A radio's address the bridge learned before the client list was read is
// still Wi-Fi, though nothing says which network it joined.
func TestBuildTopologyTakesARadioPortAsWiFi(t *testing.T) {
	t.Parallel()

	got := testRouterOS(t).buildTopology(t.Context(), switchTables{
		ifaces: []routeros.Interface{{Name: "wifi2", Type: "wifi"}},
		hosts:  []routeros.BridgeHost{{MACAddress: "00:00:5E:00:53:30", OnInterface: "wifi2"}},
	})

	assert.Equal(t, []Sighting{{Port: "wifi2", MAC: "00:00:5e:00:53:30", WiFi: true}}, got.Seen)
}

func TestTopologyReadsEveryTable(t *testing.T) {
	t.Parallel()

	r := switchServing(t, map[string]string{
		"/rest/system/identity":                   `{"name":"switch-a"}`,
		"/rest/interface":                         `[{"name":"ether4","type":"ether","mac-address":"00:00:5E:00:53:A4","running":"true"}]`,
		"/rest/interface/bridge/port":             `[{"interface":"ether4","bridge":"bridge","pvid":"10"}]`,
		"/rest/interface/bridge/vlan":             `[{"vlan-ids":"10","untagged":"ether4"}]`,
		"/rest/interface/bridge/host":             `[{"mac-address":"00:00:5E:00:53:10","on-interface":"ether4","vid":"10"}]`,
		"/rest/ip/neighbor":                       `[]`,
		"/rest/interface/wifi/registration-table": `[]`,
	})

	got, err := r.Topology(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "switch-a", got.Identity)
	assert.Equal(t, []string{"00:00:5e:00:53:a4"}, got.Own)
	assert.Equal(t, []TopologyPort{{Name: "ether4", Kind: PortWired, PVID: 10, Untagged: []int{10}, Running: true}}, got.Ports)
	assert.Equal(t, []Sighting{{Port: "ether4", MAC: "00:00:5e:00:53:10", VLAN: 10}}, got.Seen)
}

// The host table is the answer and the rest decorates it, so losing the
// decoration returns what arrived alongside the error.
func TestTopologyReturnsWhatItGotWhenATableFails(t *testing.T) {
	t.Parallel()

	r := switchServing(t, map[string]string{
		"/rest/interface/bridge/host": `[{"mac-address":"00:00:5E:00:53:10","on-interface":"ether4"}]`,
	})

	got, err := r.Topology(t.Context())
	require.Error(t, err)
	assert.Equal(t, []Sighting{{Port: "ether4", MAC: "00:00:5e:00:53:10"}}, got.Seen)
}

func TestTopologyReturnsNothingWhenEveryTableFails(t *testing.T) {
	t.Parallel()

	got, err := switchServing(t, nil).Topology(t.Context())
	require.Error(t, err)
	assert.Empty(t, got.Seen)
	assert.Empty(t, got.Identity)
}

// switchServing stands up a router answering each path in bodies. Any other
// path answers 500, which is how a failing table is arranged.
func switchServing(t *testing.T, bodies map[string]string) *RouterOS {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, ok := bodies[req.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err)

	p, err := strconv.Atoi(port)
	require.NoError(t, err)

	client, err := routeros.New(&routeros.Config{Host: host, Port: p}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	ros, err := NewRouterOS("switch_a", client, slog.New(slog.DiscardHandler), TopologyOnly())
	require.NoError(t, err)

	ros.now = func() time.Time { return pinned }

	return ros
}

// A router managing access points (CAPsMAN) lists their clients against the
// access point's radio, and learns them on the port the access point hangs
// from. The client is placed on that port and marked as Wi-Fi, and the radio
// is left out of the ports.
func TestBuildTopologyPlacesAManagedAccessPointsClientsOnItsPort(t *testing.T) {
	t.Parallel()

	got := testRouterOS(t).buildTopology(t.Context(), switchTables{
		ifaces: []routeros.Interface{
			{Name: "ether2", Type: "ether", Running: true},
			{Name: "cap-wifi1", Type: "wifi"},
		},
		hosts: []routeros.BridgeHost{{MACAddress: "00:00:5E:00:53:40", OnInterface: "ether2", VID: "10"}},
		regs:  []routeros.Registration{{Interface: "cap-wifi1", MACAddress: "00:00:5E:00:53:40", SSID: "home", Band: "5ghz-ax"}},
	})

	assert.Equal(t, []Sighting{
		{Port: "ether2", MAC: "00:00:5e:00:53:40", VLAN: 10, WiFi: true, SSID: "home", Band: "5ghz-ax"},
	}, got.Seen)
	assert.Equal(t, []TopologyPort{{Name: "ether2", Kind: PortWired, Running: true}}, got.Ports)
}

// A neighbour heard on its port and again on a VLAN interface is one
// neighbour, on the port, with the IPv4 address it advertised on the VLAN.
func TestBuildTopologyMergesANeighbourHeardTwice(t *testing.T) {
	t.Parallel()

	got := testRouterOS(t).buildTopology(t.Context(), switchTables{
		ifaces: []routeros.Interface{
			{Name: "sfp1", Type: "ether"},
			{Name: "vlan99", Type: "vlan"},
		},
		neighbors: []routeros.Neighbor{
			{Interface: "vlan99", MACAddress: "00:00:5E:00:53:20", Address4: "192.0.2.2", Identity: "switch-a"},
			{Interface: "sfp1", MACAddress: "00:00:5E:00:53:20", Address: "fe80::1", Identity: "switch-a", Board: "CRS326"},
		},
	})

	assert.Equal(t, []Neighbour{{
		Port: "sfp1", MAC: "00:00:5e:00:53:20", Addr: netip.MustParseAddr("192.0.2.2"),
		Identity: "switch-a", Board: "CRS326",
	}}, got.Neighbours)
}
