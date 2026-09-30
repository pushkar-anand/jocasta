package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// recordRouter records a router read that learned the laptop on ether4 in
// VLAN 10, on a gigabit link that came up at 100 Mbps, and the nas behind a
// switch that announced itself on sfp1.
func recordRouter(t *testing.T, store *inventory.Store) {
	t.Helper()

	require.NoError(t, store.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, plugin.Topology{
		Identity: "router",
		Gateway:  true,
		Own:      []string{"00:00:5e:00:53:a0"},
		Ports: []plugin.TopologyPort{
			{
				Name: "ether4", Kind: plugin.PortWired, PVID: 10, Untagged: []int{10}, Running: true,
				Rate: 100_000_000, Capable: 1_000_000_000, FullDuplex: true,
			},
			{
				Name: "sfp1", Kind: plugin.PortWired, Tagged: []int{10}, Running: true,
				Rate: 10_000_000_000, FullDuplex: true,
			},
		},
		Seen: []plugin.Sighting{
			{Port: "ether4", MAC: macA, VLAN: 10},
			{Port: "sfp1", MAC: macB, VLAN: 10},
			{Port: "sfp1", MAC: "00:00:5e:00:53:b0", VLAN: 10},
		},
		Neighbours: []plugin.Neighbour{{Port: "sfp1", MAC: "00:00:5e:00:53:b0", Identity: "switch-a", Board: "CRS326"}},
		ReadAt:     time.Now(),
	}))
}

func TestTopologySaysWhenNothingIsRead(t *testing.T) {
	t.Parallel()

	rec := get(t, newWebHandler(t, sweptPair(t)), "/topology")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "Nothing is read yet.")
	assert.Contains(t, body, "setup.md#show-what-is-plugged-in-where")
	assert.NotContains(t, body, `hx-get="/topology/live"`, "nothing to refresh")
}

// The page draws the router, the switch that announced itself, and each
// device on its port, and polls for itself.
func TestTopologyDrawsTheTree(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordRouter(t, store)

	rec := get(t, newWebHandler(t, store), "/topology")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `hx-get="/topology/live" hx-trigger="every 60s"`)
	assert.Contains(t, body, `href="/topology" aria-current="page"`)
	assert.Contains(t, body, ">router</text>")
	assert.Contains(t, body, ">switch-a</text>")
	assert.Contains(t, body, ">CRS326</text>")
	assert.Contains(t, body, ">Wired · VLAN\u00a010</text>", "the laptop's group names its VLAN")
	assert.Contains(t, body, `text-anchor="end">ether4</text>`, "the laptop's chip names its port")
	assert.Contains(t, body, "sfp1 · trunk", "the switch's line names the router's port")
	assert.Contains(t, body, `data-name="laptop.example.com"`, "the search finds devices by name")
	assert.Contains(t, body, `href="/devices/1"`)
	assert.Contains(t, body, "home · VLAN\u00a010", "the legend names the VLAN by its network")
	assert.Contains(t, body, `data-path="d1"`, "the laptop's line lights when it is selected")
	assert.NotContains(t, body, "&quot;", "path keys are plain")
	assert.NotContains(t, body, "ZgotmplZ")
}

func TestTopologyLiveIsTheTreeAlone(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordRouter(t, store)

	rec := get(t, newWebHandler(t, store), "/topology/live")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `class="card netmap topo"`)
	assert.NotContains(t, body, `id="netmap-search"`, "the search outlives each refresh")
	assert.NotContains(t, body, "<html")
}

// A device's page shows the path down to it, each hop linked, and links to
// the device on the topology.
func TestDevicePageShowsWhereTheDeviceIsConnected(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordRouter(t, store)

	rec := get(t, newWebHandler(t, store), "/devices/2")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `<h2 class="section">Connected</h2>`)
	assert.Contains(t, body, ">router</span>", "the router has no inventory device here, so it is not a link")
	assert.Contains(t, body, `<span class="chip">sfp1 · trunk</span>`)
	assert.Contains(t, body, ">switch-a</span>")
	assert.Contains(t, body, "<span class=\"chip chip--brand\">VLAN\u00a010</span>")
	assert.Contains(t, body, `href="/topology?focus=d2"`)
	assert.NotContains(t, body, "last seen here")
	assert.NotContains(t, body, "Gbps", "the nas is behind the switch, whose link it is")
}

// A device alone on a port shows the port's rate, and a link that came up
// below what both ends can run says so.
func TestDevicePageShowsTheLinkSpeed(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	recordRouter(t, store)

	rec := get(t, newWebHandler(t, store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "<strong>laptop.example.com</strong><span class=\"chip\">100\u00a0Mbps</span>")
	assert.Contains(t, body, "Both ends can run at 1\u00a0Gbps, and this link runs slower.")
}

func TestDevicePageSaysWhenNothingHasSeenTheDevice(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	require.NoError(t, store.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, plugin.Topology{
		Identity: "router", Gateway: true, ReadAt: time.Now(),
	}))

	rec := get(t, newWebHandler(t, store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No router, switch or access point has seen this device on a port.")
}

func TestDevicePageLeavesConnectedOutBeforeAnyRead(t *testing.T) {
	t.Parallel()

	rec := get(t, newWebHandler(t, sweptPair(t)), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), `<h2 class="section">Connected</h2>`)
}

// A device that is itself the router hangs straight from the internet.
func TestDevicePageShowsTheRouterBelowTheInternet(t *testing.T) {
	t.Parallel()

	store := sweptPair(t)
	require.NoError(t, store.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, plugin.Topology{
		Identity: "router", Gateway: true, Own: []string{macA}, ReadAt: time.Now(),
	}))

	rec := get(t, newWebHandler(t, store), "/devices/1")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, `<li class="path__hop">Internet</li>`)
	assert.Contains(t, body, "<strong>laptop.example.com</strong>")
	assert.Contains(t, body, `href="/topology?focus=d1"`)
	_, connected, _ := strings.Cut(body, `<h2 class="section">Connected</h2>`)
	connected, _, _ = strings.Cut(connected, "</section>")
	assert.NotContains(t, connected, `class="chip`, "nothing sits between the internet and the router")
}
