package routeros

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityReadsTheName(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, identityAPI, `{"name":"switch-a"}`))

	id, err := r.Identity(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "switch-a", id.Name)
}

func TestInterfacesDecodeTheTable(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, interfaceAPI, `[
	  {".id":"*1","name":"ether1","type":"ether","mac-address":"00:00:5E:00:53:01","running":"true","disabled":"false"},
	  {".id":"*2","name":"bridge","type":"bridge","mac-address":"00:00:5E:00:53:01","running":"true","disabled":"false"},
	  {".id":"*3","name":"pppoe-out1","type":"pppoe-out","running":"true","disabled":"false","comment":"uplink"}
	]`))

	ifaces, err := r.Interfaces(t.Context())
	require.NoError(t, err)
	require.Len(t, ifaces, 3)

	assert.Equal(t, "ether1", ifaces[0].Name)
	assert.Equal(t, "ether", ifaces[0].Type)
	assert.Equal(t, "00:00:5E:00:53:01", ifaces[0].MACAddress)
	assert.True(t, bool(ifaces[0].Running))
	assert.Empty(t, ifaces[2].MACAddress)
	assert.Equal(t, "uplink", ifaces[2].Comment)
}

func TestBridgePortsReadThePVID(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, bridgePortAPI, `[
	  {".id":"*1","interface":"ether2","bridge":"bridge","pvid":"10","disabled":"false","inactive":"false"},
	  {".id":"*2","interface":"ether3","bridge":"bridge","pvid":"1","disabled":"false","inactive":"true"},
	  {".id":"*3","interface":"ether4","bridge":"bridge","pvid":"x"}
	]`))

	ports, err := r.BridgePorts(t.Context())
	require.NoError(t, err)
	require.Len(t, ports, 3)

	pvid, ok := ports[0].VLAN()
	require.True(t, ok)
	assert.Equal(t, 10, pvid)
	assert.True(t, bool(ports[1].Inactive))

	_, ok = ports[2].VLAN()
	assert.False(t, ok)
}

func TestBridgeVLANsPreferWhatTheRouterIsDoing(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, bridgeVLANAPI, `[
	  {".id":"*1","bridge":"bridge","vlan-ids":"10","tagged":"bridge,ether3","untagged":"ether2","current-tagged":"bridge,ether3,ether5","current-untagged":"ether2"},
	  {".id":"*2","bridge":"bridge","vlan-ids":"20,30","tagged":"bridge,ether3","untagged":"","current-tagged":"","current-untagged":""}
	]`))

	vlans, err := r.BridgeVLANs(t.Context())
	require.NoError(t, err)
	require.Len(t, vlans, 2)

	assert.Equal(t, []int{10}, vlans[0].VLANs())
	assert.Equal(t, []string{"bridge", "ether3", "ether5"}, vlans[0].TaggedPorts())
	assert.Equal(t, []string{"ether2"}, vlans[0].UntaggedPorts())

	// Nothing current falls back to the configuration.
	assert.Equal(t, []int{20, 30}, vlans[1].VLANs())
	assert.Equal(t, []string{"bridge", "ether3"}, vlans[1].TaggedPorts())
	assert.Empty(t, vlans[1].UntaggedPorts())
}

func TestBridgeVLANExpandsRanges(t *testing.T) {
	t.Parallel()

	tests := map[string][]int{
		"10":          {10},
		"10,20":       {10, 20},
		"100-103":     {100, 101, 102, 103},
		"30, 10-11":   {10, 11, 30},
		"10,10":       {10},
		"x,20":        {20},
		"5-2":         nil,
		"0":           nil,
		"4093-999999": {4093, 4094},
		"":            nil,
	}

	for ids, want := range tests {
		t.Run(ids, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, want, BridgeVLAN{VLANIDs: ids}.VLANs())
		})
	}
}

func TestBridgeHostsReadThePortAndVLAN(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, bridgeHostAPI, `[
	  {".id":"*1","mac-address":"00:00:5E:00:53:10","on-interface":"ether2","bridge":"bridge","vid":"10","local":"false","dynamic":"true"},
	  {".id":"*2","mac-address":"00:00:5E:00:53:01","on-interface":"bridge","bridge":"bridge","local":"true","dynamic":"false"},
	  {".id":"*3","mac-address":"00:00:5E:00:53:11","interface":"ether3","bridge":"bridge","dynamic":"true"}
	]`))

	hosts, err := r.BridgeHosts(t.Context())
	require.NoError(t, err)
	require.Len(t, hosts, 3)

	assert.Equal(t, "ether2", hosts[0].Port())
	vid, ok := hosts[0].VLAN()
	require.True(t, ok)
	assert.Equal(t, 10, vid)

	assert.True(t, bool(hosts[1].Local))

	// A release that names the port "interface" still says where.
	assert.Equal(t, "ether3", hosts[2].Port())
	_, ok = hosts[2].VLAN()
	assert.False(t, ok, "no vid without VLAN filtering")
}

func TestNeighborsReadThePortTheyArrivedOn(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, neighborAPI, `[
	  {".id":"*1","interface":"ether3,bridge","mac-address":"00:00:5E:00:53:20","address":"192.0.2.2","address4":"192.0.2.2","identity":"switch-a","platform":"MikroTik","board":"CRS326-24G-2S+","interface-name":"sfp1","system-caps-enabled":"bridge","discovered-by":"mndp,lldp"},
	  {".id":"*2","interface":"ether5","mac-address":"00:00:5E:00:53:30","address":"198.51.100.3","identity":"ap-hall","platform":"MikroTik"}
	]`))

	ns, err := r.Neighbors(t.Context())
	require.NoError(t, err)
	require.Len(t, ns, 2)

	assert.Equal(t, "ether3", ns[0].Port())
	assert.Equal(t, "192.0.2.2", ns[0].Addr())
	assert.Equal(t, "switch-a", ns[0].Identity)
	assert.Equal(t, "CRS326-24G-2S+", ns[0].Board)
	assert.Equal(t, "sfp1", ns[0].InterfaceName)

	assert.Equal(t, "ether5", ns[1].Port())
	assert.Equal(t, "198.51.100.3", ns[1].Addr())
}

func TestRegistrationsReadTheWifiPackage(t *testing.T) {
	t.Parallel()

	r := serve(t, respond(t, wifiRegistrationAPI, `[
	  {".id":"*1","interface":"wifi1","mac-address":"00:00:5E:00:53:40","ssid":"home","band":"5ghz-ax","signal":"-58","uptime":"1h2m"}
	]`))

	regs, err := r.Registrations(t.Context())
	require.NoError(t, err)
	require.Len(t, regs, 1)

	assert.Equal(t, Registration{
		Interface: "wifi1", MACAddress: "00:00:5E:00:53:40", SSID: "home",
		Band: "5ghz-ax", Signal: "-58", Uptime: "1h2m",
	}, regs[0])
}

// A router on the wireless package has no wifi menu. Its registration table
// names neither the SSID nor the band, so they come off the interface.
func TestRegistrationsFallBackToTheWirelessPackage(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch req.URL.Path {
		case basePath + wirelessRegistrationAPI:
			_, _ = w.Write([]byte(`[{".id":"*1","interface":"wlan2","mac-address":"00:00:5E:00:53:41","signal-strength":"-61@6Mbps","uptime":"5m"}]`))
		case basePath + wirelessAPI:
			_, _ = w.Write([]byte(`[{".id":"*1","name":"wlan2","ssid":"iot","band":"2ghz-b/g/n"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":404,"message":"Not Found","detail":"no such command prefix"}`))
		}
	})

	regs, err := r.Registrations(t.Context())
	require.NoError(t, err)
	require.Len(t, regs, 1)

	assert.Equal(t, Registration{
		Interface: "wlan2", MACAddress: "00:00:5E:00:53:41", SSID: "iot",
		Band: "2ghz-b/g/n", Signal: "-61@6Mbps", Uptime: "5m",
	}, regs[0])
}

func TestRegistrationsOnARouterWithNoWifiIsEmpty(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":404,"message":"Not Found","detail":"no such command prefix"}`))
	})

	regs, err := r.Registrations(t.Context())
	require.NoError(t, err)
	assert.Empty(t, regs)
}

func TestRegistrationsPassOnARefusal(t *testing.T) {
	t.Parallel()

	r := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := r.Registrations(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)
}
