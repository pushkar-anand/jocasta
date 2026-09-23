package mcp

import (
	"log/slog"
	"net/http"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListNetworks(t *testing.T) {
	t.Parallel()

	cs := connect(t, listNetworks(seededStore(t)))

	out := decodeAs[listNetworksOutput](t, callTool(t, cs, "list_networks", nil))
	require.Equal(t, 1, out.Count)
	require.Len(t, out.Networks, 1)

	n := out.Networks[0]
	assert.Equal(t, prefix, n.CIDR)
	assert.Equal(t, 2, n.Total)
	assert.Equal(t, 2, n.Online)
	assert.Equal(t, 0, n.Offline)
}

func TestListNetworksWithNoneRecorded(t *testing.T) {
	t.Parallel()

	cs := connect(t, listNetworks(testStore(t)))

	out := decodeAs[listNetworksOutput](t, callTool(t, cs, "list_networks", nil))
	assert.Equal(t, 0, out.Count)
	assert.NotNil(t, out.Networks, "an empty list should still be a list, not null")
}

func TestGetNetwork(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
		listNetworks(store)(s, log)
		getNetwork(store)(s, log)
	})

	listed := decodeAs[listNetworksOutput](t, callTool(t, cs, "list_networks", nil))
	require.Len(t, listed.Networks, 1)

	id := listed.Networks[0].ID

	t.Run("the network", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[getNetworkOutput](t, callTool(t, cs, "get_network", map[string]any{"id": id}))
		require.NotNil(t, out.Network)
		assert.Equal(t, id, out.Network.ID)
		assert.Equal(t, prefix, out.Network.CIDR)
	})

	t.Run("a network that does not exist is a 404", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "get_network", map[string]any{"id": id + 1}))
		assert.Equal(t, float64(http.StatusNotFound), doc["status"])
		assert.Equal(t, "get_network", doc["instance"])
	})

	t.Run("an id the inventory never issues is refused", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "get_network", map[string]any{"id": 0}))
		assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
	})
}
