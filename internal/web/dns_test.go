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
)

// recordLookups records lookups by client of each name, now, the way the DNS
// reader does in production.
func recordLookups(t *testing.T, store *inventory.Store, queries ...plugin.Query) {
	t.Helper()

	_, err := store.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS, queries, "cursor")
	require.NoError(t, err)
}

func lookupOf(client, name string) plugin.Query {
	return plugin.Query{Client: netip.MustParseAddr(client), Name: name, Type: "A", At: time.Now()}
}

func TestDevicePageLeavesLookupsOffWithNoResolver(t *testing.T) {
	t.Parallel()

	rec := get(t, newWebHandler(t, sweptPair(t)), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Looks up")
}

func TestDevicePageSaysWhenThisDeviceLookedNothingUp(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordLookups(t, store, lookupOf("192.0.2.11", "example.com"))

	rec := get(t, newWebHandler(t, store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `<h2 class="section">Looks up</h2>`)
	assert.Contains(t, body, "No lookups from this device in the last 24 hours.")
}

func TestDevicePageShowsWhatTheDeviceLooksUp(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)

	blocked := lookupOf("192.0.2.10", "tracker.ads.example.net")
	blocked.Blocked = true

	recordLookups(t, store,
		lookupOf("192.0.2.10", "www.example.com"),
		lookupOf("192.0.2.10", "www.example.com"),
		lookupOf("192.0.2.10", "api.example.com"),
		blocked,
		lookupOf("192.0.2.10", "printer"),
	)

	rec := get(t, newWebHandler(t, store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "<div><dd>5</dd><dt>Lookups</dt></div>")
	assert.Contains(t, body, "<div><dd>4</dd><dt>Names</dt></div>")
	assert.Contains(t, body, "<div><dd>1</dd><dt>Blocked</dt></div>")
	assert.Contains(t, body, `<span class="strong">example.com</span>
                                <span class="dim">2 names</span>`)
	assert.Contains(t, body, `<span class="strong">example.net</span>`)
	assert.Contains(t, body, `<span class="chip chip--warn">1 blocked</span>`)
	assert.Contains(t, body, `<span class="mono">www.example.com</span>`)
	assert.Contains(t, body, `<span class="strong">printer</span>`, "a bare host name stands for itself")
	assert.NotContains(t, body, "ZgotmplZ")
}

func TestDeviceDNSFragmentSwitchesPeriod(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordLookups(t, store, lookupOf("192.0.2.10", "example.com"))

	h := newWebHandler(t, store)

	rec := get(t, h, "/devices/1/dns?dns=7d")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `id="device-dns"`)
	assert.Contains(t, body, `<option value="7d" selected>Last 7 days</option>`)
	assert.NotContains(t, body, "<html", "a fragment, not a page")

	rec = get(t, h, "/devices/99/dns")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRegistrableDomain(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"www.example.com":   "example.com",
		"a.b.example.co.uk": "example.co.uk",
		"example.com":       "example.com",
		"printer":           "printer",
		"host-a.home.arpa":  "host-a.home.arpa", // home.arpa is itself a public suffix
	} {
		assert.Equal(t, want, registrableDomain(name), name)
	}
}
