package plugin

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// pinned is the time of every fact in these tests. So the tests can assert
// SeenAt exactly.
var pinned = time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

// Addresses come from RFC 5737, and hardware addresses from RFC 7042. Both are
// for documentation. So nothing here names a real device.

// testRouterOS builds the plugin without a client. Every test here exercises
// the mapping, which is the half with the decisions. pkg/routeros is the
// network half, and has its own tests.
func testRouterOS(t *testing.T) *RouterOS {
	t.Helper()

	return &RouterOS{
		name:   routerOSPrefix + "gateway",
		logger: slog.New(slog.DiscardHandler),
		now:    func() time.Time { return pinned },
	}
}

// facts runs the mapping over both tables, as Discover does, without a
// router.
func facts(t *testing.T, arp []routeros.ARPEntry, leases []routeros.DHCPLease) []Fact {
	t.Helper()

	r := testRouterOS(t)
	c := make(claims)

	r.collectARP(t.Context(), c, arp)
	r.collectLeases(t.Context(), c, leases)

	out, err := buildFacts(t.Context(), r.logger, r.now(), c)
	require.NoError(t, err)

	return out
}

func TestNewRouterOSPrefixesTheInstanceName(t *testing.T) {
	t.Parallel()

	client, err := routeros.New(&routeros.Config{Host: "192.0.2.1"}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	p, err := NewRouterOS("gateway", client, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	assert.Equal(t, "routeros:gateway", p.Name())
	assert.Equal(t, dbtype.SourceRouter, p.Kind())
}

// The name becomes a database key, and later rows match against it. So the
// plugin refuses a missing name.
func TestNewRouterOSRefusesAnUnnamedInstance(t *testing.T) {
	t.Parallel()

	client, err := routeros.New(&routeros.Config{Host: "192.0.2.1"}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	_, err = NewRouterOS("", client, slog.New(slog.DiscardHandler))
	require.ErrorIs(t, err, ErrNoInstanceName)
}

func TestNewRouterOSRefusesAMissingClient(t *testing.T) {
	t.Parallel()

	_, err := NewRouterOS("gateway", nil, slog.New(slog.DiscardHandler))
	require.Error(t, err)
}

// The most important rule. A router keeps a failed entry for almost every
// address in every subnet that it serves. So most of the table proves
// nothing. Without this rule, the first run would make up a device for each
// address.
func TestCollectARPDropsTheFailedHalfOfTheTable(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{
			ID: "*1", Address: "192.0.2.12", MACAddress: "00:00:5E:00:53:01",
			Interface: "vlan10", Complete: true, Dynamic: true, Status: "reachable",
		},
		// The shape of most of the table: an address that the router asked
		// about, with no answer. No mac-address member.
		{ID: "*2", Address: "192.0.2.13", Interface: "vlan10", Status: "failed"},
		{ID: "*3", Address: "192.0.2.14", Interface: "vlan10", Status: "failed"},
	}

	out := facts(t, arp, nil)

	require.Len(t, out, 1)
	assert.Equal(t, "192.0.2.12", out[0].Host.IP)
	assert.True(t, out[0].Present)
}

// An entry that the router no longer trusts still names hardware. So the
// plugin keeps it as a claim, but not as a sighting.
func TestCollectARPKeepsDisbelievedEntriesWithoutPresence(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.20", MACAddress: "00:00:5E:00:53:05", Complete: true, Invalid: true},
		{Address: "192.0.2.21", MACAddress: "00:00:5E:00:53:06", Complete: true, Disabled: true},
	}

	out := facts(t, arp, nil)

	require.Len(t, out, 2)

	for _, f := range out {
		assert.False(t, f.Present, "the router does not believe this entry")
		assert.NotEmpty(t, f.Host.MAC)
	}
}

// In a "stale" ARP entry, the router only remembers a hardware address. The
// device stays in the inventory as a claim, and is marked absent.
func TestCollectARPKeepsStaleEntriesWithoutPresence(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.22", MACAddress: "00:00:5E:00:53:07", Complete: true, Dynamic: true, Status: "stale"},
	}

	out := facts(t, arp, nil)

	require.Len(t, out, 1)
	assert.False(t, out[0].Present, "stale is not a sighting")
	assert.Equal(t, "stale", out[0].Detail["arp_status"])
	assert.NotEmpty(t, out[0].Host.MAC, "still a claim about a device")
}

// If the ARP entry of a device is stale, but its DHCP lease is still bound, the
// lease keeps it present. Whether a bound lease must expire by itself is a
// separate question.
func TestCollectARPStaleEntryStaysPresentThroughABoundLease(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.23", MACAddress: "00:00:5E:00:53:08", Complete: true, Status: "stale"},
	}
	leases := []routeros.DHCPLease{
		{Address: "192.0.2.23", MACAddress: "00:00:5E:00:53:08", HostName: "host-d", Status: "bound"},
	}

	out := facts(t, arp, leases)

	require.Len(t, out, 1)
	assert.True(t, out[0].Present, "the bound lease still vouches for it")
}

// The all-zero address parses, but it identifies nothing. So it must not become
// an identity that two unrelated devices share.
func TestCollectARPTreatsTheZeroMACAsNoMAC(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.30", MACAddress: "00:00:00:00:00:00", Complete: true},
	}

	out := facts(t, arp, nil)

	require.Len(t, out, 1)
	assert.Empty(t, out[0].Host.MAC)
}

func TestCollectARPDropsAnUnparseableHardwareAddress(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.40", MACAddress: "not-a-mac", Complete: true},
	}

	assert.Empty(t, facts(t, arp, nil))
}

// The name and the presence of a device come from different tables. The merge
// exists for this reason. With two facts, the empty name of the ARP row would
// overwrite the name of the lease on the same claim.
func TestLeaseNameReachesTheARPEntryForTheSameDevice(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.12", MACAddress: "00:00:5E:00:53:01", Interface: "vlan10", Complete: true},
	}
	leases := []routeros.DHCPLease{
		{
			Address: "192.0.2.12", MACAddress: "00:00:5E:00:53:01",
			HostName: "host-a", Status: "bound", Server: "dhcp1",
		},
	}

	out := facts(t, arp, leases)

	require.Len(t, out, 1, "one device, one fact")
	assert.Equal(t, "host-a", out[0].Host.Hostname())
	assert.True(t, out[0].Present)
	assert.Equal(t, "vlan10", out[0].Detail["interface"])
	assert.Equal(t, "dhcp1", out[0].Detail["dhcp_server"])
}

// An operator bound a static lease on purpose. So the operator already checked
// its name, and kept it.
func TestLeaseHostnameCarriesItsStanding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dynamic routeros.Bool
		want    dbtype.HostnameSource
	}{
		{name: "static", dynamic: false, want: dbtype.HostnameFromDHCPStatic},
		{name: "dynamic", dynamic: true, want: dbtype.HostnameFromDHCPLease},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			leases := []routeros.DHCPLease{{
				Address: "192.0.2.50", MACAddress: "00:00:5E:00:53:06",
				HostName: "workstation", Status: "bound", Dynamic: tt.dynamic,
			}}

			out := facts(t, nil, leases)

			require.Len(t, out, 1)
			assert.Equal(t, "workstation", out[0].Host.Hostname())
			assert.Equal(t, tt.want, out[0].HostnameSource)
		})
	}
}

// Router tables have "Workstation - wired" next to a host-name of
// "workstation". The first has spaces and a dash, and names an interface. Only
// the second is a hostname.
func TestLeaseCommentIsCarriedAsDetailNotAsAName(t *testing.T) {
	t.Parallel()

	leases := []routeros.DHCPLease{{
		Address: "192.0.2.50", MACAddress: "00:00:5E:00:53:06",
		HostName: "workstation", Comment: "Workstation - wired", Status: "bound",
	}}

	out := facts(t, nil, leases)

	require.Len(t, out, 1)
	assert.Equal(t, "workstation", out[0].Host.Hostname())
	assert.Equal(t, "Workstation - wired", out[0].Detail["dhcp_comment"])
}

// For this code, one machine with two NICs is two devices. The comment
// separates them. Both keep the same host-name.
func TestTwoInterfacesOfOneMachineStayTwoFacts(t *testing.T) {
	t.Parallel()

	leases := []routeros.DHCPLease{
		{
			Address: "192.0.2.50", MACAddress: "00:00:5E:00:53:06",
			HostName: "workstation", Comment: "Workstation - wired", Status: "bound",
		},
		{
			Address: "192.0.2.51", MACAddress: "00:00:5E:00:53:07",
			HostName: "workstation", Comment: "Workstation - wireless", Status: "bound",
		},
	}

	out := facts(t, nil, leases)

	require.Len(t, out, 2)
	assert.Equal(t, "workstation", out[0].Host.Hostname())
	assert.Equal(t, "workstation", out[1].Host.Hostname())
	assert.NotEqual(t, out[0].Host.MAC, out[1].Host.MAC)
}

// A static lease for a device that is not connected is configuration. As a
// sighting, it would report a disconnected printer as online forever.
func TestAnUnboundLeaseIsNotASighting(t *testing.T) {
	t.Parallel()

	leases := []routeros.DHCPLease{{
		Address: "198.51.100.80", MACAddress: "00:00:5E:00:53:08",
		HostName: "host-c", Status: "waiting", LastSeen: "never",
	}}

	out := facts(t, nil, leases)

	require.Len(t, out, 1)
	assert.False(t, out[0].Present)
	assert.Equal(t, "host-c", out[0].Host.Hostname(), "it still names a device")
}

// The switch has a configured address and never asks for one. So its lease
// shows "waiting, never", but the ARP table has it reachable. One devices row
// cannot hold both. So the claim has presence for each source. Also, it is
// enough if one table says that the device is here.
func TestPresenceIsEitherTableSayingSo(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "203.0.113.2", MACAddress: "00:00:5E:00:53:09", Complete: true, Status: "reachable"},
	}
	leases := []routeros.DHCPLease{
		{Address: "203.0.113.2", MACAddress: "00:00:5E:00:53:09", Status: "waiting", LastSeen: "never"},
	}

	out := facts(t, arp, leases)

	require.Len(t, out, 1)
	assert.True(t, out[0].Present, "arp says it is reachable now")
	assert.Equal(t, "waiting", out[0].Detail["dhcp_status"])
}

// During a new negotiation, a lease has both addresses. The address in use
// tells where the device is.
func TestLeasePrefersTheAddressInUse(t *testing.T) {
	t.Parallel()

	leases := []routeros.DHCPLease{{
		Address: "192.0.2.60", ActiveAddress: "192.0.2.61",
		MACAddress: "00:00:5E:00:53:0A", HostName: "host-b", Status: "bound",
	}}

	out := facts(t, nil, leases)

	require.Len(t, out, 1)
	assert.Equal(t, "192.0.2.61", out[0].Host.IP)
}

// A lease that names no device and no name is configuration for an address.
func TestLeaseWithNothingToSayIsDropped(t *testing.T) {
	t.Parallel()

	leases := []routeros.DHCPLease{{Address: "192.0.2.70", Status: "waiting"}}

	assert.Empty(t, facts(t, nil, leases))
}

// A resolved name belongs to the source that resolved it. The row of the router
// for a device with no name must not get the reverse DNS of this host, under
// the claim of the router.
func TestFactsCarryNoNameTheRouterDidNotGive(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.10", MACAddress: "00:00:0C:11:22:33", Complete: true},
	}

	out := facts(t, arp, nil)

	require.Len(t, out, 1)
	assert.Empty(t, out[0].Host.Hostname())
	assert.Empty(t, out[0].HostnameSource)
}

// internal/hosts does the OUI lookup. So a fact is built through it.
func TestFactsAreEnrichedThroughHosts(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		// 00:00:0C is Cisco in the IEEE registry.
		{Address: "192.0.2.10", MACAddress: "00:00:0C:11:22:33", Complete: true},
	}

	out := facts(t, arp, nil)

	require.Len(t, out, 1)
	assert.NotEmpty(t, out[0].Host.Vendor())
	assert.Equal(t, pinned, out[0].SeenAt)
}

// Map iteration is random. So the plugin sorts facts by address before they
// leave. Nothing later depends on it. But a run that reports its rows in a
// different order every time cannot be compared with the last run.
func TestFactsComeBackSortedByAddress(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.9", MACAddress: "00:00:5E:00:53:03", Complete: true},
		{Address: "198.51.100.7", MACAddress: "00:00:5E:00:53:01", Complete: true},
		{Address: "192.0.2.10", MACAddress: "00:00:5E:00:53:02", Complete: true},
	}
	leases := []routeros.DHCPLease{
		{Address: "192.0.2.2", MACAddress: "00:00:5E:00:53:04", HostName: "host-e", Status: "bound"},
	}

	// By number. As a string, "192.0.2.9" sorts after "192.0.2.10". A simple
	// sort would have this bug.
	want := []string{"192.0.2.2", "192.0.2.9", "192.0.2.10", "198.51.100.7"}

	for range 10 {
		out := facts(t, arp, leases)

		got := make([]string, 0, len(out))
		for _, f := range out {
			got = append(got, f.Host.IP)
		}

		require.Equal(t, want, got)
	}
}

func TestClassifyRouterOSMapsWhatThePollerActsOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   error
		want error
	}{
		{name: "refused credentials", in: routeros.ErrUnauthorized, want: ErrAuth},
		{name: "unreachable", in: routeros.ErrUnreachable, want: ErrUnreachable},
		{name: "certificate", in: routeros.ErrTLS, want: ErrUnreachable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := classifyRouterOS(tt.in)

			require.ErrorIs(t, got, tt.want)
			assert.ErrorIs(t, got, tt.in, "the cause survives the mapping")
		})
	}
}

// A user with read,rest-api but not api authenticates. Then the router refuses
// every command, and RouterOS reports a 500. It is a credentials problem, in
// any form.
func TestClassifyRouterOSMapsTheRefusedCommandTo500(t *testing.T) {
	t.Parallel()

	err := &routeros.Error{
		Status:  500,
		Message: "Internal Server Error",
		Detail:  "std failure: not allowed (9)",
	}

	require.ErrorIs(t, classifyRouterOS(err), ErrAuth)
}

// A router with no REST service is not unreachable, and does not refuse
// credentials. Either name would give a wrong log.
func TestClassifyRouterOSLeavesAMissingEndpointAlone(t *testing.T) {
	t.Parallel()

	got := classifyRouterOS(routeros.ErrNotFound)

	require.ErrorIs(t, got, routeros.ErrNotFound)
	assert.NotErrorIs(t, got, ErrUnreachable)
	assert.NotErrorIs(t, got, ErrAuth)
}

// routerFor starts a router that answers the ARP and lease tables, with no own
// addresses.
func routerFor(t *testing.T, arpBody, leaseBody string) *RouterOS {
	t.Helper()

	return routerAnswering(t, map[string]string{
		"/rest/ip/arp":               arpBody,
		"/rest/ip/dhcp-server/lease": leaseBody,
		"/rest/ip/address":           "[]",
		"/rest/interface":            "[]",
	})
}

// routerAnswering starts a router that answers each path with its body. An
// empty or missing body answers 500. So one table can fail while the others
// succeed.
func routerAnswering(t *testing.T, bodies map[string]string) *RouterOS {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := bodies[req.URL.Path]

		if body == "" {
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

	client, err := routeros.New(
		&routeros.Config{Host: host, Port: p},
		slog.New(slog.DiscardHandler),
	)
	require.NoError(t, err)

	ros, err := NewRouterOS("gateway", client, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	ros.now = func() time.Time { return pinned }

	return ros
}

const (
	liveARP = `[{".id":"*1","address":"192.0.2.12","mac-address":"00:00:5E:00:53:01",` +
		`"interface":"vlan10","complete":"true","dynamic":"true","status":"reachable"},` +
		`{".id":"*2","address":"192.0.2.13","interface":"vlan10","status":"failed"}]`

	liveLeases = `[{".id":"*1","address":"192.0.2.12","mac-address":"00:00:5E:00:53:01",` +
		`"host-name":"host-a","status":"bound","dynamic":"false","server":"dhcp1",` +
		`"comment":"Server, rack 4U"}]`
)

func TestDiscoverMergesBothTables(t *testing.T) {
	t.Parallel()

	out, err := routerFor(t, liveARP, liveLeases).Discover(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1, "the failed arp row is dropped and the rest merges")
	assert.Equal(t, "host-a", out[0].Host.Hostname())
	assert.Equal(t, dbtype.HostnameFromDHCPStatic, out[0].HostnameSource)
	assert.True(t, out[0].Present)
	assert.Equal(t, "vlan10", out[0].Detail["interface"])
	assert.Equal(t, "Server, rack 4U", out[0].Detail["dhcp_comment"])
}

func TestDiscoverFailsWhenOneTableFails(t *testing.T) {
	t.Parallel()

	out, err := routerFor(t, liveARP, "").Discover(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "dhcp")
	assert.Empty(t, out)
}

func TestDiscoverReturnsNothingWhenBothTablesFail(t *testing.T) {
	t.Parallel()

	out, err := routerFor(t, "", "").Discover(t.Context())

	require.Error(t, err)
	assert.Empty(t, out)
}

// An empty table is an answer. A router with an empty ARP table told
// something.
func TestDiscoverAcceptsEmptyTables(t *testing.T) {
	t.Parallel()

	out, err := routerFor(t, "[]", "[]").Discover(t.Context())

	require.NoError(t, err)
	assert.Empty(t, out)
}

// Each address of a device makes its own claim. So each fact keeps the name and
// detail of its own rows. A lease on one address tells nothing about the
// other.
func TestEachAddressKeepsItsOwnNameAndDetail(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.14", MACAddress: "00:00:5E:00:53:0B", Interface: "vlan10", Complete: true},
		{Address: "198.51.100.14", MACAddress: "00:00:5E:00:53:0B", Interface: "vlan30", Complete: true},
	}
	leases := []routeros.DHCPLease{{
		Address: "192.0.2.14", MACAddress: "00:00:5E:00:53:0B",
		HostName: "host-d", Status: "bound", Server: "dhcp1", Comment: "Host D",
	}}

	out := facts(t, arp, leases)

	require.Len(t, out, 2)

	assert.Equal(t, "192.0.2.14", out[0].Host.IP)
	assert.Equal(t, "host-d", out[0].Host.Hostname())
	assert.Equal(t, "Host D", out[0].Detail["dhcp_comment"])
	assert.Equal(t, "vlan10", out[0].Detail["interface"])

	assert.Equal(t, "198.51.100.14", out[1].Host.IP)
	assert.Empty(t, out[1].Host.Hostname(), "no lease names this address")
	assert.NotContains(t, out[1].Detail, "dhcp_comment")
	assert.Equal(t, "vlan30", out[1].Detail["interface"])
}

// own runs the own-address mapping as Discover does, without a router.
func own(t *testing.T, addrs []routeros.IPAddress, ifaces []routeros.Interface) []Fact {
	t.Helper()

	r := testRouterOS(t)
	c := make(claims)

	r.collectOwn(t.Context(), c, addrs, ifaces)

	out, err := buildFacts(t.Context(), r.logger, r.now(), c)
	require.NoError(t, err)

	return out
}

// A router that serves several segments shares one hardware address across
// them. So each of its addresses returns as the same device, present.
func TestOwnAddressesCarryTheirInterfacesHardwareAddress(t *testing.T) {
	t.Parallel()

	addrs := []routeros.IPAddress{
		{Address: "192.0.2.1/24", Interface: "vlan10"},
		{Address: "198.51.100.1/24", Interface: "vlan20"},
		{Address: "203.0.113.1/24", Interface: "port1", ActualInterface: "bridge"},
	}
	ifaces := []routeros.Interface{
		{Name: "vlan10", MACAddress: "00:00:5E:00:53:01"},
		{Name: "vlan20", MACAddress: "00:00:5E:00:53:01"},
		{Name: "bridge", MACAddress: "00:00:5E:00:53:01"},
	}

	out := own(t, addrs, ifaces)

	require.Len(t, out, 3)

	for _, f := range out {
		assert.Equal(t, "00:00:5e:00:53:01", f.Host.MAC)
		assert.True(t, f.Present, "the router answered the read")
		assert.Empty(t, f.Host.Hostname(), "the router gives itself no name here")
	}

	assert.Equal(t, "192.0.2.1", out[0].Host.IP, "the prefix length is dropped")
	assert.Equal(t, "vlan10", out[0].Detail["interface"], "each address keeps its own interface")
	assert.Equal(t, "vlan20", out[1].Detail["interface"])
}

// The WAN lease is dynamic, and on a link that the router does not serve.
// Disabled and invalid rows, and rows with no hardware, also tell nothing
// about a device.
func TestOwnAddressesSkipWhatIsNotASegmentTheRouterServes(t *testing.T) {
	t.Parallel()

	addrs := []routeros.IPAddress{
		{Address: "203.0.113.7/24", Interface: "ether1", Dynamic: true},
		{Address: "192.0.2.1/24", Interface: "vlan10", Disabled: true},
		{Address: "198.51.100.1/24", Interface: "vlan20", Invalid: true},
		{Address: "192.0.2.129/25", Interface: "wg0"},
		{Address: "not-an-address", Interface: "vlan10"},
	}
	ifaces := []routeros.Interface{
		{Name: "ether1", MACAddress: "00:00:5E:00:53:02"},
		{Name: "vlan10", MACAddress: "00:00:5E:00:53:01"},
		{Name: "vlan20", MACAddress: "00:00:5E:00:53:01"},
		{Name: "wg0"},
	}

	assert.Empty(t, own(t, addrs, ifaces))
}

func TestDiscoverFailsWhenTheOwnAddressesFail(t *testing.T) {
	t.Parallel()

	out, err := routerAnswering(t, map[string]string{
		"/rest/ip/arp":               liveARP,
		"/rest/ip/dhcp-server/lease": liveLeases,
		"/rest/interface":            "[]",
	}).Discover(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read addresses")
	assert.Empty(t, out)
}

func TestDiscoverReportsTheRoutersOwnAddresses(t *testing.T) {
	t.Parallel()

	out, err := routerAnswering(t, map[string]string{
		"/rest/ip/arp":               "[]",
		"/rest/ip/dhcp-server/lease": "[]",
		"/rest/ip/address":           `[{".id":"*1","address":"192.0.2.1/24","interface":"vlan10","dynamic":"false"}]`,
		"/rest/interface":            `[{".id":"*1","name":"vlan10","mac-address":"00:00:5E:00:53:01"}]`,
	}).Discover(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Equal(t, "192.0.2.1", out[0].Host.IP)
	assert.Equal(t, "00:00:5e:00:53:01", out[0].Host.MAC)
	assert.Equal(t, "vlan10", out[0].Detail["interface"])
}
