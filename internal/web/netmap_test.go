package web

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/inventory"
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
