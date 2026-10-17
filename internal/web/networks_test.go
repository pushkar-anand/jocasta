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

// The network page is the prefix and its current devices. The same filtered
// list as on the Devices page draws it.
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

	// It has the same filter form as /devices, pointed at this page.
	assert.Contains(t, body, `class="filters"`)
	assert.Contains(t, body, `action="/networks/1"`)
	assert.Contains(t, body, `hx-get="/networks/1/rows"`)

	// The path fixes the network. So the form does not offer to change it.
	assert.NotContains(t, body, "Any network")

	// The way back goes to the device list. This page is a view of it for one
	// network.
	assert.Contains(t, body, `href="/devices"`)

	// The presence legend explains the window of its count.
	assert.Contains(t, body, "answered a sweep in the last 15 minutes")
}

// The filter limits the list, and stays on the page of the network.
func TestNetworkPageFilters(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	body := get(t, h, "/networks/1?q=nas").Body.String()
	assert.Contains(t, body, "nas.local")
	assert.NotContains(t, body, "printer.local")

	// The fragment endpoint answers with only the table, and pushes an address
	// on this page.
	rec := get(t, h, "/networks/1/rows?q=nas")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "<!DOCTYPE html>")
	assert.Equal(t, "/networks/1?q=nas", rec.Header().Get("HX-Push-Url"))
}

// The list on the page of a network has pages, as the Devices page does.
func TestNetworkPagePaginates(t *testing.T) {
	t.Parallel()

	// seededWith sweeps 192.0.2.0/24, which is network 1.
	body := get(t, seededWith(t, 60), "/networks/1").Body.String()

	assert.Contains(t, body, "Page 1 of 2")
	assert.Contains(t, body, `href="/networks/1?page=2"`)
}

// The tag shows only when a router named the segment.
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

	// The friendly name comes first on the page. It is the heading and the
	// browser title.
	assert.Contains(t, body, `<h1 id="page-title" title="Home">Home</h1>`)
	assert.Contains(t, body, "<title>Home &middot; jocasta</title>")

	// The CIDR still shows, one line down in the summary card.
	assert.Contains(t, body, prefix)
}

// The device table on the page of a network drops the Network column. Every
// row is on that network. So the column would repeat the page.
func TestNetworkPageDropsTheRedundantColumn(t *testing.T) {
	t.Parallel()

	body := get(t, seeded(t), "/networks/1").Body.String()
	assert.Contains(t, body, `<table class="on-network">`)

	// The full device list keeps it.
	assert.NotContains(t, get(t, seeded(t), "/devices").Body.String(), `class="on-network"`)
}

// The route allows any segment. So a name or an id that nothing has is a
// missing page.
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
