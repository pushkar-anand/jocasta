package web

import (
	"net/http"
	"net/netip"
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

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), []scanner.Host{
		host("192.0.2.10", macA, "laptop.example.com"),
		host("192.0.2.11", macB, "nas.example.com"),
	})
	require.NoError(t, err)

	return store
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

	rec := get(t, newWebHandler(t, store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.Contains(t, body, "On your network")
	assert.Contains(t, body, `<a href="/devices/2">nas.example.com</a>`)
	assert.Contains(t, body, "smb")
	assert.Contains(t, body, "2.5 MB")

	assert.Contains(t, body, "Internet")
	assert.Contains(t, body, "Cloudflare")
	assert.Contains(t, body, "2 addresses")
	assert.Contains(t, body, "2.0 kB")
	assert.Contains(t, body, "DB-IP", "the ASN data's licence asks for credit")

	// The page speaks plainly: no protocol jargon reaches it.
	for _, word := range []string{"NetFlow", "IPFIX", "flow", "exporter"} {
		assert.NotContains(t, body, word)
	}

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
		// Two addresses no device holds, in one subnet: one row.
		tcp("192.0.2.10", "198.51.100.7", 80, 300),
		tcp("192.0.2.10", "198.51.100.8", 80, 200),
	)

	rec := get(t, newWebHandler(t, store), "/devices/1/traffic")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Equal(t, 1, strings.Count(body, `<a href="/devices/2">nas.example.com</a>`))
	assert.Contains(t, body, "smb, ssh")
	assert.Contains(t, body, "2 addresses in <span class=\"mono\">198.51.100.0/24</span>")
	assert.Contains(t, body, "not in your inventory")
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
			name: "internet only", query: "scope=internet", push: "/devices/1?scope=internet",
			want: []string{"Cloudflare", "Google"}, avoid: []string{"nas.example.com"},
		},
		{
			name: "local only", query: "scope=local", push: "/devices/1?scope=local",
			want: []string{"nas.example.com"}, avoid: []string{"Cloudflare"},
		},
		{
			name: "one service", query: "service=6%2F445", push: "/devices/1?service=6%2F445",
			want: []string{"nas.example.com"}, avoid: []string{"Cloudflare"},
		},
		{
			name: "search matches an organisation", query: "q=google&traffic=7d", push: "/devices/1?q=google&traffic=7d",
			want: []string{"Google"}, avoid: []string{"Cloudflare", "nas.example.com"},
		},
		{
			name: "nothing matches", query: "q=nothing-here", push: "/devices/1?q=nothing-here",
			want: []string{"Nothing in the last 24 hours matches.", "Clear filters"},
		},
		{
			name: "unknown values are dropped", query: "scope=moon&service=https", push: "/devices/1",
			want: []string{"Cloudflare", "nas.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := get(t, h, "/devices/1/traffic?"+tt.query)
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.push, rec.Header().Get("HX-Push-Url"))

			// Only the tables, not the filter's service choices, which list
			// everything whatever is picked.
			body := rec.Body.String()
			if i := strings.Index(body, "</form>"); i >= 0 {
				body = body[i:]
			}

			for _, w := range tt.want {
				assert.Contains(t, body, w)
			}

			for _, a := range tt.avoid {
				assert.NotContains(t, body, a)
			}
		})
	}
}

func TestDevicePageAppliesTheTrafficFilterFromItsAddress(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store,
		tcp("192.0.2.10", "192.0.2.11", 445, 2_000),
		tcp("192.0.2.10", "1.1.1.1", 443, 1_200),
	)

	rec := get(t, newWebHandler(t, store), "/devices/1?scope=internet")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `<option value="internet" selected>Internet</option>`)
	assert.NotContains(t, body, `<a href="/devices/2">nas.example.com</a>`)
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
