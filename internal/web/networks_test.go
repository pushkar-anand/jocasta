package web

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The network page is the way in from an overview card: the prefix, and the
// devices currently on it.
func TestNetworkPage(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/networks/1")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.Contains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, prefix)

	// The devices on the prefix, rendered by the same table the Devices page uses.
	assert.Contains(t, body, "printer.local")
	assert.Contains(t, body, "nas.local")
	assert.Contains(t, body, `id="device-rows"`)

	// The way back out.
	assert.Contains(t, body, `href="/"`)
}

// A router that named the segment is the only place its tag is shown.
func TestNetworkPageShowsTheTag(t *testing.T) {
	t.Parallel()

	store := testStore(t)

	require.NoError(t, store.RecordNetworks(t.Context(), []plugin.Network{{
		Prefix: netip.MustParsePrefix(prefix),
		Name:   "Home",
		VLAN:   10,
	}}))

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "printer.local")})
	require.NoError(t, err)

	body := get(t, NewHandler(testLogger(), testReader(t), store), "/networks/1").Body.String()

	assert.Contains(t, body, "VLAN 10")
	assert.Contains(t, body, "Home")
}

// The route admits any segment, so a name or an id nothing has is a page that
// is not there rather than a fault.
func TestNetworkPageUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/networks/999", "/networks/abc", "/networks/0"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, http.StatusNotFound, get(t, h, target).Code)
		})
	}
}
