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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/pkg/routeros"
)

// networkRouter answers the two tables Networks reads. A body left empty
// answers 500, which is how one table failing while the other succeeds is
// arranged.
func networkRouter(t *testing.T, addressBody, vlanBody string) *RouterOS {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := ""

		switch req.URL.Path {
		case "/rest/ip/address":
			body = addressBody
		case "/rest/interface/vlan":
			body = vlanBody
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

	client, err := routeros.New(&routeros.Config{Host: host, Port: p}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	ros, err := NewRouterOS("gateway", client, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	return ros
}

const (
	liveAddresses = `[{".id":"*1","address":"192.0.2.1/24","network":"192.0.2.0",` +
		`"interface":"vlan10","actual-interface":"vlan10"},` +
		`{".id":"*2","address":"198.51.100.1/24","network":"198.51.100.0",` +
		`"interface":"vlan20","actual-interface":"vlan20"}]`

	liveVLANs = `[{".id":"*1","name":"vlan10","vlan-id":"10","interface":"bridge","comment":"Home"},` +
		`{".id":"*2","name":"vlan20","vlan-id":"20","interface":"bridge","comment":"IoT"}]`
)

func TestNetworksJoinTheAddressTableToTheTags(t *testing.T) {
	t.Parallel()

	out, err := networkRouter(t, liveAddresses, liveVLANs).Networks(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 2)
	assert.Equal(t, netip.MustParsePrefix("192.0.2.0/24"), out[0].Prefix)
	assert.Equal(t, 10, out[0].VLAN)
	assert.Equal(t, "Home", out[0].Name)

	assert.Equal(t, netip.MustParsePrefix("198.51.100.0/24"), out[1].Prefix)
	assert.Equal(t, 20, out[1].VLAN)
	assert.Equal(t, "IoT", out[1].Name)
}

// The router reports the address it holds, and the prefix a sweep recorded is
// the base of it. They have to meet at one value or a segment is recorded
// twice.
func TestNetworkPrefixIsMasked(t *testing.T) {
	t.Parallel()

	body := `[{"address":"192.0.2.37/24","interface":"vlan10"}]`

	out, err := networkRouter(t, body, "[]").Networks(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Equal(t, netip.MustParsePrefix("192.0.2.0/24"), out[0].Prefix)
}

// Losing the tags costs the segments their numbers, not their existence.
func TestNetworksSurviveTheVLANTableFailing(t *testing.T) {
	t.Parallel()

	out, err := networkRouter(t, liveAddresses, "").Networks(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vlan")

	require.Len(t, out, 2)
	assert.Zero(t, out[0].VLAN)
	assert.Equal(t, "vlan10", out[0].Name, "the interface name is what is left to call it")
}

// Without the address table there are no segments at all, so nothing comes
// back rather than a list the tags alone cannot fill in.
func TestNetworksAreNothingWithoutTheAddressTable(t *testing.T) {
	t.Parallel()

	out, err := networkRouter(t, "", liveVLANs).Networks(t.Context())

	require.Error(t, err)
	assert.Empty(t, out)
}

func TestNetworksAcceptEmptyTables(t *testing.T) {
	t.Parallel()

	out, err := networkRouter(t, "[]", "[]").Networks(t.Context())

	require.NoError(t, err)
	assert.Empty(t, out)
}

// The WAN is a link the router sits on, and listing it beside the segments
// would report the ISP's prefix as somewhere devices are inventoried.
func TestNetworksLeaveOutWhatTheRouterDoesNotServe(t *testing.T) {
	t.Parallel()

	body := `[{"address":"203.0.113.7/24","interface":"ether1","dynamic":"true"},` +
		`{"address":"192.0.2.1/24","interface":"vlan10","disabled":"true"},` +
		`{"address":"198.51.100.1/24","interface":"vlan20","invalid":"true"},` +
		`{"address":"127.0.0.1/8","interface":"lo"},` +
		`{"address":"169.254.1.1/16","interface":"ether2"},` +
		`{"address":"not-an-address","interface":"ether3"},` +
		`{"address":"192.0.2.1/25","interface":"vlan30"}]`

	out, err := networkRouter(t, body, "[]").Networks(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Equal(t, netip.MustParsePrefix("192.0.2.0/25"), out[0].Prefix)
}

// A segment reached on two addresses is one segment, and which of them names
// it must not depend on the order the router listed them.
func TestOnePrefixIsRecordedOnce(t *testing.T) {
	t.Parallel()

	body := `[{"address":"192.0.2.9/24","interface":"vlan10","comment":"second"},` +
		`{"address":"192.0.2.1/24","interface":"vlan10","comment":"first"}]`

	out, err := networkRouter(t, body, "[]").Networks(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Equal(t, "first", out[0].Name)
}

// A note on the address describes that prefix; a note on the VLAN describes
// every prefix on it.
func TestTheMostSpecificNoteNamesTheSegment(t *testing.T) {
	t.Parallel()

	body := `[{"address":"192.0.2.1/24","interface":"vlan10","comment":"Wired, ground floor"}]`
	vlans := `[{"name":"vlan10","vlan-id":"10","comment":"Home"}]`

	out, err := networkRouter(t, body, vlans).Networks(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Equal(t, "Wired, ground floor", out[0].Name)
	assert.Equal(t, 10, out[0].VLAN, "the tag still comes from the vlan table")
}

// A segment on an untagged interface is a segment. Nothing about it is missing.
func TestAnUntaggedSegmentIsStillASegment(t *testing.T) {
	t.Parallel()

	body := `[{"address":"192.0.2.1/24","interface":"bridge"}]`

	out, err := networkRouter(t, body, liveVLANs).Networks(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Zero(t, out[0].VLAN)
	assert.Equal(t, "bridge", out[0].Name)
}

// An address configured on a bridge port resolves to the VLAN carrying it, so
// the tag is reachable through the name the router settled on.
func TestTheResolvedInterfaceIsTriedWhenTheConfiguredOneMisses(t *testing.T) {
	t.Parallel()

	body := `[{"address":"192.0.2.1/24","interface":"lan","actual-interface":"vlan10"}]`

	out, err := networkRouter(t, body, liveVLANs).Networks(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Equal(t, 10, out[0].VLAN)
	assert.Equal(t, "Home", out[0].Name)
}

// A tag that does not read as a number is left out of the lookup entirely, so
// the segment reports no tag rather than VLAN 0.
func TestAnUnreadableTagLeavesTheSegmentUntagged(t *testing.T) {
	t.Parallel()

	body := `[{"address":"192.0.2.1/24","interface":"vlan10"}]`
	vlans := `[{"name":"vlan10","vlan-id":"none","comment":"Home"}]`

	out, err := networkRouter(t, body, vlans).Networks(t.Context())
	require.NoError(t, err)

	require.Len(t, out, 1)
	assert.Zero(t, out[0].VLAN)
	assert.Equal(t, "vlan10", out[0].Name, "the vlan row was not consulted at all")
}
