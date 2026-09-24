package web

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

type trafficSource struct{}

func (trafficSource) Name() string            { return "netflow:test" }
func (trafficSource) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

// recordTraffic writes flows the way the recorder does in production.
func recordTraffic(t *testing.T, store *inventory.Store, flows ...plugin.Flow) {
	t.Helper()

	rec := inventory.NewTrafficRecorder(store, testLogger(), nil)
	rec.Add(trafficSource{}, flows)
	require.NoError(t, rec.Flush(t.Context()))
}

func tcp(src, dst string, dstPort uint16, bytes uint64) plugin.Flow {
	return plugin.Flow{
		Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: 51000, DstPort: dstPort, Protocol: 6,
		Bytes: bytes, Packets: 1, End: time.Now(),
	}
}

func sweptPair(t *testing.T) *inventory.Store {
	t.Helper()

	store := testStore(t)

	require.NoError(t, store.RecordNetworks(t.Context(), []plugin.Network{{
		Prefix: netip.MustParsePrefix(prefix), Name: "home", VLAN: 10,
	}}))

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), []scanner.Host{
		host("192.0.2.10", macA, "laptop.example.com"),
		host("192.0.2.11", macB, "nas.example.com"),
	})
	require.NoError(t, err)

	return store
}

// tabKey is the tab key of the recorded network cidr.
func tabKey(t *testing.T, store *inventory.Store, cidr string) string {
	t.Helper()

	nets, err := store.ListNetworks(t.Context())
	require.NoError(t, err)

	for _, n := range nets {
		if n.CIDR == cidr {
			return networkTabKey(n.ID)
		}
	}

	t.Fatalf("no network %s", cidr)

	return ""
}

// afterForm is the section below its filter form, whose service choices list
// everything whatever is shown.
func afterForm(body string) string {
	if i := strings.Index(body, "</form>"); i >= 0 {
		return body[i:]
	}

	return body
}

func TestDevicePageSaysWhenTrafficIsNotCollected(t *testing.T) {
	t.Parallel()

	rec := get(t, newWebHandler(t, sweptPair(t)), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `<h2 class="section">Talks to</h2>`)
	assert.Contains(t, body, "Traffic collection is not set up")
	assert.NotContains(t, body, "Traffic period", "no period switch when there is nothing to switch")
}

func TestDevicePageSaysWhenThisDeviceExchangedNothing(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)

	// Traffic exists, but only between the other device and the internet.
	recordTraffic(t, store, tcp("192.0.2.11", "198.51.100.7", 443, 100))

	rec := get(t, newWebHandler(t, store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No traffic recorded for this device in the last 24 hours.")
}

func TestDevicePageShowsWhoTheDeviceTalksTo(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)

	recordTraffic(t, store,
		tcp("192.0.2.10", "192.0.2.11", 445, 2_500_000),
		// Public resolvers, as in the asn tests: one organisation, two addresses.
		tcp("192.0.2.10", "1.1.1.1", 443, 1_200),
		tcp("192.0.2.10", "1.0.0.1", 443, 800),
	)

	h := newWebHandler(t, store)

	rec := get(t, h, "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	// The figures cover everything; the busiest tab, the home network, opens.
	assert.Contains(t, body, "<dt>Received</dt>")
	assert.Contains(t, body, "<div><dd>1</dd><dt>On your network</dt></div>")
	assert.Contains(t, body, "<div><dd>1</dd><dt>Organisation on the internet</dt></div>")
	assert.Contains(t, body, `aria-current="page">home <span class="tabs__vlan">VLAN 10</span>`)
	assert.Contains(t, body, `>Internet
            <span class="tabs__count">1</span>`)
	assert.Contains(t, body, `<a href="/devices/2">nas.example.com</a>`)
	assert.Contains(t, body, "smb")
	assert.Contains(t, body, "2.5 MB")
	assert.NotContains(t, afterForm(body), "Cloudflare", "the internet is another tab")
	assert.Contains(t, body, "DB-IP", "the ASN data's licence asks for credit")

	// The page speaks plainly: no protocol jargon reaches it.
	for _, word := range []string{"NetFlow", "IPFIX", "flow", "exporter"} {
		assert.NotContains(t, body, word)
	}

	assert.NotContains(t, body, "ZgotmplZ")

	rec = get(t, h, "/devices/1/traffic?tab=internet")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/devices/1?tab=internet", rec.Header().Get("HX-Push-Url"))

	body = rec.Body.String()
	assert.Contains(t, body, "Cloudflare")
	assert.Contains(t, body, "2 addresses")
	assert.Contains(t, body, "2.0 kB")
	assert.NotContains(t, body, `<a href="/devices/2">nas.example.com</a>`)
	assert.NotContains(t, body, "ZgotmplZ")
}

func TestDeviceTrafficFragmentSwitchesPeriod(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store, tcp("192.0.2.10", "192.0.2.11", 22, 100))

	rec := get(t, newWebHandler(t, store), "/devices/1/traffic?window=7d")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "/devices/1?traffic=7d", rec.Header().Get("HX-Push-Url"))

	body := rec.Body.String()
	assert.Contains(t, body, `id="device-traffic"`)
	assert.Contains(t, body, `<option value="7d" selected>Last 7 days</option>`)
	assert.NotContains(t, body, "<html", "a fragment, not a page")
}

func TestDeviceTrafficCollapsesServicesAndUnknownNeighbours(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)

	recordTraffic(t, store,
		// Two services on the same device: one row.
		tcp("192.0.2.10", "192.0.2.11", 445, 2_000),
		tcp("192.0.2.10", "192.0.2.11", 22, 1_000),
		// Two addresses no device holds, on no recorded network: one row, on
		// a tab of their own.
		tcp("192.0.2.10", "198.51.100.7", 80, 300),
		tcp("192.0.2.10", "198.51.100.8", 80, 200),
	)

	h := newWebHandler(t, store)

	rec := get(t, h, "/devices/1/traffic")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Equal(t, 1, strings.Count(body, `<a href="/devices/2">nas.example.com</a>`))
	assert.Contains(t, body, "smb, ssh")
	assert.Contains(t, body, `>Elsewhere on your network
            <span class="tabs__count">2</span>`)
	assert.NotContains(t, body, "198.51.100.0/24")

	rec = get(t, h, "/devices/1/traffic?tab=local")
	require.Equal(t, http.StatusOK, rec.Code)

	body = rec.Body.String()
	assert.Contains(t, body, "2 addresses in <span class=\"mono\">198.51.100.0/24</span>")
	assert.Contains(t, body, "not in your inventory")
	assert.NotContains(t, body, `<a href="/devices/2">nas.example.com</a>`)
}

func TestDeviceTrafficFilters(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)

	recordTraffic(t, store,
		tcp("192.0.2.10", "192.0.2.11", 445, 2_000),
		tcp("192.0.2.10", "1.1.1.1", 443, 1_200),
		tcp("192.0.2.10", "8.8.8.8", 443, 900),
	)

	h := newWebHandler(t, store)

	tests := []struct {
		name        string
		query       string
		push        string
		want, avoid []string
	}{
		{
			name: "internet tab", query: "tab=internet", push: "/devices/1?tab=internet",
			want: []string{"Cloudflare", "Google"}, avoid: []string{"nas.example.com"},
		},
		{
			name: "one service", query: "service=6%2F445", push: "/devices/1?service=6%2F445",
			want: []string{"nas.example.com"}, avoid: []string{"Cloudflare"},
		},
		{
			// Nothing on the home network matches, so the busiest tab left
			// is the internet.
			name: "search opens the tab it matches on", query: "q=google&traffic=7d", push: "/devices/1?q=google&traffic=7d",
			want: []string{"Google"}, avoid: []string{"Cloudflare", "nas.example.com"},
		},
		{
			name: "search on a tab it does not match", query: "q=google&tab=internet&service=6%2F445",
			push: "/devices/1?q=google&service=6%2F445&tab=internet",
			want: []string{"Nothing on the internet in the last 24 hours matches.", "Clear filters"},
		},
		{
			// The internet moved the most, so it is the tab that opens.
			name: "unknown values are dropped", query: "tab=moon&service=https", push: "/devices/1",
			want: []string{"Cloudflare", "Google"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := get(t, h, "/devices/1/traffic?"+tt.query)
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.push, rec.Header().Get("HX-Push-Url"))

			body := afterForm(rec.Body.String())

			for _, w := range tt.want {
				assert.Contains(t, body, w)
			}

			for _, a := range tt.avoid {
				assert.NotContains(t, body, a)
			}
		})
	}
}

func TestDevicePageOpensTheTabFromItsAddress(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store,
		tcp("192.0.2.10", "192.0.2.11", 445, 2_000),
		tcp("192.0.2.10", "1.1.1.1", 443, 1_200),
	)

	rec := get(t, newWebHandler(t, store), "/devices/1?tab=internet")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `aria-current="page">Internet`)
	assert.Contains(t, body, `<input type="hidden" name="tab" value="internet">`)
	assert.NotContains(t, body, `<a href="/devices/2">nas.example.com</a>`)
}

func TestSegmentTabsPickTheNarrowestNetwork(t *testing.T) {
	t.Parallel()

	nets := []*inventory.Network{
		{ID: 1, CIDR: "192.0.2.0/24", Name: "home"},
		{ID: 2, CIDR: "192.0.2.128/25", VLAN: 20},
		{ID: 3, CIDR: "203.0.113.0/24", Name: "guest"},
	}

	local := []*inventory.TrafficPeer{
		{IP: netip.MustParseAddr("192.0.2.5"), Sent: 1},
		{IP: netip.MustParseAddr("192.0.2.200"), Sent: 1},
		{IP: netip.MustParseAddr("198.51.100.9"), Sent: 1},
	}

	tabs := segmentTabs(nets, local, nil, nil, trafficFilter{})

	var got []string
	for _, tab := range tabs {
		got = append(got, tab.Label+"="+strconv.Itoa(tab.Count))
	}

	assert.Equal(t, []string{
		"home=1", "192.0.2.128/25=1", "guest=0", "Elsewhere on your network=1", "Internet=0",
	}, got, "every recorded network is a tab, even an empty one; the leftovers get one when there are any")
}

func TestDeviceTrafficFragmentFallsBackToTheDefaultPeriod(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store, tcp("192.0.2.10", "192.0.2.11", 22, 100))

	rec := get(t, newWebHandler(t, store), "/devices/1/traffic?window=forever")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/devices/1", rec.Header().Get("HX-Push-Url"))
}

func TestDeviceTrafficFragmentIsNotFoundForAMissingDevice(t *testing.T) {
	t.Parallel()

	rec := get(t, newWebHandler(t, sweptPair(t)), "/devices/99/traffic")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHumanBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1000, "1.0 kB"},
		{1_234_567, "1.2 MB"},
		{25_000_000, "25 MB"},
		{3_100_000_000_000, "3.1 TB"},
		{9_000_000_000_000_000, "9000 TB"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, humanBytes(tt.n), "%d", tt.n)
	}
}

func TestHumanCount(t *testing.T) {
	t.Parallel()

	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 15187: "15,187", 1234567: "1,234,567", -4200: "-4,200"} {
		assert.Equal(t, want, humanCount(n), "%d", n)
	}
}

func TestTrafficPageExplainsWhenNothingIsRecorded(t *testing.T) {
	t.Parallel()

	rec := get(t, newWebHandler(t, sweptPair(t)), "/traffic")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "No traffic recorded yet.")
	assert.Contains(t, body, `href="/traffic" aria-current="page"`, "the rail marks the page")
}

func TestTrafficPageSummarisesTheNetwork(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)

	recordTraffic(t, store,
		tcp("192.0.2.10", "192.0.2.11", 445, 9_000_000),
		tcp("192.0.2.11", "1.1.1.1", 443, 4_000),
		tcp("192.0.2.10", "8.8.8.8", 53, 1_000),
	)

	rec := get(t, newWebHandler(t, store), "/traffic?window=7d")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.Contains(t, body, "Busiest devices")
	assert.Contains(t, body, `<a href="/devices/1#traffic">laptop.example.com</a>`)
	assert.Contains(t, body, "9.0 MB")
	assert.Contains(t, body, `<option value="7d" selected>Last 7 days</option>`)

	// Collection started moments ago, so everything is a first contact, and
	// the page says why rather than implying the network changed.
	assert.Contains(t, body, "New this week")
	assert.Contains(t, body, "Cloudflare")
	assert.Contains(t, body, "Google")
	assert.Contains(t, body, "everything\n        counts as new")

	assert.Contains(t, body, "Top internet destinations")
	assert.Contains(t, body, "DB-IP")

	for _, word := range []string{"NetFlow", "IPFIX", "flow", "exporter"} {
		assert.NotContains(t, body, word)
	}

	assert.NotContains(t, body, "ZgotmplZ")
}

func TestTrafficPageGroupsNewContactsAndNarrowsToAGroup(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)

	recordTraffic(t, store,
		// Both devices reach Google for the first time: one row, two devices.
		tcp("192.0.2.10", "8.8.8.8", 443, 1_000),
		tcp("192.0.2.11", "8.8.8.8", 443, 3_000),
		tcp("192.0.2.11", "1.1.1.1", 443, 500),
	)

	_, err := store.UpdateCuration(t.Context(), 2, inventory.Curation{Group: "media"})
	require.NoError(t, err)

	h := newWebHandler(t, store)

	rec := get(t, h, "/traffic")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Equal(t, 2, strings.Count(body, `<span class="strong" title="Google LLC">Google</span>
                                <span class="dim">2 devices</span>`),
		"one row for Google in New this week, one in Top destinations")
	assert.Contains(t, body, `<option value="media">media</option>`)

	rec = get(t, h, "/traffic?group=media")
	require.Equal(t, http.StatusOK, rec.Code)

	body = rec.Body.String()
	assert.Contains(t, body, `<option value="media" selected>media</option>`)
	assert.NotContains(t, body, "laptop.example.com", "the laptop is not in media")
	assert.Contains(t, body, "nas.example.com")

	// A group that no longer exists shows everything rather than nothing.
	rec = get(t, h, "/traffic?group=gone")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "laptop.example.com")
}

// pingSweep is device 192.0.2.10 pinging n documentation addresses, none of
// which answers, plus one refused knock on the NAS's telnet port.
func pingSweep(n int) []plugin.Flow {
	var flows []plugin.Flow

	for i := 1; i <= n; i++ {
		flows = append(flows, plugin.Flow{
			Src: netip.MustParseAddr("192.0.2.10"), Dst: netip.MustParseAddr("198.51.100." + strconv.Itoa(i)),
			Protocol: 1, ICMPType: 8, Bytes: 84, Packets: 1, End: time.Now(),
		})
	}

	return append(flows,
		plugin.Flow{
			Src: netip.MustParseAddr("192.0.2.10"), Dst: netip.MustParseAddr("192.0.2.11"),
			SrcPort: 50000, DstPort: 23, Protocol: 6, TCPFlags: 0x02, Bytes: 60, Packets: 1, End: time.Now(),
		},
		plugin.Flow{
			Src: netip.MustParseAddr("192.0.2.11"), Dst: netip.MustParseAddr("192.0.2.10"),
			SrcPort: 23, DstPort: 50000, Protocol: 6, TCPFlags: 0x14, Bytes: 40, Packets: 1, End: time.Now(),
		},
	)
}

func TestTrafficPageNamesADeviceProbingTheNetwork(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store, pingSweep(25)...)

	rec := get(t, newWebHandler(t, store), "/traffic")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "Probing your network")
	assert.Contains(t, body, `<a href="/devices/1#traffic">laptop.example.com</a>`)
	assert.Contains(t, body, "Tried 26 addresses", "25 pinged and the NAS knocked on")
	assert.Contains(t, body, "0 of 26")
	assert.NotContains(t, body, "ZgotmplZ")
}

func TestTrafficPageSaysWhenNothingProbed(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store, pingSweep(3)...)

	rec := get(t, newWebHandler(t, store), "/traffic")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No device probed your network in the last 24 hours.")
}

func TestDevicePageShowsWhatTheDeviceTriedWhenAsked(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store, pingSweep(25)...)

	h := newWebHandler(t, store)

	rec := get(t, h, "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	// Tries are counted at the top but left out of the rows by default.
	body := rec.Body.String()
	assert.Contains(t, body, "This device probed your network.")
	assert.Contains(t, body, "In one hour it tried 26 addresses on your network.")
	assert.Contains(t, body, "<div><dd>26</dd><dt>Tries with no data</dt></div>")
	assert.Contains(t, body, "0 of those answered, across 26 peers.")
	assert.Contains(t, body, "Show tries with no data")
	assert.NotContains(t, body, "198.51.100.0/24")

	// Asked for, they open on the busiest tab: the sweep.
	rec = get(t, h, "/devices/1/traffic?tried=1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/devices/1?tried=1", rec.Header().Get("HX-Push-Url"))

	body = rec.Body.String()
	assert.Contains(t, body, "25 addresses in <span class=\"mono\">198.51.100.0/24</span>")
	assert.Contains(t, body, `<span class="chip chip--warn">25 tried, 0 answered</span>`)
	assert.Contains(t, body, `<li class="dim">and 5 more</li>`, "a sweep opens to the first 20 addresses")
	assert.Contains(t, body, "Hide them")

	rec = get(t, h, "/devices/1/traffic?tried=1&tab="+tabKey(t, store, prefix))
	require.Equal(t, http.StatusOK, rec.Code)

	body = rec.Body.String()
	assert.Contains(t, body, `<a href="/devices/2">nas.example.com</a>`)
	assert.Contains(t, body, "port 23")
	assert.Contains(t, body, "1 tried, 0 answered")

	// The NAS tried nothing, so it carries no notice.
	rec = get(t, h, "/devices/2")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "This device probed your network.")

	// The search narrows tries like everything else.
	rec = get(t, h, "/devices/1/traffic?tried=1&q=nas")
	require.Equal(t, http.StatusOK, rec.Code)

	body = rec.Body.String()
	assert.Contains(t, body, "nas.example.com")
	assert.NotContains(t, body, "198.51.100.0/24")
	assert.NotContains(t, body, "ZgotmplZ")
}

func TestTrafficPageTabsBySegment(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)

	iot := "198.51.100.0/24"
	require.NoError(t, store.RecordNetworks(t.Context(), []plugin.Network{
		{Prefix: netip.MustParsePrefix(prefix), Name: "home", VLAN: 10},
		{Prefix: netip.MustParsePrefix(iot), Name: "iot", VLAN: 20},
	}))

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(iot), []scanner.Host{
		host("198.51.100.20", "00:00:5e:00:53:03", "plug.example.com"),
	})
	require.NoError(t, err)

	recordTraffic(t, store,
		tcp("192.0.2.10", "8.8.8.8", 443, 1_000),
		tcp("198.51.100.20", "1.1.1.1", 443, 5_000),
	)

	h := newWebHandler(t, store)

	rec := get(t, h, "/traffic")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `aria-current="page">Whole network`)
	assert.Contains(t, body, `>iot <span class="tabs__vlan">VLAN 20</span>
            <span class="tabs__count">1</span>`)
	assert.Contains(t, body, "laptop.example.com")
	assert.Contains(t, body, "plug.example.com")

	key := tabKey(t, store, iot)

	rec = get(t, h, "/traffic?tab="+key+"&window=7d")
	require.Equal(t, http.StatusOK, rec.Code)

	body = rec.Body.String()
	assert.Contains(t, body, `aria-current="page">iot`)
	assert.Contains(t, body, `<input type="hidden" name="tab" value="`+key+`">`)
	assert.Contains(t, body, "plug.example.com")
	assert.Contains(t, body, "Cloudflare")
	assert.NotContains(t, body, "laptop.example.com")
	assert.NotContains(t, body, ">Google<")
	assert.Contains(t, body, "<div><dd>1</dd><dt>Device active</dt></div>")
	assert.NotContains(t, body, "ZgotmplZ")
}
