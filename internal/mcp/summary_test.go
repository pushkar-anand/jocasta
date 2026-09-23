package mcp

import (
	"testing"

	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStats(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	cs := connect(t, getStats(store))

	// An ignored device is still counted, and counted again as ignored.
	devices, err := store.ListDevices(t.Context(), inventory.DeviceFilter{Query: "nas"})
	require.NoError(t, err)
	require.Len(t, devices, 1)

	_, err = store.UpdateCuration(t.Context(), devices[0].ID, inventory.Curation{Ignored: true})
	require.NoError(t, err)

	out := decodeAs[getStatsOutput](t, callTool(t, cs, "get_stats", nil))
	require.NotNil(t, out.Stats)
	assert.Equal(t, 2, out.Total)
	assert.Equal(t, 2, out.Online)
	assert.Equal(t, 0, out.Offline)
	assert.Equal(t, 1, out.Ignored)
	assert.Equal(t, 2, out.Discovered, "both devices were first seen just now")
}

func TestListGroups(t *testing.T) {
	t.Parallel()

	t.Run("none assigned", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[listGroupsOutput](t, callTool(t, connect(t, listGroups(seededStore(t))), "list_groups", nil))
		assert.NotNil(t, out.Groups, "an empty list should still be a list, not null")
		assert.Empty(t, out.Groups)
	})

	t.Run("the groups the owner used", func(t *testing.T) {
		t.Parallel()

		store := seededStore(t)

		devices, err := store.ListDevices(t.Context(), inventory.DeviceFilter{})
		require.NoError(t, err)
		require.Len(t, devices, 2)

		for i, group := range []string{"office", "storage"} {
			_, err := store.UpdateCuration(t.Context(), devices[i].ID, inventory.Curation{Group: group})
			require.NoError(t, err)
		}

		out := decodeAs[listGroupsOutput](t, callTool(t, connect(t, listGroups(store)), "list_groups", nil))
		assert.Equal(t, []string{"office", "storage"}, out.Groups)
	})
}
