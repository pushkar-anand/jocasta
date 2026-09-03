package web

import (
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/pkg/cursor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevicesPageListsThem(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/devices")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.Contains(t, body, "<!DOCTYPE html>")
	assert.Contains(t, body, "printer.local")
	assert.Contains(t, body, "nas.local")
	assert.Contains(t, body, "192.0.2.10")
	assert.Contains(t, body, macA)

	// Each row links to the device it names.
	assert.Contains(t, body, `href="/devices/1"`)

	// The form is present and fetches only the table.
	assert.Contains(t, body, `hx-get="/devices/rows"`)
	assert.Contains(t, body, `hx-target="#device-rows"`)
}

// The fragment endpoint is what the form fetches, so it has to answer with the
// table alone.
func TestDeviceRowsServesTheTableAlone(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/devices/rows")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.NotContains(t, body, "<!DOCTYPE html>")
	assert.NotContains(t, body, "hx-get=\"/devices/rows\"", "the form is not part of the fragment")
	assert.Contains(t, body, `id="device-rows"`)
	assert.Contains(t, body, "printer.local")
}

// Only the table is fetched, so the address bar has to be told where the reader
// actually is -- otherwise a filtered list cannot be reloaded or shared.
func TestDeviceRowsPushesTheCanonicalURL(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	tests := []struct {
		target string
		want   string
	}{
		{"/devices/rows", "/devices"},
		{"/devices/rows?q=nas", "/devices?q=nas"},
		{"/devices/rows?status=online", "/devices?status=online"},
		{"/devices/rows?ignored=1", "/devices?ignored=1"},
		{"/devices/rows?network=1", "/devices?network=1"},

		// A value the inventory does not recognise is dropped, so the pushed
		// address describes the list that was actually rendered.
		{"/devices/rows?status=onlin", "/devices"},
		{"/devices/rows?sort=vendor", "/devices"},
		{"/devices/rows?network=eth0", "/devices"},

		// Whitespace a reader typed is not part of the search.
		{"/devices/rows?q=%20nas%20", "/devices?q=nas"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			rec := get(t, h, tc.target)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tc.want, rec.Header().Get("HX-Push-Url"))
		})
	}
}

func TestDevicesPageFilters(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	tests := []struct {
		name    string
		target  string
		present []string
		absent  []string
	}{
		{
			name:    "by hostname",
			target:  "/devices?q=nas",
			present: []string{"nas.local"},
			absent:  []string{"printer.local"},
		},
		{
			name:    "by address",
			target:  "/devices?q=192.0.2.10",
			present: []string{"printer.local"},
			absent:  []string{"nas.local"},
		},
		{
			name:    "not seen recently, of which there are none",
			target:  "/devices?status=offline",
			present: []string{"No device matches this filter"},
			absent:  []string{"printer.local", "nas.local"},
		},
		{
			// The seed sweeps one prefix, so its network holds both devices.
			name:    "by network",
			target:  "/devices?network=1",
			present: []string{"printer.local", "nas.local"},
		},
		{
			name:    "by a network nothing is on",
			target:  "/devices?network=999",
			present: []string{"No device matches this filter"},
			absent:  []string{"printer.local", "nas.local"},
		},
		{
			name:    "matching nothing",
			target:  "/devices?q=absent",
			present: []string{"No device matches this filter"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := get(t, h, tc.target).Body.String()

			for _, want := range tc.present {
				assert.Contains(t, body, want)
			}

			for _, unwanted := range tc.absent {
				assert.NotContains(t, body, unwanted)
			}
		})
	}
}

// The form has to come back showing what was applied, or a reloaded page looks
// like it is showing everything.
func TestDevicesFormReflectsTheFilter(t *testing.T) {
	t.Parallel()

	body := get(t, seeded(t), "/devices?q=nas&status=offline&sort=name&ignored=1").Body.String()

	assert.Contains(t, body, `value="nas"`)
	assert.Contains(t, body, `<option value="offline" selected>`)
	assert.Contains(t, body, `<option value="name" selected>`)
	assert.Contains(t, body, `checked`)
}

func TestDevicePage(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	// The id is taken from the list rather than assumed.
	list := get(t, h, "/devices").Body.String()
	id := deviceIDFromBody(t, list)

	rec := get(t, h, "/devices/"+id)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.Contains(t, body, "Addresses")
	assert.Contains(t, body, "History")
	assert.Contains(t, body, "192.0.2.1")

	// An address the device still answers on is marked as held now.
	assert.Contains(t, body, ">now<")

	// Each address names the prefix a sweep placed it on, linking to that
	// network's own page.
	assert.Contains(t, body, `href="/networks/1"`)
	assert.Contains(t, body, prefix)

	// And the page carries the device's own history, which the list does not.
	assert.Contains(t, body, "discovered")
}

// A released address keeps its row, which is what makes "where did this used to
// live" a read rather than a walk back through the log. Only the detail page
// shows it.
func TestDevicePageShowsAReleasedAddress(t *testing.T) {
	t.Parallel()

	store := testStore(t)

	// The address moves because another device claims it: a device answering on
	// a second address does not give up the first, since a sweep reports only
	// what answered.
	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "")})
	require.NoError(t, err)

	_, err = store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macB, "")})
	require.NoError(t, err)

	h := NewHandler(testLogger(), testReader(t), store)

	// The device that lost the address is the first one recorded.
	rec := get(t, h, "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, ">released<")
	assert.NotContains(t, body, ">now<", "it holds nothing any more")
}

func TestDevicePageUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/devices/4040", "/devices/abc", "/devices/0", "/devices/-1"} {
		t.Run(target, func(t *testing.T) {
			rec := get(t, h, target)

			require.Equal(t, http.StatusNotFound, rec.Code)
			assert.Contains(t, rec.Body.String(), "There is nothing at this address")
		})
	}
}

// /devices/rows is a literal and /devices/{id} a wildcard, so the mux has to
// prefer the literal or the fragment endpoint becomes a device lookup.
func TestDeviceRowsIsNotTreatedAsADeviceID(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/devices/rows")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `id="device-rows"`)
}

func TestEventsPage(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/events")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "Change log")
	assert.Contains(t, body, "discovered")
	assert.Contains(t, body, "printer.local")

	// One sweep of two devices does not fill a page, so there is nowhere to
	// walk to.
	assert.NotContains(t, body, "Older")
	assert.NotContains(t, body, "Newest")
}

func TestScansPage(t *testing.T) {
	t.Parallel()

	rec := get(t, seeded(t), "/scans")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "test-sweep")
	assert.Contains(t, body, prefix)
	assert.Contains(t, body, "OK")
}

// A cursor walks one way, so the way back from a later page is to the top of
// the log rather than to the page before it.
func TestLogPagerWalksToTheTop(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	// A page reached by a cursor has somewhere to go back to, even where there
	// is nothing left to show.
	token, err := inventory.Cursor{Value: time.Now(), ID: 1, Order: cursor.Desc}.Encode()
	require.NoError(t, err)

	body := get(t, h, "/events?cursor="+url.QueryEscape(token)).Body.String()
	assert.Contains(t, body, "Newest")
	assert.Contains(t, body, `href="/events"`)

	// A cursor that is not a position starts at the top, which is where a
	// reader who edited the address by hand is best served.
	for _, target := range []string{"/events?cursor=nonsense", "/events?cursor="} {
		t.Run(target, func(t *testing.T) {
			body := get(t, h, target).Body.String()

			assert.NotContains(t, body, "Newest")
			assert.Contains(t, body, "discovered")
		})
	}
}

func TestPagerLinks(t *testing.T) {
	t.Parallel()

	// The top of the log has no way back and, until it fills, nowhere onward.
	top := logData{Path: "/events"}
	assert.True(t, top.AtTop())
	assert.False(t, top.HasOlder())

	// A page reached by a cursor is not the top, and one that ended with a
	// cursor has a page behind it.
	page := logData{Path: "/events", Cursor: "here", Next: "there+/="}
	assert.False(t, page.AtTop())
	assert.True(t, page.HasOlder())

	// The token is escaped, since it is base64 and carries characters a query
	// string gives its own meaning.
	assert.Equal(t, "/events?cursor=there%2B%2F%3D", page.Older())
}

func TestCanonicalURL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "/devices", devicesData{}.canonical())

	// Every value that was applied is in the address, so it can be shared.
	got := devicesData{Query: "nas", Group: "rack", Network: "3", Status: "online", Sort: "name", IncludeIgnored: true}.canonical()

	parsed, err := url.Parse(got)
	require.NoError(t, err)
	assert.Equal(t, "/devices", parsed.Path)

	assert.Equal(t, url.Values{
		"q":       {"nas"},
		"group":   {"rack"},
		"network": {"3"},
		"status":  {"online"},
		"sort":    {"name"},
		"ignored": {"1"},
	}, parsed.Query())
}

func TestDeviceFormDropsUnrecognisedValues(t *testing.T) {
	t.Parallel()

	form := deviceForm(url.Values{
		"q":       {"  nas  "},
		"status":  {"onlin"},
		"sort":    {"vendor"},
		"ignored": {"yes"},
	})

	assert.Equal(t, "nas", form.Query, "surrounding whitespace is not part of a search")
	assert.Empty(t, form.Status)
	assert.Empty(t, form.Sort)
	assert.False(t, form.IncludeIgnored, "only the value the form submits turns it on")

	form = deviceForm(url.Values{"status": {"offline"}, "sort": {"address"}, "ignored": {"1"}})
	assert.Equal(t, "offline", form.Status)
	assert.Equal(t, "address", form.Sort)
	assert.True(t, form.IncludeIgnored)

	// The network select offers ids; a name is not one.
	assert.Empty(t, deviceForm(url.Values{"network": {"eth0"}}).Network)
	assert.Empty(t, deviceForm(url.Values{"network": {"0"}}).Network)
	assert.Equal(t, "4", deviceForm(url.Values{"network": {"4"}}).Network)
}

// deviceIDFromBody pulls the first device id out of a rendered list.
func deviceIDFromBody(t *testing.T, body string) string {
	t.Helper()

	const marker = `href="/devices/`

	i := strings.Index(body, marker)
	require.GreaterOrEqual(t, i, 0, "the list should link to a device")

	rest := body[i+len(marker):]

	j := strings.IndexByte(rest, '"')
	require.Greater(t, j, 0)

	id := rest[:j]

	_, err := strconv.Atoi(id)
	require.NoError(t, err, "the link should end in a device id, got %q", id)

	return id
}

// A device the sweep and a router both know shows what each of them says, not
// only the name that won.
func TestDevicePageShowsWhatEachSourceClaims(t *testing.T) {
	t.Parallel()

	store := testStore(t)

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "printer.example.com")})
	require.NoError(t, err)

	h, err := hosts.BuildHost(t.Context(), hosts.HostInput{IP: "192.0.2.10", MAC: macA, Hostname: "lab-printer"})
	require.NoError(t, err)

	_, err = store.RecordFacts(t.Context(), "routeros:gateway", dbtype.SourceRouter, []plugin.Fact{{
		Host:           h,
		Present:        true,
		HostnameSource: dbtype.HostnameFromDHCPStatic,
		Detail:         map[string]string{"interface": "vlan10", "dhcp_comment": "bench unit"},
	}})
	require.NoError(t, err)

	rec := get(t, NewHandler(testLogger(), testReader(t), store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()

	assert.Contains(t, body, "Sources")

	// Both sources are named, and the name each of them offered is shown --
	// including the one the election did not pick.
	assert.Contains(t, body, "test-sweep")
	assert.Contains(t, body, "routeros:gateway")
	assert.Contains(t, body, "printer.example.com")
	assert.Contains(t, body, "lab-printer")

	// The standing is worded rather than shown as the stored constant.
	assert.Contains(t, body, "reverse DNS")
	assert.Contains(t, body, "static lease")
	assert.NotContains(t, body, "DHCP_STATIC")

	// What only the router knows is rendered from its stored JSON.
	assert.Contains(t, body, "interface")
	assert.Contains(t, body, "vlan10")
	assert.Contains(t, body, "bench unit")

	assert.NotContains(t, body, "ZgotmplZ")
}

// A device nothing has claimed yet still renders: the section is left out
// rather than drawn empty.
func TestDevicePageWithoutClaimsOmitsTheSection(t *testing.T) {
	t.Parallel()

	store, conn := testStoreWithConn(t)

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "")})
	require.NoError(t, err)

	// The state an install upgraded from an earlier schema is in: devices that
	// no source has filed a claim about yet.
	_, err = conn.ExecContext(t.Context(), `DELETE FROM device_sources`)
	require.NoError(t, err)

	rec := get(t, NewHandler(testLogger(), testReader(t), store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.NotContains(t, rec.Body.String(), `<p class="eyebrow">Sources</p>`)
}
