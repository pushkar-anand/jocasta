package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// recordRouter records a router read that learned the laptop on ether4 in
// VLAN 10, and the nas behind a switch that announced itself on sfp1.
func recordRouter(t *testing.T, store *inventory.Store) {
	t.Helper()

	require.NoError(t, store.RecordTopology(t.Context(), "routeros:gateway", dbtype.SourceRouter, plugin.Topology{
		Identity: "router",
		Gateway:  true,
		Own:      []string{"00:00:5e:00:53:a0"},
		Ports: []plugin.TopologyPort{
			{Name: "ether4", Kind: plugin.PortWired, PVID: 10, Untagged: []int{10}, Running: true},
			{Name: "sfp1", Kind: plugin.PortWired, Tagged: []int{10}, Running: true},
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
	assert.Contains(t, body, "ether4 · VLAN 10", "the laptop's group names its port and VLAN")
	assert.Contains(t, body, "sfp1 · trunk", "the switch's line names the router's port")
	assert.Contains(t, body, `data-name="laptop.example.com"`, "the search finds devices by name")
	assert.Contains(t, body, `href="/devices/1"`)
	assert.Contains(t, body, "home · VLAN 10", "the legend names the VLAN by its network")
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
