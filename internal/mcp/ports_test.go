package mcp

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPortOverview(t *testing.T) {
	t.Parallel()

	store := seededStore(t)

	// Both devices answer ssh; only the first answers http.
	_, err := store.RecordPorts(t.Context(), "test-sweep", []scanner.PortScan{
		{Addr: netip.MustParseAddr("192.0.2.10"), Open: []uint16{22, 80}, Scanned: []uint16{22, 80}},
		{Addr: netip.MustParseAddr("192.0.2.11"), Open: []uint16{22}, Scanned: []uint16{22, 80}},
	})
	require.NoError(t, err)

	cs := connect(t, getPortOverview(store))

	t.Run("the overview", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[getPortOverviewOutput](t, callTool(t, cs, "get_port_overview", nil))
		require.NotNil(t, out.Overview)
		assert.Equal(t, 3, out.Overview.Open)
		assert.Equal(t, 2, out.Overview.Devices)
		assert.Equal(t, 3, out.Overview.Opened, "every port opened just now")
		assert.Equal(t, 0, out.Overview.Closed)

		require.Len(t, out.Overview.Services, 2)
		assert.Equal(t, uint16(22), out.Overview.Services[0].Port, "the commonest service first")
		assert.Equal(t, "ssh", out.Overview.Services[0].Service)
		assert.Equal(t, 2, out.Overview.Services[0].Devices)
	})

	t.Run("the service list is bounded", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[getPortOverviewOutput](t, callTool(t, cs, "get_port_overview", map[string]any{"service_limit": 1}))
		require.Len(t, out.Overview.Services, 1)
		assert.Equal(t, uint16(22), out.Overview.Services[0].Port)
	})

	t.Run("a limit past the ceiling is refused", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "get_port_overview", map[string]any{"service_limit": serviceLimitMax + 1}))
		assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
	})
}

func TestGetPortOverviewWithNoPorts(t *testing.T) {
	t.Parallel()

	out := decodeAs[getPortOverviewOutput](t, callTool(t, connect(t, getPortOverview(seededStore(t))), "get_port_overview", nil))
	require.NotNil(t, out.Overview)
	assert.Equal(t, 0, out.Overview.Open)
	assert.NotNil(t, out.Overview.Services, "an empty list should still be a list, not null")
}
