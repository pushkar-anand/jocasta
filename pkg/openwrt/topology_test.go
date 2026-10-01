package openwrt

import (
	"encoding/base64"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fdbTable builds a brforward table from entries, as the kernel writes it.
func fdbTable(t *testing.T, entries ...RawFDBEntry) []byte {
	t.Helper()

	var b []byte

	for _, e := range entries {
		mac, err := net.ParseMAC(e.MAC)
		require.NoError(t, err)

		row := make([]byte, fdbEntrySize)
		copy(row, mac)
		row[6] = byte(e.PortNo)       //nolint:gosec // G115: a test port number fits.
		row[12] = byte(e.PortNo >> 8) //nolint:gosec // G115: a test port number fits.

		if e.Local {
			row[7] = 1
		}

		b = append(b, row...)
	}

	return b
}

func TestParseFDB(t *testing.T) {
	t.Parallel()

	// Laid out as the test router's bridge wrote its table: a wired client on
	// port 1 with an ageing timer running, then the bridge's own address.
	raw := []byte{
		0x00, 0x00, 0x5e, 0x00, 0x53, 0x01, // address
		0x01,                   // port number, low byte
		0x00,                   // not local
		0x94, 0x06, 0x00, 0x00, // ageing timer
		0x00,       // port number, high byte
		0x00,       // padding
		0x00, 0x00, // unused
		0x00, 0x00, 0x5e, 0x00, 0x53, 0xf0,
		0x01,
		0x01, // local
		0x00, 0x00, 0x00, 0x00,
		0x00,
		0x00,
		0x00, 0x00,
	}

	got, err := ParseFDB(raw)
	require.NoError(t, err)

	assert.Equal(t, []RawFDBEntry{
		{MAC: "00:00:5e:00:53:01", PortNo: 1},
		{MAC: "00:00:5e:00:53:f0", PortNo: 1, Local: true},
	}, got)
}

// A port number past 255 carries its high byte in port_hi.
func TestParseFDBReadsTheHighPortByte(t *testing.T) {
	t.Parallel()

	got, err := ParseFDB(fdbTable(t, RawFDBEntry{MAC: "00:00:5e:00:53:01", PortNo: 300}))
	require.NoError(t, err)

	assert.Equal(t, 300, got[0].PortNo)
}

func TestParseFDBReportsAPartEntry(t *testing.T) {
	t.Parallel()

	b := append(fdbTable(t, RawFDBEntry{MAC: "00:00:5e:00:53:01", PortNo: 1}), 0x00, 0x00)

	got, err := ParseFDB(b)
	require.ErrorIs(t, err, ErrBadFDB)
	assert.Len(t, got, 1)
}

func TestParseBridgeVLANPort(t *testing.T) {
	t.Parallel()

	tests := map[string]BridgeVLANPort{
		"lan1":    {Name: "lan1"},
		"lan2:t":  {Name: "lan2", Tagged: true},
		"lan3:u*": {Name: "lan3", PVID: true},
		"lan4:*":  {Name: "lan4", PVID: true},
		"wan:t*":  {Name: "wan", Tagged: true, PVID: true},
	}

	for in, want := range tests {
		assert.Equal(t, want, ParseBridgeVLANPort(in), in)
	}
}

func TestBridgeFDBNamesEachPort(t *testing.T) {
	t.Parallel()

	table := fdbTable(t,
		RawFDBEntry{MAC: "00:00:5e:00:53:01", PortNo: 1},
		RawFDBEntry{MAC: "00:00:5e:00:53:02", PortNo: 2},
		RawFDBEntry{MAC: "00:00:5e:00:53:03", PortNo: 9},
		RawFDBEntry{MAC: "00:00:5e:00:53:ff", PortNo: 1, Local: true},
	)

	_, o, _ := newFakeRouter(t, map[string]string{
		"read /sys/class/net/br-lan/brforward":    `[0,{"data":"` + base64.StdEncoding.EncodeToString(table) + `"}]`,
		"read /sys/class/net/lan1/brport/port_no": `[0,{"data":"0x1\n"}]`,
		"read /sys/class/net/lan2/brport/port_no": `[0,{"data":"0x2\n"}]`,
	})

	got, err := o.BridgeFDB(t.Context(), "br-lan", []string{"lan1", "lan2"})
	require.NoError(t, err)

	assert.Equal(t, []FDBEntry{
		{MAC: "00:00:5e:00:53:01", Port: "lan1"},
		{MAC: "00:00:5e:00:53:02", Port: "lan2"},
		{MAC: "00:00:5e:00:53:03"},
		{MAC: "00:00:5e:00:53:ff", Port: "lan1", Local: true},
	}, got)
}

// A member whose number cannot be read leaves its entries with no port, and
// the rest still come back.
func TestBridgeFDBKeepsTheTableWhenAPortNumberFails(t *testing.T) {
	t.Parallel()

	table := fdbTable(t, RawFDBEntry{MAC: "00:00:5e:00:53:01", PortNo: 1})

	_, o, _ := newFakeRouter(t, map[string]string{
		"read /sys/class/net/br-lan/brforward": `[0,{"data":"` + base64.StdEncoding.EncodeToString(table) + `"}]`,
	})

	got, err := o.BridgeFDB(t.Context(), "br-lan", []string{"lan1"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, []FDBEntry{{MAC: "00:00:5e:00:53:01"}}, got)
}

// rpcd reads at most 4096 bytes of the table, 256 entries. A table that fills
// the read may have been cut short, and says so beside the entries it has.
func TestBridgeFDBReportsATableThatFillsTheRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		entries int
		capped  bool
	}{
		{entries: 255},
		{entries: 256, capped: true},
	}

	for _, tt := range tests {
		var rows []RawFDBEntry
		for i := range tt.entries {
			rows = append(rows, RawFDBEntry{MAC: fmt.Sprintf("00:00:5e:00:%02x:%02x", i/256, i%256), PortNo: 1})
		}

		_, o, _ := newFakeRouter(t, map[string]string{
			"read /sys/class/net/br-lan/brforward":    `[0,{"data":"` + base64.StdEncoding.EncodeToString(fdbTable(t, rows...)) + `"}]`,
			"read /sys/class/net/lan1/brport/port_no": `[0,{"data":"0x1\n"}]`,
		})

		got, err := o.BridgeFDB(t.Context(), "br-lan", []string{"lan1"})
		assert.Len(t, got, tt.entries)

		if tt.capped {
			require.ErrorIs(t, err, ErrFDBCapped)
		} else {
			require.NoError(t, err)
		}
	}
}

// The ACL file grants the bridge table; without it the read is refused.
func TestBridgeFDBWithoutTheACLIsUnauthorized(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{"read /sys/class/net/br-lan/brforward": `[6]`})

	_, err := o.BridgeFDB(t.Context(), "br-lan", nil)
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestNetworkDevices(t *testing.T) {
	t.Parallel()

	// Trimmed from the test router's answer, with a DSA switch's user port and
	// its conduit, and a radio.
	_, o, _ := newFakeRouter(t, map[string]string{
		"luci-rpc getNetworkDevices": `[0,{` +
			`"br-lan":{"name":"br-lan","bridge":true,"ports":["lan1","phy0-ap0"],"devtype":"bridge","type":1,"up":true,"mac":"00:00:5E:00:53:F0","link":{"speed":1000,"duplex":"unknown","carrier":true}},` +
			`"eth0":{"name":"eth0","devtype":"ethernet","type":1,"up":true,"mac":"00:00:5E:00:53:F0","link":{"speed":1000,"duplex":"full","carrier":true}},` +
			`"lan1":{"name":"lan1","devtype":"dsa","type":1,"up":true,"mac":"00:00:5E:00:53:F0","link":{"speed":1000,"duplex":"full","carrier":true}},` +
			`"phy0-ap0":{"name":"phy0-ap0","wireless":true,"devtype":"wlan","type":1,"up":true,"mac":"00:00:5E:00:53:F1","link":{"carrier":true}},` +
			`"br-lan.20":{"name":"br-lan.20","devtype":"vlan","type":1,"up":true,"mac":"00:00:5E:00:53:F0","link":{"carrier":true}},` +
			`"lo":{"name":"lo","devtype":"ethernet","type":772,"up":true,"link":{"carrier":true}}}]`,
	})

	got, err := o.NetworkDevices(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 6)

	assert.Equal(t, []string{"lan1", "phy0-ap0"}, got["br-lan"].Ports)
	assert.Equal(t, 1000, got["lan1"].Link.Speed)
	assert.Equal(t, "full", got["lan1"].Link.Duplex)

	ports := map[string]bool{}
	for name, d := range got {
		ports[name] = d.IsPort()
	}

	assert.Equal(t, map[string]bool{
		"br-lan": false, "eth0": true, "lan1": true, "phy0-ap0": true, "br-lan.20": false, "lo": false,
	}, ports)

	assert.True(t, got["lo"].IsLoopback())
	assert.True(t, got["phy0-ap0"].IsWireless())
}

func TestWirelessDevices(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"luci-rpc getWirelessDevices": `[0,{` +
			`"radio1":{"up":true,"config":{"band":"5g","channel":"36"},"interfaces":[` +
			`{"section":"default_radio1","ifname":"phy1-ap0","config":{"mode":"ap","ssid":"Home"},"iwinfo":{"ssid":"Home","frequency":5180}}]},` +
			`"radio0":{"up":true,"config":{"band":"2g"},"interfaces":[` +
			`{"section":"default_radio0","ifname":"phy0-ap0","config":{"mode":"ap","ssid":"Home"}},` +
			`{"section":"guest","config":{"mode":"ap","ssid":"Guest"}}]}}]`,
	})

	got, err := o.WirelessDevices(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []Radio{
		{Name: "radio0", Up: true, Band: "2g", Interfaces: []WiFiIface{
			{Ifname: "phy0-ap0", SSID: "Home", Mode: "ap"},
			{SSID: "Guest", Mode: "ap"},
		}},
		{Name: "radio1", Up: true, Band: "5g", Interfaces: []WiFiIface{
			{Ifname: "phy1-ap0", SSID: "Home", Mode: "ap"},
		}},
	}, got)
}

// A router with no radio answers with the status alone.
func TestWirelessDevicesWithNoRadio(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{"luci-rpc getWirelessDevices": `[0]`})

	got, err := o.WirelessDevices(t.Context())
	require.NoError(t, err)
	assert.Empty(t, got)
}

// iwinfo gives rates in kbit/s, which are converted to bits per second.
func TestStations(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"iwinfo assoclist": `[0,{"results":[{"mac":"00:00:5E:00:53:21","signal":-52,"noise":-95,"inactive":10,` +
			`"rx":{"rate":866700,"mcs":9,"40mhz":false},"tx":{"rate":1200900,"mcs":11}}]}]`,
	})

	got, err := o.Stations(t.Context(), "phy1-ap0")
	require.NoError(t, err)

	assert.Equal(t, []Station{{MAC: "00:00:5E:00:53:21", Signal: -52, TxRate: 1_200_900_000, RxRate: 866_700_000}}, got)
}

func TestBridgeVLANs(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"uci network bridge-vlan": `[0,{"values":{` +
			`"cfgb":{".index":9,"device":"br-lan","vlan":"20","ports":["lan1:t","lan3:u*"]},` +
			`"cfga":{".index":8,"device":"br-lan","vlan":"1","ports":["lan1:u*","lan2:u*","lan3"]}}}]`,
	})

	got, err := o.BridgeVLANs(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []BridgeVLAN{
		{Bridge: "br-lan", VLAN: 1, Ports: []BridgeVLANPort{
			{Name: "lan1", PVID: true}, {Name: "lan2", PVID: true}, {Name: "lan3"},
		}},
		{Bridge: "br-lan", VLAN: 20, Ports: []BridgeVLANPort{
			{Name: "lan1", Tagged: true}, {Name: "lan3", PVID: true},
		}},
	}, got)
}
