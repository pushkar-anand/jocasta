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

// The network page is the prefix and the devices currently on it, drawn by the
// same filtered list the Devices page uses.
func TestNetworkPage(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/networks/1")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.Contains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, prefix)

	assert.Contains(t, body, "printer.local")
	assert.Contains(t, body, "nas.local")
	assert.Contains(t, body, `id="device-rows"`)

	// It carries the same filter form as /devices, pointed at this page.
	assert.Contains(t, body, `class="filters"`)
	assert.Contains(t, body, `action="/networks/1"`)
	assert.Contains(t, body, `hx-get="/networks/1/rows"`)

	// The network is fixed by the path, so the form does not offer to change it.
	assert.NotContains(t, body, "Any network")

	// The way back leads to the device list, which the page is a scoped view of.
	assert.Contains(t, body, `href="/devices"`)
}

// The filter narrows the list without leaving the network's page.
func TestNetworkPageFilters(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	body := get(t, h, "/networks/1?q=nas").Body.String()
	assert.Contains(t, body, "nas.local")
	assert.NotContains(t, body, "printer.local")

	// The fragment endpoint answers with the table alone and pushes an address
	// on this page, not on /devices.
	rec := get(t, h, "/networks/1/rows?q=nas")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "<!DOCTYPE html>")
	assert.Equal(t, "/networks/1?q=nas", rec.Header().Get("HX-Push-Url"))
}

// The list on a network's page is paged like the Devices page, not rendered
// whole.
func TestNetworkPagePaginates(t *testing.T) {
	t.Parallel()

	// seededWith sweeps 192.0.2.0/24, which is network 1.
	body := get(t, seededWith(t, 60), "/networks/1").Body.String()

	assert.Contains(t, body, "Page 1 of 2")
	assert.Contains(t, body, `href="/networks/1?page=2"`)
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

	body := get(t, newWebHandler(t, store), "/networks/1").Body.String()

	assert.Contains(t, body, "VLAN 10")
	assert.Contains(t, body, "Home")
}

// The route admits any segment, so a name or an id nothing has is a page that
// is not there rather than a fault.
func TestNetworkPageUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/networks/999", "/networks/abc", "/networks/0", "/networks/999/rows"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, http.StatusNotFound, get(t, h, target).Code)
		})
	}
}
