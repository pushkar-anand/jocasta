package plugin

import (
	"encoding/base64"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/pkg/openwrt"
)

// netDevice builds a device as getNetworkDevices describes it.
func netDevice(name, devtype string, up, carrier bool, speed int, duplex string) openwrt.NetDevice {
	d := openwrt.NetDevice{Name: name, DevType: devtype, Type: 1, Up: up, MAC: "00:00:5e:00:53:f0"}
	d.Link.Carrier, d.Link.Speed, d.Link.Duplex = carrier, speed, duplex

	return d
}

// homeTables is a router with a VLAN-aware bridge over two wired ports and a
// 2.4 GHz network, and a 5 GHz network in no bridge.
func homeTables() openWrtTables {
	bridge := netDevice("br-lan", "bridge", true, true, 1000, "unknown")
	bridge.Bridge, bridge.Ports = true, []string{"lan1", "lan2", "phy0-ap0"}

	radio0 := netDevice("phy0-ap0", "wlan", true, true, 0, "")
	radio0.Wireless, radio0.MAC = true, "00:00:5e:00:53:f1"

	radio1 := netDevice("phy1-ap0", "wlan", true, true, 0, "")
	radio1.Wireless, radio1.MAC = true, "00:00:5e:00:53:f2"

	lo := netDevice("lo", "ethernet", true, true, 0, "")
	lo.Type, lo.MAC = 772, ""

	return openWrtTables{
		identity: "gateway",
		devices: map[string]openwrt.NetDevice{
			"br-lan":    bridge,
			"lan1":      netDevice("lan1", "ethernet", true, true, 1000, "full"),
			"lan2":      netDevice("lan2", "ethernet", true, false, 0, ""),
			"br-lan.20": netDevice("br-lan.20", "vlan", true, true, 0, ""),
			"phy0-ap0":  radio0,
			"phy1-ap0":  radio1,
			"lo":        lo,
		},
		fdb: map[string][]openwrt.FDBEntry{
			"br-lan": {
				{MAC: "00:00:5e:00:53:01", Port: "lan1"},
				{MAC: "00:00:5e:00:53:02", Port: "phy0-ap0"},
				{MAC: "00:00:5e:00:53:f0", Port: "lan1", Local: true},
				{MAC: "00:00:5e:00:53:09"},
			},
		},
		vlans: []openwrt.BridgeVLAN{
			{Bridge: "br-lan", VLAN: 1, Ports: []openwrt.BridgeVLANPort{{Name: "lan1", PVID: true}, {Name: "lan2", PVID: true}}},
			{Bridge: "br-lan", VLAN: 20, Ports: []openwrt.BridgeVLANPort{{Name: "lan1", Tagged: true}}},
		},
		radios: []openwrt.Radio{
			{Name: "radio0", Up: true, Band: "2g", Interfaces: []openwrt.WiFiIface{{Ifname: "phy0-ap0", SSID: "Home", Mode: "ap"}}},
			{Name: "radio1", Up: true, Band: "5g", Interfaces: []openwrt.WiFiIface{{Ifname: "phy1-ap0", SSID: "Home 5", Mode: "ap"}}},
		},
		stations: map[string][]openwrt.Station{
			"phy0-ap0": {{MAC: "00:00:5E:00:53:02", Signal: -61, TxRate: 72_200_000, RxRate: 65_000_000}},
			"phy1-ap0": {{MAC: "00:00:5E:00:53:03", Signal: -48, TxRate: 866_700_000, RxRate: 780_000_000}},
		},
	}
}

func TestOpenWrtTopologyPorts(t *testing.T) {
	t.Parallel()

	topo := testOpenWrt(t).buildTopology(t.Context(), homeTables())

	assert.Equal(t, "gateway", topo.Identity)
	assert.True(t, topo.Gateway)
	assert.Equal(t, pinned, topo.ReadAt)

	// The bridge, the VLAN device and loopback are places an address can be
	// learned, and no port.
	assert.Equal(t, []TopologyPort{
		{Name: "lan1", Kind: PortWired, PVID: 1, Tagged: []int{20}, Untagged: []int{1}, Running: true, Rate: 1_000_000_000, FullDuplex: true},
		{Name: "lan2", Kind: PortWired, PVID: 1, Untagged: []int{1}},
		{Name: "phy0-ap0", Kind: PortWiFi, Running: true},
		{Name: "phy1-ap0", Kind: PortWiFi, Running: true},
	}, topo.Ports)

	assert.Equal(t, []string{"00:00:5e:00:53:f0", "00:00:5e:00:53:f1", "00:00:5e:00:53:f2"}, topo.Own)
	assert.Empty(t, topo.Neighbours)
}

// A wired client is where the bridge learned it. A Wi-Fi client the bridge
// learned is on its radio's interface with the network's SSID and band, and
// one on a network in no bridge is placed on its interface.
func TestOpenWrtTopologySightings(t *testing.T) {
	t.Parallel()

	topo := testOpenWrt(t).buildTopology(t.Context(), homeTables())

	assert.Equal(t, []Sighting{
		{Port: "lan1", MAC: "00:00:5e:00:53:01"},
		{Port: "phy0-ap0", MAC: "00:00:5e:00:53:02", WiFi: true, SSID: "Home", Band: "2ghz", TxRate: 72_200_000, RxRate: 65_000_000, Signal: -61},
		{Port: "phy1-ap0", MAC: "00:00:5e:00:53:03", WiFi: true, SSID: "Home 5", Band: "5ghz", TxRate: 866_700_000, RxRate: 780_000_000, Signal: -48},
	}, topo.Seen)
}

// An access point is read for what is plugged into it, and the router above
// it is where the network hangs from.
func TestOpenWrtTopologyOnlyIsNoGateway(t *testing.T) {
	t.Parallel()

	o := testOpenWrt(t)
	OpenWrtTopologyOnly()(o)

	assert.True(t, o.IsTopologyOnly())
	assert.False(t, o.buildTopology(t.Context(), homeTables()).Gateway)
}

func TestBandName(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"2g": "2ghz", "5g": "5ghz", "6g": "6ghz", "60g": "60ghz", "": ""} {
		assert.Equal(t, want, bandName(in), in)
	}
}

// The tables as an access point with one bridge, one wired client and one
// radio answers them.
var openWrtTopologyAnswers = map[string]string{
	"system board": `[0,{"hostname":"attic-ap"}]`,
	"luci-rpc getNetworkDevices": `[0,{` +
		`"br-lan":{"name":"br-lan","bridge":true,"ports":["lan1"],"devtype":"bridge","type":1,"up":true,"mac":"00:00:5E:00:53:F0","link":{"carrier":true}},` +
		`"lan1":{"name":"lan1","devtype":"ethernet","type":1,"up":true,"mac":"00:00:5E:00:53:F0","link":{"speed":1000,"duplex":"full","carrier":true}}}]`,
	"read /sys/class/net/br-lan/brforward": `[0,{"data":"` + base64.StdEncoding.EncodeToString([]byte{
		0x00, 0x00, 0x5e, 0x00, 0x53, 0x01, 0x01, 0x00, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}) + `"}]`,
	"read /sys/class/net/lan1/brport/port_no": `[0,{"data":"0x1\n"}]`,
	"uci network bridge-vlan":                 `[0,{"values":{}}]`,
	"luci-rpc getWirelessDevices":             `[0]`,
}

func TestOpenWrtTopologyReadsEveryTable(t *testing.T) {
	t.Parallel()

	topo, err := testOpenWrtWith(t, openWrtTopologyAnswers).Topology(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "attic-ap", topo.Identity)
	assert.Equal(t, []Sighting{{Port: "lan1", MAC: "00:00:5e:00:53:01"}}, topo.Seen)
}

// Without the bridge table nothing was learned, so the read fails with the
// reason.
func TestOpenWrtTopologyWithoutTheBridgeTableFails(t *testing.T) {
	t.Parallel()

	answers := maps.Clone(openWrtTopologyAnswers)
	delete(answers, "read /sys/class/net/br-lan/brforward")

	topo, err := testOpenWrtWith(t, answers).Topology(t.Context())
	require.ErrorIs(t, err, ErrAuth)
	assert.True(t, topo.Empty())
}
