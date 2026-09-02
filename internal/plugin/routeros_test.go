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

// pinned is the moment every fact in these tests is stamped with, so that
// SeenAt is an assertion rather than a wobble.
var pinned = time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)

// Addresses come from RFC 5737 and hardware addresses from RFC 7042, both
// reserved for documentation, so nothing here names a real device.

// testRouterOS builds the plugin without a client. Every test here exercises
// the mapping, which is the half that holds the decisions; the wire half is
// pkg/routeros and has its own tests.
func testRouterOS(t *testing.T) *RouterOS {
	t.Helper()

	return &RouterOS{
		name:   routerOSPrefix + "gateway",
		logger: slog.New(slog.DiscardHandler),
		now:    func() time.Time { return pinned },
	}
}

// facts runs the mapping over both tables the way Discover does, without a
// router to read them from.
func facts(t *testing.T, arp []routeros.ARPEntry, leases []routeros.DHCPLease) []Fact {
	t.Helper()

	r := testRouterOS(t)
	c := make(claims)

	r.collectARP(t.Context(), c, arp)
	r.collectLeases(t.Context(), c, leases)
	shareByDevice(c)

	out, err := r.build(t.Context(), c)
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

// The name becomes a database key that later rows are matched against, so a
// missing one is refused rather than defaulted.
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

// The load-bearing rule: a router keeps a failed entry for very nearly every
// address in every subnet it serves, so most of the table is not evidence of
// anything. Without this the first run would invent a device per address.
func TestCollectARPDropsTheFailedHalfOfTheTable(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{
			ID: "*1", Address: "192.0.2.12", MACAddress: "00:00:5E:00:53:01",
			Interface: "vlan10", Complete: true, Dynamic: true, Status: "reachable",
		},
		// The shape of most of the table: an address the router asked about
		// and got no answer for. No mac-address member at all.
		{ID: "*2", Address: "192.0.2.13", Interface: "vlan10", Status: "failed"},
		{ID: "*3", Address: "192.0.2.14", Interface: "vlan10", Status: "failed"},
	}

	out := facts(t, arp, nil)

	require.Len(t, out, 1)
	assert.Equal(t, "192.0.2.12", out[0].Host.IP)
	assert.True(t, out[0].Present)
}

// An entry the router itself no longer believes still names hardware, so it is
// kept as a claim and denied as a sighting.
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

// The all-zero address parses perfectly well and identifies nothing, so it must
// not become an identity two unrelated devices share.
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

// A device's name and its presence come from different tables, and the merge is
// the whole point: emitting them as two facts would have the ARP row's empty
// name overwrite the lease's on the same claim.
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

// A static lease is one an operator bound deliberately, so the name recorded
// against it is one they have already looked at and left alone.
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

// The real tables read "Workstation - wired" beside a host-name of
// "workstation". The first has spaces and a dash and names an interface; only the
// second is a hostname.
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

// One machine with two NICs is two devices as far as anything here can tell,
// and the comment is what distinguishes them. Both keep the same host-name.
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

// A static lease for a device nobody has plugged in is configuration. Treating
// it as a sighting would report an unplugged printer as online forever.
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

// The switch holds a configured address and never asks for one, so its lease
// reads "waiting, never" while the ARP table has it reachable. One devices row
// cannot hold both, which is why the claim carries presence per source -- and
// why either table saying the device is here is enough.
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

// A lease being renegotiated carries both, and the one in use is the one that
// says where the device is.
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

// A lease naming neither a device nor a name is configuration for an address.
func TestLeaseWithNothingToSayIsDropped(t *testing.T) {
	t.Parallel()

	leases := []routeros.DHCPLease{{Address: "192.0.2.70", Status: "waiting"}}

	assert.Empty(t, facts(t, nil, leases))
}

// A resolved name belongs to whoever resolved it. The router's row for a
// nameless device must not come back carrying this host's reverse DNS, filed
// under the router's claim.
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

// The OUI lookup is what internal/hosts is for, and it is why a fact is built
// through it rather than assembled here.
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

// Map iteration is randomised, so facts are sorted by address before they
// leave. Nothing downstream depends on it, but a run that reports its rows in a
// different sequence every time cannot be diffed against the last one.
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

	// Numerically, not lexically: "192.0.2.9" sorts after "192.0.2.10"
	// as a string, which is the bug a naive sort would have.
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

// A user holding read,rest-api but not api authenticates and is then refused
// every command, which RouterOS reports as a 500. It is a credentials problem
// however it arrives.
func TestClassifyRouterOSMapsTheRefusedCommandTo500(t *testing.T) {
	t.Parallel()

	err := &routeros.Error{
		Status:  500,
		Message: "Internal Server Error",
		Detail:  "std failure: not allowed (9)",
	}

	require.ErrorIs(t, classifyRouterOS(err), ErrAuth)
}

// A router with no REST service is neither unreachable nor refusing
// credentials, and calling it either would be a lie in the log.
func TestClassifyRouterOSLeavesAMissingEndpointAlone(t *testing.T) {
	t.Parallel()

	got := classifyRouterOS(routeros.ErrNotFound)

	require.ErrorIs(t, got, routeros.ErrNotFound)
	assert.NotErrorIs(t, got, ErrUnreachable)
	assert.NotErrorIs(t, got, ErrAuth)
}

// routerFor stands up a router answering the two tables this plugin reads.
// A handler left nil answers 500, which is how a table that fails while its
// sibling succeeds is arranged.
func routerFor(t *testing.T, arpBody, leaseBody string) *RouterOS {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := ""

		switch req.URL.Path {
		case "/rest/ip/arp":
			body = arpBody
		case "/rest/ip/dhcp-server/lease":
			body = leaseBody
		}

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

// Half a router is worth ingesting. The facts that arrived are true whatever
// happened to the rest, so they come back beside the error rather than instead
// of it -- this is what replaced an explicit Partial flag.
func TestDiscoverReturnsWhatItGotWhenOneTableFails(t *testing.T) {
	t.Parallel()

	out, err := routerFor(t, liveARP, "").Discover(t.Context())

	require.Error(t, err, "the lease table failed and must say so")
	assert.Contains(t, err.Error(), "dhcp")

	require.Len(t, out, 1, "the arp table answered and its rows stand")
	assert.Equal(t, "192.0.2.12", out[0].Host.IP)
	assert.Empty(t, out[0].Host.Hostname(), "the names were in the table that failed")
}

func TestDiscoverReturnsNothingWhenBothTablesFail(t *testing.T) {
	t.Parallel()

	out, err := routerFor(t, "", "").Discover(t.Context())

	require.Error(t, err)
	assert.Empty(t, out)
}

// An empty table is an answer, not a failure: a router with nothing in its ARP
// table has told us something.
func TestDiscoverAcceptsEmptyTables(t *testing.T) {
	t.Parallel()

	out, err := routerFor(t, "[]", "[]").Discover(t.Context())

	require.NoError(t, err)
	assert.Empty(t, out)
}

// A device holding two addresses is two facts but one claim. Without sharing
// the name across them, the nameless address clears the name the other found --
// which a real table does produce: a device named on its lease, and the same
// MAC on a second address with no lease at all.
func TestOneDeviceOnTwoAddressesKeepsItsName(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{Address: "192.0.2.14", MACAddress: "00:00:5E:00:53:0B", Complete: true},
		{Address: "192.0.2.106", MACAddress: "00:00:5E:00:53:0B", Complete: true},
	}
	leases := []routeros.DHCPLease{
		{Address: "192.0.2.14", MACAddress: "00:00:5E:00:53:0B", HostName: "host-d", Status: "bound"},
	}

	out := facts(t, arp, leases)

	require.Len(t, out, 2)

	for _, f := range out {
		assert.Equal(t, "host-d", f.Host.Hostname(), "both addresses are the same device")
		assert.Equal(t, dbtype.HostnameFromDHCPStatic, f.HostnameSource)
	}
}

// The better-standing name wins across a device's addresses, not the first one
// the map happened to yield.
func TestSharedNameTakesTheBetterStanding(t *testing.T) {
	t.Parallel()

	leases := []routeros.DHCPLease{
		{
			Address: "192.0.2.60", MACAddress: "00:00:5E:00:53:0A",
			HostName: "dynamic-name", Status: "bound", Dynamic: true,
		},
		{
			Address: "192.0.2.61", MACAddress: "00:00:5E:00:53:0A",
			HostName: "static-name", Status: "bound",
		},
	}

	out := facts(t, nil, leases)

	require.Len(t, out, 2)

	for _, f := range out {
		assert.Equal(t, "static-name", f.Host.Hostname())
		assert.Equal(t, dbtype.HostnameFromDHCPStatic, f.HostnameSource)
	}
}

// The claim a device's facts write is keyed on the device, so the detail has to
// be the union of what its addresses carry. Without it the lease comment is
// overwritten by the thinner ARP-only detail of an address the device gave up.
func TestDetailIsSharedAcrossADevicesAddresses(t *testing.T) {
	t.Parallel()

	arp := []routeros.ARPEntry{
		{
			Address: "192.0.2.14", MACAddress: "00:00:5E:00:53:0B",
			Interface: "vlan10", Complete: true, Status: "stale",
		},
		{
			Address: "192.0.2.106", MACAddress: "00:00:5E:00:53:0B",
			Interface: "vlan10", Complete: true, Status: "stale",
		},
	}
	leases := []routeros.DHCPLease{{
		Address: "192.0.2.14", MACAddress: "00:00:5E:00:53:0B",
		HostName: "host-d", Status: "bound", Server: "dhcp1", Comment: "Host D, virtual",
	}}

	out := facts(t, arp, leases)

	require.Len(t, out, 2)

	for _, f := range out {
		assert.Equal(t, "Host D, virtual", f.Detail["dhcp_comment"], "the lease detail reaches both addresses")
		assert.Equal(t, "dhcp1", f.Detail["dhcp_server"])
		assert.Equal(t, "bound", f.Detail["dhcp_status"])
		assert.Equal(t, "vlan10", f.Detail["interface"])
	}

	// Cloned rather than shared, so one fact cannot mutate another's.
	out[0].Detail["injected"] = "x"
	assert.NotContains(t, out[1].Detail, "injected")
}
