package web

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scanned builds a store with one discovery sweep and one port scan. These two
// scan kinds are in the same log, with the same "Found" number.
func scanned(t *testing.T) http.Handler {
	t.Helper()

	store := testStore(t)

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "printer.example.com")})
	require.NoError(t, err)

	_, err = store.RecordPorts(t.Context(), "test-sweep", []scanner.PortScan{
		{Addr: netip.MustParseAddr("192.0.2.10"), Open: []uint16{22, 443}, Scanned: []uint16{22, 80, 443}},
	})
	require.NoError(t, err)

	return newWebHandler(t, store)
}

func TestScansPageNamesTheKindAndUnits(t *testing.T) {
	t.Parallel()

	body := get(t, scanned(t), "/scans").Body.String()

	// The kind has its own column.
	assert.Contains(t, body, `<th scope="col">Kind</th>`)
	assert.Contains(t, body, "DISCOVERY")
	assert.Contains(t, body, "PORTS")

	// The count tells what it counts.
	assert.Regexp(t, `\d+ devices?\b`, body)
	assert.Regexp(t, `\d+ open ports?\b`, body)
}

func TestScansPageFiltersByKind(t *testing.T) {
	t.Parallel()

	h := scanned(t)

	ports := get(t, h, "/scans?kind=ports").Body.String()
	assert.Contains(t, ports, "PORTS")
	assert.NotContains(t, ports, "DISCOVERY")
	assert.NotRegexp(t, `\d+ devices?\b`, ports, "the discovery row is gone, and with it its units")

	// The filter row marks the current place. The pager would keep it.
	assert.Contains(t, ports, `href="/scans?kind=ports"`)
	assert.Contains(t, ports, `aria-current="page"`)

	discovery := get(t, h, "/scans?kind=discovery").Body.String()
	assert.Contains(t, discovery, "DISCOVERY")
	assert.NotContains(t, discovery, "PORTS")
}

func TestScansPageRejectsAnUnknownKind(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, http.StatusOK, get(t, scanned(t), "/scans?kind=bogus").Code)
}

// An empty log with a kind filter names the kind that found nothing.
func TestScansPageEmptyKindReadsRight(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	assert.Contains(t, get(t, h, "/scans?kind=ports").Body.String(), "No port scans recorded yet.")

	// A log with nothing in it tells how scans come.
	assert.Contains(t, get(t, empty(t), "/scans").Body.String(), "No scans recorded yet.")
}

// A port scan of only the new devices has a tag. The full scan does not.
func TestScansPageTagsANewDeviceScan(t *testing.T) {
	t.Parallel()

	h := scanned(t)

	assert.NotContains(t, get(t, h, "/scans").Body.String(), "new devices", "a full port scan carries no tag")

	store := testStore(t)

	_, err := store.RecordNewDevicePorts(t.Context(), "test-sweep", []scanner.PortScan{
		{Addr: netip.MustParseAddr("192.0.2.10"), Open: []uint16{22}, Scanned: []uint16{22}},
	})
	require.NoError(t, err)

	assert.Contains(t, get(t, newWebHandler(t, store), "/scans").Body.String(), `<span class="chip">new devices</span>`)
}
