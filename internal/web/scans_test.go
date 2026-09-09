package web

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scanned builds a store holding one discovery sweep and one port scan, the two
// scan kinds that otherwise sit in the same log with the same "Found" number.
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

	// The kind is a column, not something to infer from the source.
	assert.Contains(t, body, `<th scope="col">Kind</th>`)
	assert.Contains(t, body, "DISCOVERY")
	assert.Contains(t, body, "PORTS")

	// The count says what it counts.
	assert.Contains(t, body, "hosts")
	assert.Contains(t, body, "ports")
}

func TestScansPageFiltersByKind(t *testing.T) {
	t.Parallel()

	h := scanned(t)

	ports := get(t, h, "/scans?kind=ports").Body.String()
	assert.Contains(t, ports, "PORTS")
	assert.NotContains(t, ports, "DISCOVERY")
	assert.NotContains(t, ports, "hosts", "the discovery row is gone, and with it its units")

	// The filter row marks where you are, and the pager would carry it.
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

// An empty kind-filtered log says which kind found nothing, not that no sweep
// has ever run.
func TestScansPageEmptyKindReadsRight(t *testing.T) {
	t.Parallel()

	body := get(t, seeded(t), "/scans?kind=ports").Body.String()
	assert.Contains(t, body, "No scan of that kind has been recorded yet")
}
