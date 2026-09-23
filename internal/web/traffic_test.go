package web

import (
	"net/http"
	"net/netip"
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
	assert.Contains(t, body, `aria-current="page">Last 7 days</a>`)
	assert.NotContains(t, body, "<html", "a fragment, not a page")
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
