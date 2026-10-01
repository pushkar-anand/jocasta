package openwrt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNeighbours(t *testing.T) {
	t.Parallel()

	// busybox, as OpenWrt ships it, then iproute2, as ip-full prints it.
	text := `192.0.2.10 dev br-lan lladdr 00:00:5e:00:53:01 ref 1 used 0/0/0 probes 4 REACHABLE
192.0.2.11 dev br-lan  used 0/0/0 probes 3 FAILED
192.0.2.12 dev br-lan.20 lladdr 00:00:5e:00:53:02 used 0/0/0 probes 1 STALE
2001:db8::10 dev br-lan lladdr 00:00:5e:00:53:01 router STALE
fe80::1 dev br-lan lladdr 00:00:5e:00:53:03 PERMANENT

garbage
`

	assert.Equal(t, []Neighbour{
		{Address: "192.0.2.10", Device: "br-lan", MAC: "00:00:5e:00:53:01", State: NeighReachable},
		{Address: "192.0.2.11", Device: "br-lan", State: NeighFailed},
		{Address: "192.0.2.12", Device: "br-lan.20", MAC: "00:00:5e:00:53:02", State: NeighStale},
		{Address: "2001:db8::10", Device: "br-lan", MAC: "00:00:5e:00:53:01", State: NeighStale},
		{Address: "fe80::1", Device: "br-lan", MAC: "00:00:5e:00:53:03", State: NeighPermanent},
	}, ParseNeighbours(text))
}

func TestNeighbourUsableAndReachable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n                 Neighbour
		usable, reachable bool
	}{
		{Neighbour{MAC: "00:00:5e:00:53:01", State: NeighReachable}, true, true},
		{Neighbour{MAC: "00:00:5e:00:53:01", State: NeighStale}, true, false},
		{Neighbour{MAC: "00:00:5e:00:53:01", State: NeighPermanent}, true, false},
		{Neighbour{MAC: "00:00:5e:00:53:01", State: "DELAY"}, true, false},
		{Neighbour{State: NeighFailed}, false, false},
		{Neighbour{MAC: "00:00:5e:00:53:01", State: NeighFailed}, false, false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.usable, tt.n.Usable(), "%+v", tt.n)
		assert.Equal(t, tt.reachable, tt.n.Reachable(), "%+v", tt.n)
	}
}

func TestNeighboursReadsBothFamilies(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"exec /sbin/ip -4 neigh show": `[0,{"code":0,"stdout":"192.0.2.10 dev br-lan lladdr 00:00:5e:00:53:01 ref 1 used 0/0/0 probes 4 REACHABLE\n"}]`,
		"exec /sbin/ip -6 neigh show": `[0,{"code":0,"stdout":"2001:db8::10 dev br-lan lladdr 00:00:5e:00:53:01 router STALE\n"}]`,
	})

	got, err := o.Neighbours(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "192.0.2.10", got[0].Address)
	assert.Equal(t, "2001:db8::10", got[1].Address)
}

// A command the router ran and that failed costs its own family alone.
func TestNeighboursKeepsTheFamilyThatWorked(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"exec /sbin/ip -4 neigh show": `[0,{"code":0,"stdout":"192.0.2.10 dev br-lan lladdr 00:00:5e:00:53:01 REACHABLE\n"}]`,
		"exec /sbin/ip -6 neigh show": `[0,{"code":1,"stderr":"ip: can't find device\n"}]`,
	})

	got, err := o.Neighbours(t.Context())
	require.ErrorIs(t, err, ErrCommand)
	assert.Len(t, got, 1)
}

// An ACL without the command answers with a permission status.
func TestACommandTheACLRefusesIsUnauthorized(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"exec /sbin/ip -4 neigh show": `[6]`,
		"exec /sbin/ip -6 neigh show": `[6]`,
	})

	_, err := o.Neighbours(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestDHCPLeases(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"luci-rpc getDHCPLeases 4": `[0,{"dhcp_leases":[` +
			`{"expires":43200,"hostname":"phone-a","macaddr":"16:30:A1:4E:06:42","duid":"011630a14e0642","ipaddr":"192.0.2.119"},` +
			`{"expires":43200,"hostname":"*","macaddr":"BA:D1:CD:A8:75:A5","ipaddr":"192.0.2.136"},` +
			`{"expires":false,"macaddr":"1E:2A:29:9C:9C:E3","ipaddr":"192.0.2.50"}]}]`,
		"luci-rpc getDHCPLeases 6": `[0,{"dhcp6_leases":[` +
			`{"expires":3600,"hostname":"laptop","duid":"00010001","macaddr":"BA:D1:CD:A8:75:A5",` +
			`"ip6addr":"2001:db8::a","ip6addrs":["2001:db8::a","2001:db8::b"]},` +
			`{"expires":3600,"duid":"00040000","ip6addr":"2001:db8::c"}]}]`,
	})

	got, err := o.DHCPLeases(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []Lease{
		{Address: "192.0.2.119", MAC: "16:30:A1:4E:06:42", Hostname: "phone-a"},
		{Address: "192.0.2.136", MAC: "BA:D1:CD:A8:75:A5"},
		{Address: "192.0.2.50", MAC: "1E:2A:29:9C:9C:E3"},
		{Address: "2001:db8::a", MAC: "BA:D1:CD:A8:75:A5", Hostname: "laptop", IPv6: true},
		{Address: "2001:db8::b", MAC: "BA:D1:CD:A8:75:A5", Hostname: "laptop", IPv6: true},
		{Address: "2001:db8::c", IPv6: true},
	}, got)
}

// A router without the IPv6 half still lists its IPv4 leases.
func TestDHCPLeasesKeepsTheFamilyThatWorked(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"luci-rpc getDHCPLeases 4": `[0,{"dhcp_leases":[{"hostname":"tv","macaddr":"1E:2A:29:9C:9C:E3","ipaddr":"192.0.2.50"}]}]`,
		"luci-rpc getDHCPLeases 6": `[9]`,
	})

	got, err := o.DHCPLeases(t.Context())
	require.Error(t, err)
	assert.Len(t, got, 1)
}

func TestStaticHosts(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"uci dhcp host": `[0,{"values":{` +
			`"cfg01":{".type":"host","name":"living-room-tv","mac":"1e:2a:29:9c:9c:e3","ip":"192.0.2.50"},` +
			`"cfg02":{".type":"host","name":"laptop","mac":["ba:d1:cd:a8:75:a5","ba:d1:cd:a8:75:a6"]},` +
			`"cfg03":{".type":"host","name":"nas","mac":"00:00:5e:00:53:01 00:00:5e:00:53:02","ip":"192.0.2.60"},` +
			`"cfg04":{".type":"host","mac":"00:00:5e:00:53:09","ip":"ignore"}}}]`,
	})

	got, err := o.StaticHosts(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []StaticHost{
		{Name: "living-room-tv", MACs: []string{"1e:2a:29:9c:9c:e3"}, Address: "192.0.2.50"},
		{Name: "laptop", MACs: []string{"ba:d1:cd:a8:75:a5", "ba:d1:cd:a8:75:a6"}},
		{Name: "nas", MACs: []string{"00:00:5e:00:53:01", "00:00:5e:00:53:02"}, Address: "192.0.2.60"},
		{MACs: []string{"00:00:5e:00:53:09"}},
	}, got)
}

func TestInterfaces(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"network.interface dump": `[0,{"interface":[` +
			`{"interface":"lan","up":true,"proto":"static","device":"br-lan","l3_device":"br-lan",` +
			`"ipv4-address":[{"address":"192.0.2.1","mask":24}],"ipv6-address":[],` +
			`"ipv6-prefix-assignment":[{"address":"2001:db8:0:1::","mask":64,"local-address":{"address":"2001:db8:0:1::1","mask":64}}]},` +
			`{"interface":"wan","up":true,"proto":"dhcp","device":"eth1","l3_device":"eth1",` +
			`"ipv4-address":[{"address":"198.51.100.7","mask":24}]}]}]`,
	})

	got, err := o.Interfaces(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, Interface{
		Name: "lan", Up: true, Proto: ProtoStatic, Device: "br-lan", L3Device: "br-lan",
		IPv4:     []IfaceAddr{{Address: "192.0.2.1", Mask: 24}},
		IPv6:     []IfaceAddr{},
		Assigned: []PrefixAssignment{{Address: "2001:db8:0:1::", Mask: 64}},
	}, got[0])
	assert.Equal(t, "dhcp", got[1].Proto)
}

func TestVLANs(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{
		"uci network device": `[0,{"values":{` +
			`"cfg01":{".type":"device","name":"br-lan","type":"bridge","ports":["lan1","lan2"]},` +
			`"vl20":{".type":"device","type":"8021q","ifname":"eth0","vid":"20","name":"iot0"},` +
			`"vl30":{".type":"device","type":"8021q","ifname":"eth0","vid":"30"},` +
			`"bad":{".type":"device","type":"8021q","ifname":"eth0","vid":"5000"}}}]`,
		"uci network bridge-vlan": `[0,{"values":{` +
			`"cfg09":{".type":"bridge-vlan","device":"br-lan","vlan":"40","ports":["lan1:t"]}}}]`,
	})

	got, err := o.VLANs(t.Context())
	require.NoError(t, err)

	assert.Equal(t, map[string]int{"iot0": 20, "eth0.30": 30, "br-lan.40": 40}, got)
}
