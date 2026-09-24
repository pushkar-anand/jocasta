package web

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/pkg/geo"
)

// recentEdges stands in for the recorder, reporting the same edges whatever
// it is asked.
type recentEdges []inventory.RecentEdge

func (r recentEdges) Recent(time.Time) []inventory.RecentEdge { return r }

func TestMapSaysWhenNoTrafficIsRecorded(t *testing.T) {
	t.Parallel()

	rec := get(t, newWebHandler(t, sweptPair(t)), "/map")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "No traffic recorded yet.")
	assert.NotContains(t, body, `hx-get="/map/live"`, "nothing to refresh")
}

func TestMapSaysWhenTheLastHourWasQuiet(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	old := tcp("192.0.2.10", "198.51.100.7", 443, 100)
	old.End = time.Now().Add(-3 * time.Hour)
	recordTraffic(t, store, old)

	rec := get(t, newWebHandler(t, store), "/map")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No traffic in the last hour.")
}

// The page draws each device on its network and links to it, and polls for
// itself; the fragment it polls is the map alone.
func TestMapDrawsDevicesAndRefreshesItself(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store,
		tcp("192.0.2.10", "192.0.2.11", 22, 1_000),
		tcp("192.0.2.10", "1.1.1.1", 443, 5_000),
	)

	h := newWebHandlerWithAuth(t, store, testAuth(t), WithRecentTraffic(recentEdges{{
		A: netip.MustParseAddr("1.1.1.1"), B: netip.MustParseAddr("192.0.2.10"), Bytes: 5_000,
	}}))

	rec := get(t, h, "/map")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `hx-get="/map/live" hx-trigger="every 60s"`)
	assert.Contains(t, body, "home · VLAN\u00a010")
	assert.Contains(t, body, `href="/devices/1"`)
	assert.Contains(t, body, "laptop.example.com")
	assert.Contains(t, body, `id="netmap-search"`)
	assert.Contains(t, body, `data-name="laptop.example.com"`, "the search finds devices by name")
	assert.Contains(t, body, `class="netmap__link netmap__link--active" data-a="d1"`, "a line joins the device to what it talked to")
	assert.Contains(t, body, `data-b="d2"`, "and to the device on the network it reached")
	assert.Contains(t, body, `>https</text>`, "the line names its service")
	assert.Contains(t, body, `>ssh</text>`)
	assert.Contains(t, body, "netmap__line--active", "the internet line was active lately")
	assert.Contains(t, body, "Active now", "the legend shows what a moving line is")
	assert.NotContains(t, body, "ZgotmplZ")

	live := get(t, h, "/map/live")
	require.Equal(t, http.StatusOK, live.Code)

	frag := live.Body.String()
	assert.Contains(t, frag, "netmap__svg")
	assert.NotContains(t, frag, `id="map-live"`, "the wrapper stays on the page")
	assert.NotContains(t, frag, `id="netmap-search"`, "so does the search, keeping what was typed")
	assert.NotContains(t, frag, "<html")
}

// Without the recorder in this process nothing can be told to be active, and
// the page does not claim otherwise.
func TestMapWithoutTheRecorderMarksNothingActive(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store, tcp("192.0.2.10", "192.0.2.11", 22, 1_000))

	rec := get(t, newWebHandler(t, store), "/map")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Equal(t, 0, strings.Count(body, "netmap__line--active"))
	assert.NotContains(t, body, "Active now")
}

// The world view draws every country once with the page and polls only for
// how to shade them; a country the network reached is shaded, with a card of
// who talked to it.
func TestWorldMapShadesTheCountriesReached(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store, tcp("192.0.2.10", "1.1.1.1", 443, 5_000))

	code, ok := geo.Lookup(netip.MustParseAddr("1.1.1.1"))
	require.True(t, ok)

	country, ok := geo.CountryOf(code)
	require.True(t, ok)

	h := newWebHandler(t, store)

	rec := get(t, h, "/map?view=world")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `hx-get="/map/live?view=world"`)
	assert.Contains(t, body, `data-code="IN"`, "every country is drawn")
	assert.Contains(t, body, `<li data-code="`+code+`" data-shade="5"`)
	assert.Contains(t, body, `data-key="c`+code+`" hidden`, "a card for the country")
	assert.Contains(t, body, country.Name)
	assert.Contains(t, body, `href="/devices/1"`)
	assert.NotContains(t, body, "ZgotmplZ")

	live := get(t, h, "/map/live?view=world")
	require.Equal(t, http.StatusOK, live.Code)

	frag := live.Body.String()
	assert.Contains(t, frag, `id="world-data"`)
	assert.NotContains(t, frag, "worldmap__country", "the outline is not sent again")
}

func TestShadeIsALogScale(t *testing.T) {
	t.Parallel()

	assert.Equal(t, worldShades, shade(1_000_000, 1_000_000))
	assert.Equal(t, 1, shade(1, 1_000_000))
	assert.Equal(t, 3, shade(1_000, 1_000_000), "a thousandth of the busiest is halfway")
}

// Home is the country configured, or else the one the router's outside address
// is registered in; a network behind the ISP's NAT, with none configured, has
// none.
func TestWorldMapFindsHome(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	now := time.Now()

	home, err := (&Handler{store: store}).homeOf(t.Context(), now)
	require.NoError(t, err)
	assert.Nil(t, home, "nothing names it")

	home, err = (&Handler{store: store, homeCountry: "AU"}).homeOf(t.Context(), now)
	require.NoError(t, err)
	require.NotNil(t, home)
	assert.Equal(t, "AU", home.Code)

	// The router names its outside address as it translates a connection out.
	out := tcp("192.0.2.10", "198.51.100.7", 443, 100)
	out.NATSrc = netip.MustParseAddr("1.1.1.1")
	out.Exporter = netip.MustParseAddr("192.0.2.1")
	recordTraffic(t, store, out)

	want, ok := geo.Lookup(netip.MustParseAddr("1.1.1.1"))
	require.True(t, ok)

	home, err = (&Handler{store: store}).homeOf(t.Context(), now)
	require.NoError(t, err)
	require.NotNil(t, home)
	assert.Equal(t, want, home.Code)

	home, err = (&Handler{store: store, homeCountry: "AU"}).homeOf(t.Context(), now)
	require.NoError(t, err)
	assert.Equal(t, "AU", home.Code, "a country named in the config wins")
}

// With a home, each country the network reached gets a line from it, which
// selecting the country lights; home itself gets none.
func TestWorldMapDrawsLinesFromHome(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordTraffic(t, store, tcp("192.0.2.10", "1.1.1.1", 443, 5_000))

	code, ok := geo.Lookup(netip.MustParseAddr("1.1.1.1"))
	require.True(t, ok)

	home := "IN"
	if code == home {
		home = "NZ"
	}

	body := get(t, newWebHandlerWithAuth(t, store, testAuth(t), WithHomeCountry(home)), "/map?view=world").Body.String()
	assert.Contains(t, body, `data-home="`+home+`"`)
	assert.Contains(t, body, `data-arc="M `)
	assert.Contains(t, body, `data-a="c`+home+`"`, "the line's other end is home, for selection")

	country, _ := geo.CountryOf(home)
	assert.Contains(t, body, country.Name, "the legend names home")

	// Traffic that stayed in the home country draws no line.
	same := get(t, newWebHandlerWithAuth(t, store, testAuth(t), WithHomeCountry(code)), "/map?view=world").Body.String()
	assert.NotContains(t, same, "data-arc=")

	// Without a home there is nothing to draw from.
	none := get(t, newWebHandler(t, store), "/map?view=world").Body.String()
	assert.NotContains(t, none, "data-arc=")
	assert.NotContains(t, none, "data-home=")
}

// An arc bows up the map whichever way it runs.
func TestArcBowsUpwards(t *testing.T) {
	t.Parallel()

	for _, ends := range [][4]float64{{100, 200, 500, 200}, {500, 200, 100, 200}, {100, 300, 400, 100}} {
		var x1, y1, cx, cy, x2, y2 float64

		_, err := fmt.Sscanf(arc(ends[0], ends[1], ends[2], ends[3]), "M %f %f Q %f %f %f %f", &x1, &y1, &cx, &cy, &x2, &y2)
		require.NoError(t, err)
		assert.Less(t, cy, (y1+y2)/2, "%v", ends)
	}
}
