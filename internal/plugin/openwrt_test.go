package plugin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"maps"
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

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/openwrt"
)

// testOpenWrt builds the plugin without a client. Every test here exercises the
// mapping. pkg/openwrt is the network half, and has its own tests.
func testOpenWrt(t *testing.T) *OpenWrt {
	t.Helper()

	return &OpenWrt{
		name:   openWrtPrefix + "gateway",
		logger: slog.New(slog.DiscardHandler),
		now:    func() time.Time { return pinned },
	}
}

// openWrtFacts runs the mapping over the three tables, as Discover does.
func openWrtFacts(
	t *testing.T,
	static []openwrt.StaticHost,
	neigh []openwrt.Neighbour,
	leases []openwrt.Lease,
) []Fact {
	t.Helper()

	out, err := testOpenWrt(t).facts(t.Context(), static, neigh, leases, nil, nil)
	require.NoError(t, err)

	return out
}

// factAt returns the fact for addr. It fails the test when there is none.
func factAt(t *testing.T, facts []Fact, addr string) Fact {
	t.Helper()

	for _, f := range facts {
		if f.Host.Address() == netip.MustParseAddr(addr) {
			return f
		}
	}

	require.Failf(t, "no fact", "for %s", addr)

	return Fact{}
}

func TestNewOpenWrtPrefixesTheInstanceName(t *testing.T) {
	t.Parallel()

	client, err := openwrt.New(&openwrt.Config{Host: "192.0.2.1"}, nil)
	require.NoError(t, err)

	p, err := NewOpenWrt("gateway", client, nil)
	require.NoError(t, err)

	assert.Equal(t, "openwrt:gateway", p.Name())
	assert.Equal(t, dbtype.SourceRouter, p.Kind())
}

func TestNewOpenWrtRefusesAnUnnamedInstance(t *testing.T) {
	t.Parallel()

	client, err := openwrt.New(&openwrt.Config{Host: "192.0.2.1"}, nil)
	require.NoError(t, err)

	_, err = NewOpenWrt("", client, nil)
	require.ErrorIs(t, err, ErrNoOpenWrtName)

	_, err = NewOpenWrt("gateway", nil, nil)
	require.Error(t, err)
}

// Only a reachable neighbour is a sighting. A stale neighbour keeps the device.
// A neighbour that never resolved names nothing.
func TestOpenWrtNeighboursSayWhoIsHere(t *testing.T) {
	t.Parallel()

	facts := openWrtFacts(t, nil, []openwrt.Neighbour{
		{Address: "192.0.2.10", Device: "br-lan", MAC: "00:00:5e:00:53:01", State: openwrt.NeighReachable},
		{Address: "192.0.2.11", Device: "br-lan", MAC: "00:00:5e:00:53:02", State: openwrt.NeighStale},
		{Address: "192.0.2.12", Device: "br-lan", State: openwrt.NeighFailed},
	}, nil)

	require.Len(t, facts, 2)

	here := factAt(t, facts, "192.0.2.10")
	assert.True(t, here.Present)
	assert.Equal(t, "00:00:5e:00:53:01", here.Host.MAC)
	assert.Equal(t, "br-lan", here.Detail["interface"])
	assert.Equal(t, "reachable", here.Detail["neigh_state"])
	assert.Equal(t, pinned, here.SeenAt)

	assert.False(t, factAt(t, facts, "192.0.2.11").Present)
}

// Every device has a link-local IPv6 address on every segment, and a sweep
// never records one. So the plugin drops them from the neighbour table.
func TestOpenWrtDropsLinkLocalNeighbours(t *testing.T) {
	t.Parallel()

	facts := openWrtFacts(t, nil, []openwrt.Neighbour{
		{Address: "fe80::1", Device: "br-lan", MAC: "00:00:5e:00:53:01", State: openwrt.NeighReachable},
		{Address: "2001:db8::10", Device: "br-lan", MAC: "00:00:5e:00:53:01", State: openwrt.NeighReachable},
	}, nil)

	require.Len(t, facts, 1)
	assert.Equal(t, netip.MustParseAddr("2001:db8::10"), facts[0].Host.Address())
}

// A lease names the device, with the standing of a name that a device asked
// for. That name reaches the neighbour entry of the same device.
func TestOpenWrtLeasesNameDevices(t *testing.T) {
	t.Parallel()

	facts := openWrtFacts(t, nil, []openwrt.Neighbour{
		{Address: "192.0.2.10", Device: "br-lan", MAC: "00:00:5e:00:53:01", State: openwrt.NeighReachable},
	}, []openwrt.Lease{
		{Address: "192.0.2.10", MAC: "00:00:5E:00:53:01", Hostname: "phone-a"},
	})

	require.Len(t, facts, 1)

	f := facts[0]
	assert.Equal(t, "phone-a", f.Host.Hostname())
	assert.Equal(t, dbtype.HostnameFromDHCPLease, f.HostnameSource)
	assert.True(t, f.Present)
	assert.Equal(t, "true", f.Detail["dhcp_dynamic"])
}

// The name of a static host is the choice of the operator. So it ranks above
// the name that the device gives itself, at every address of the device. A
// static host that reserves an address makes a claim there, even when nothing
// is connected.
func TestOpenWrtStaticHostsNameAtTheirStanding(t *testing.T) {
	t.Parallel()

	static := []openwrt.StaticHost{
		{Name: "living-room-tv", MACs: []string{"00:00:5e:00:53:14"}, Address: "192.0.2.50"},
		{Name: "nas", MACs: []string{"00:00:5e:00:53:09"}, Address: "192.0.2.60"},
	}

	facts := openWrtFacts(t, static, []openwrt.Neighbour{
		{Address: "192.0.2.5", Device: "br-lan", MAC: "00:00:5e:00:53:14", State: openwrt.NeighStale},
	}, []openwrt.Lease{
		{Address: "192.0.2.50", MAC: "00:00:5E:00:53:14", Hostname: "android-1234"},
	})

	require.Len(t, facts, 3)

	for _, addr := range []string{"192.0.2.5", "192.0.2.50"} {
		f := factAt(t, facts, addr)
		assert.Equal(t, "living-room-tv", f.Host.Hostname(), addr)
		assert.Equal(t, dbtype.HostnameFromDHCPStatic, f.HostnameSource, addr)
	}

	assert.True(t, factAt(t, facts, "192.0.2.50").Present)
	assert.Equal(t, "false", factAt(t, facts, "192.0.2.50").Detail["dhcp_dynamic"])

	nas := factAt(t, facts, "192.0.2.60")
	assert.False(t, nas.Present)
	assert.Equal(t, "nas", nas.Host.Hostname())
	assert.Equal(t, dbtype.HostnameFromDHCPStatic, nas.HostnameSource)
}

// A lease without a hardware address cannot match a device. This happens to an
// IPv6 lease whose client built its DUID in a different way.
func TestOpenWrtDropsALeaseWithNoHardwareAddress(t *testing.T) {
	t.Parallel()

	facts := openWrtFacts(t, nil, nil, []openwrt.Lease{
		{Address: "2001:db8::c", Hostname: "laptop", IPv6: true},
	})

	assert.Empty(t, facts)
}

func TestOpenWrtNetworks(t *testing.T) {
	t.Parallel()

	ifaces := []openwrt.Interface{
		{
			Name: "lan", Up: true, Proto: openwrt.ProtoStatic, Device: "br-lan", L3Device: "br-lan",
			IPv4:     []openwrt.IfaceAddr{{Address: "192.0.2.1", Mask: 24}},
			Assigned: []openwrt.PrefixAssignment{{Address: "2001:db8:0:1::", Mask: 64}},
		},
		{
			Name: "iot", Up: true, Proto: openwrt.ProtoStatic, Device: "br-lan.20", L3Device: "br-lan.20",
			IPv4: []openwrt.IfaceAddr{{Address: "198.51.100.1", Mask: 24}},
		},
		// An uplink has an address on a segment that the router does not
		// serve.
		{
			Name: "wan", Up: true, Proto: "dhcp", Device: "eth1", L3Device: "eth1",
			IPv4: []openwrt.IfaceAddr{{Address: "203.0.113.7", Mask: 24}},
		},
		// An interface that is down serves nothing now.
		{
			Name: "guest", Up: false, Proto: openwrt.ProtoStatic,
			IPv4: []openwrt.IfaceAddr{{Address: "192.0.2.129", Mask: 25}},
		},
		{
			Name: "loopback", Up: true, Proto: openwrt.ProtoStatic, Device: "lo", L3Device: "lo",
			IPv4: []openwrt.IfaceAddr{{Address: "127.0.0.1", Mask: 8}},
		},
	}

	got := buildOpenWrtNetworks(ifaces, map[string]int{"br-lan.20": 20})

	assert.Equal(t, []Network{
		{Prefix: netip.MustParsePrefix("198.51.100.0/24"), Name: "iot", VLAN: 20},
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Name: "lan"},
		{Prefix: netip.MustParsePrefix("2001:db8:0:1::/64"), Name: "lan"},
	}, got)
}

// fakeUbus answers ubus JSON-RPC from canned results with "object method" as
// key. The key of `file exec` is its command line. The key of `uci get` is its
// config and type. Every login works. It refuses a key with no answer, as rpcd
// refuses a call that the ACL does not allow.
func fakeUbus(t *testing.T, answers map[string]string) *openwrt.OpenWrt {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []jsontext.Value `json:"params"`
		}

		if err := json.UnmarshalRead(r.Body, &req); err != nil || len(req.Params) != 4 {
			http.Error(w, "bad request", http.StatusBadRequest)

			return
		}

		var object, method string

		_ = json.Unmarshal(req.Params[1], &object)
		_ = json.Unmarshal(req.Params[2], &method)

		var args struct {
			Command string   `json:"command"`
			Params  []string `json:"params"`
			Config  string   `json:"config"`
			Type    string   `json:"type"`
			Family  int      `json:"family"`
			Device  string   `json:"device"`
		}

		_ = json.Unmarshal(req.Params[3], &args)

		key := object + " " + method

		var path struct {
			Path string `json:"path"`
		}

		_ = json.Unmarshal(req.Params[3], &path)

		switch key {
		case "file read":
			key = "read " + path.Path
		case "session login":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[0,{"ubus_rpc_session":"s"}]}`))

			return
		case "file exec":
			key = strings.Join(append([]string{"exec", args.Command}, args.Params...), " ")
		case "uci get":
			key = "uci " + args.Config + " " + args.Type
		case "luci-rpc getDHCPLeases":
			key += " " + strconv.Itoa(args.Family)
		case "iwinfo assoclist":
			key += " " + args.Device
		}

		answer, ok := answers[key]
		if !ok {
			answer = "[6]"
		}

		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + answer + `}`))
	}))
	t.Cleanup(srv.Close)

	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err)

	p, err := strconv.Atoi(port)
	require.NoError(t, err)

	client, err := openwrt.New(&openwrt.Config{Host: host, Port: p}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	return client
}

// testOpenWrtWith builds the plugin over a fake router that replies with
// answers.
func testOpenWrtWith(t *testing.T, answers map[string]string) *OpenWrt {
	t.Helper()

	o := testOpenWrt(t)
	o.client = fakeUbus(t, answers)

	return o
}

// The tables of a router with one static host and two clients.
var openWrtAnswers = map[string]string{
	"uci dhcp host": `[0,{"values":{"cfg1":{".index":3,"name":"living-room-tv","mac":"00:00:5e:00:53:14","ip":"192.0.2.50"}}}]`,
	"exec /sbin/ip -4 neigh show": `[0,{"code":0,"stdout":` +
		`"192.0.2.50 dev br-lan lladdr 00:00:5e:00:53:14 ref 1 used 0/0/0 probes 4 REACHABLE\n` +
		`192.0.2.119 dev br-lan lladdr 00:00:5e:00:53:11 used 0/0/0 probes 1 STALE\n"}]`,
	"exec /sbin/ip -6 neigh show": `[0,{"code":0,"stdout":""}]`,
	"luci-rpc getDHCPLeases 4": `[0,{"dhcp_leases":[` +
		`{"hostname":"android-1234","macaddr":"00:00:5E:00:53:14","ipaddr":"192.0.2.50"},` +
		`{"hostname":"phone-a","macaddr":"00:00:5E:00:53:11","ipaddr":"192.0.2.119"}]}]`,
	"luci-rpc getDHCPLeases 6": `[0,{"dhcp6_leases":[]}]`,
	"network.interface dump": `[0,{"interface":[{"interface":"lan","up":true,"proto":"static",` +
		`"device":"br-lan.10","l3_device":"br-lan.10","ipv4-address":[{"address":"192.0.2.1","mask":24}]}]}]`,
	"uci network device":      `[0,{"values":{}}]`,
	"uci network bridge-vlan": `[0,{"values":{"cfg2":{"device":"br-lan","vlan":"10"}}}]`,
	"luci-rpc getNetworkDevices": `[0,{"br-lan.10":{"name":"br-lan.10","type":1,"up":true,"mac":"00:00:5E:00:53:01"},` +
		`"lo":{"name":"lo","type":772,"up":true,"mac":"00:00:00:00:00:00"}}]`,
}

func TestOpenWrtDiscoverReadsEveryTable(t *testing.T) {
	t.Parallel()

	facts, err := testOpenWrtWith(t, openWrtAnswers).Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, facts, 3)

	tv := factAt(t, facts, "192.0.2.50")
	assert.Equal(t, "living-room-tv", tv.Host.Hostname())
	assert.Equal(t, dbtype.HostnameFromDHCPStatic, tv.HostnameSource)
	assert.True(t, tv.Present)

	phone := factAt(t, facts, "192.0.2.119")
	assert.Equal(t, "phone-a", phone.Host.Hostname())
	assert.Equal(t, dbtype.HostnameFromDHCPLease, phone.HostnameSource)

	router := factAt(t, facts, "192.0.2.1")
	assert.Equal(t, "00:00:5e:00:53:01", router.Host.MAC)
	assert.True(t, router.Present)
	assert.Equal(t, "br-lan.10", router.Detail["interface"])
}

// ownFacts runs the own-address mapping as Discover does, without a router.
func ownFacts(t *testing.T, ifaces []openwrt.Interface, devices map[string]openwrt.NetDevice) []Fact {
	t.Helper()

	o := testOpenWrt(t)
	c := make(claims)

	o.collectOwn(c, ifaces, devices)

	out, err := buildFacts(t.Context(), o.logger, o.now(), c)
	require.NoError(t, err)

	return out
}

// A router that serves several segments shares one hardware address across
// them. So each of its addresses returns as the same device, present.
func TestOpenWrtOwnAddressesCarryTheirDevicesHardwareAddress(t *testing.T) {
	t.Parallel()

	ifaces := []openwrt.Interface{
		{
			Name: "lan", Up: true, Proto: openwrt.ProtoStatic, Device: "br-lan", L3Device: "br-lan",
			IPv4: []openwrt.IfaceAddr{{Address: "192.0.2.1", Mask: 24}},
			IPv6: []openwrt.IfaceAddr{{Address: "2001:db8:0:1::1", Mask: 64}},
		},
		{
			Name: "iot", Up: true, Proto: openwrt.ProtoStatic, Device: "br-lan.20",
			IPv4: []openwrt.IfaceAddr{{Address: "198.51.100.1", Mask: 24}},
		},
	}
	devices := map[string]openwrt.NetDevice{
		"br-lan":    {MAC: "00:00:5E:00:53:01"},
		"br-lan.20": {MAC: "00:00:5E:00:53:01"},
	}

	out := ownFacts(t, ifaces, devices)
	require.Len(t, out, 3)

	for _, f := range out {
		assert.Equal(t, "00:00:5e:00:53:01", f.Host.MAC)
		assert.True(t, f.Present, "the router answered the read")
		assert.Empty(t, f.Host.Hostname(), "the router gives itself no name here")
	}

	assert.Equal(t, "br-lan", factAt(t, out, "2001:db8:0:1::1").Detail["interface"])
	assert.Equal(t, "br-lan.20", factAt(t, out, "198.51.100.1").Detail["interface"],
		"an interface with no l3_device falls back to its configured device")
}

// An uplink is on a link that the router does not serve. An interface that is
// down has nothing. A device with no hardware address names no device.
func TestOpenWrtOwnAddressesSkipWhatIsNotASegmentTheRouterServes(t *testing.T) {
	t.Parallel()

	ifaces := []openwrt.Interface{
		{
			Name: "wan", Up: true, Proto: "dhcp", L3Device: "eth1",
			IPv4: []openwrt.IfaceAddr{{Address: "203.0.113.7", Mask: 24}},
		},
		{
			Name: "guest", Up: false, Proto: openwrt.ProtoStatic, L3Device: "br-guest",
			IPv4: []openwrt.IfaceAddr{{Address: "192.0.2.129", Mask: 25}},
		},
		{
			Name: "vpn", Up: true, Proto: openwrt.ProtoStatic, L3Device: "wg0",
			IPv4: []openwrt.IfaceAddr{{Address: "198.51.100.129", Mask: 25}},
		},
		{
			Name: "loopback", Up: true, Proto: openwrt.ProtoStatic, L3Device: "lo",
			IPv4: []openwrt.IfaceAddr{{Address: "127.0.0.1", Mask: 8}},
		},
	}
	devices := map[string]openwrt.NetDevice{
		"eth1":     {MAC: "00:00:5E:00:53:02"},
		"br-guest": {MAC: "00:00:5E:00:53:01"},
		"wg0":      {},
		"lo":       {MAC: "00:00:00:00:00:00"},
	}

	assert.Empty(t, ownFacts(t, ifaces, devices))
}

func TestOpenWrtDiscoverFailsWhenTheOwnAddressesFail(t *testing.T) {
	t.Parallel()

	answers := maps.Clone(openWrtAnswers)
	delete(answers, "luci-rpc getNetworkDevices")

	facts, err := testOpenWrtWith(t, answers).Discover(t.Context())
	require.ErrorIs(t, err, ErrAuth)
	assert.Contains(t, err.Error(), "read network devices")
	assert.Empty(t, facts)
}

// If rpcd refuses one table, the read fails. The error says that the login
// does not have an ACL grant.
func TestOpenWrtDiscoverFailsWhenOneTableIsRefused(t *testing.T) {
	t.Parallel()

	answers := maps.Clone(openWrtAnswers)
	delete(answers, "exec /sbin/ip -4 neigh show")
	delete(answers, "exec /sbin/ip -6 neigh show")

	facts, err := testOpenWrtWith(t, answers).Discover(t.Context())
	require.ErrorIs(t, err, ErrAuth)
	assert.Empty(t, facts)
}

func TestOpenWrtNetworksReadsVLANs(t *testing.T) {
	t.Parallel()

	nets, err := testOpenWrtWith(t, openWrtAnswers).Networks(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []Network{{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Name: "lan", VLAN: 10}}, nets)
}

func TestOpenWrtNetworksFailsWithoutVLANs(t *testing.T) {
	t.Parallel()

	answers := maps.Clone(openWrtAnswers)
	delete(answers, "uci network device")
	delete(answers, "uci network bridge-vlan")

	nets, err := testOpenWrtWith(t, answers).Networks(t.Context())
	require.ErrorIs(t, err, ErrAuth)
	assert.Empty(t, nets)
}

func TestOpenWrtNetworksFailsWithoutInterfaces(t *testing.T) {
	t.Parallel()

	answers := maps.Clone(openWrtAnswers)
	delete(answers, "network.interface dump")

	_, err := testOpenWrtWith(t, answers).Networks(t.Context())
	require.ErrorIs(t, err, ErrAuth)
}

func TestClassifyOpenWrt(t *testing.T) {
	t.Parallel()

	assert.ErrorIs(t, classifyOpenWrt(openwrt.ErrUnauthorized), ErrAuth)
	assert.ErrorIs(t, classifyOpenWrt(openwrt.ErrUnreachable), ErrUnreachable)
	assert.ErrorIs(t, classifyOpenWrt(openwrt.ErrTLS), ErrUnreachable)

	// Not a problem that a retry can fix, and not a credentials problem. So
	// it stays as it is.
	err := classifyOpenWrt(openwrt.ErrNotFound)
	assert.NotErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}
