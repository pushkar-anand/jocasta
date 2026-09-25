package mcp

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListScans(t *testing.T) {
	t.Parallel()

	// One sweep from seededStore, then a port scan.
	store := seededStore(t)

	_, err := store.RecordPorts(t.Context(), "test-sweep", []scanner.PortScan{
		{Addr: netip.MustParseAddr("192.0.2.10"), Open: []uint16{22}, Scanned: []uint16{22}},
	})
	require.NoError(t, err)

	cs := connect(t, listScans(store))

	t.Run("every scan, newest first", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[listScansOutput](t, callTool(t, cs, "list_scans", nil))
		require.Equal(t, 2, out.Count)
		assert.True(t, out.NextCursor.IsZero())

		assert.Equal(t, dbtype.ScanPorts, out.Scans[0].Kind)
		assert.Equal(t, dbtype.ScanDiscovery, out.Scans[1].Kind)
		assert.Equal(t, "test-sweep", out.Scans[1].Source)
		assert.Equal(t, prefix, out.Scans[1].Network)
		assert.Equal(t, dbtype.StatusOK, out.Scans[1].Status)
		assert.Equal(t, 2, out.Scans[1].Found)
	})

	t.Run("kind narrows the history", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[listScansOutput](t, callTool(t, cs, "list_scans", map[string]any{"kind": string(dbtype.ScanDiscovery)}))
		require.Equal(t, 1, out.Count)
		assert.Equal(t, dbtype.ScanDiscovery, out.Scans[0].Kind)
	})

	t.Run("paging walks the whole history", func(t *testing.T) {
		t.Parallel()

		first := decodeAs[listScansOutput](t, callTool(t, cs, "list_scans", map[string]any{"limit": 1}))
		require.Equal(t, 1, first.Count)
		require.False(t, first.NextCursor.IsZero())

		token, err := first.NextCursor.Encode()
		require.NoError(t, err)

		second := decodeAs[listScansOutput](t, callTool(t, cs, "list_scans", map[string]any{"limit": 1, "cursor": token}))
		require.Equal(t, 1, second.Count)
		assert.True(t, second.NextCursor.IsZero())
		assert.Equal(t, []dbtype.ScanKind{dbtype.ScanPorts, dbtype.ScanDiscovery},
			[]dbtype.ScanKind{first.Scans[0].Kind, second.Scans[0].Kind})
	})

	for name, tc := range map[string]struct {
		args   map[string]any
		detail string
	}{
		"an unknown kind is refused":   {map[string]any{"kind": "PING"}, "DISCOVERY"},
		"a page past the ceiling":      {map[string]any{"limit": inventory.MaxPageSize + 1}, ""},
		"a cursor it never issued":     {map[string]any{"cursor": "not-a-cursor"}, "list_scans"},
		"a page of nothing is refused": {map[string]any{"limit": 0}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := problemOf(t, callTool(t, cs, "list_scans", tc.args))
			assert.Equal(t, float64(http.StatusBadRequest), doc["status"])

			if tc.detail != "" {
				assert.Contains(t, doc["detail"], tc.detail)
			}
		})
	}
}

// A store with no scans answers an empty page.
func TestListScansWithNone(t *testing.T) {
	t.Parallel()

	out := decodeAs[listScansOutput](t, callTool(t, connect(t, listScans(testStore(t))), "list_scans", nil))
	assert.Equal(t, 0, out.Count)
	assert.NotNil(t, out.Scans)
	assert.Equal(t, []*inventory.Scan{}, out.Scans)
}
