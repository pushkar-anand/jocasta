package mcp

import (
	"net/netip"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDNS(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	cs := connect(t, listDNS(store, time.Now))

	// Before a resolver is read, the answer tells that no source collects
	// lookups.
	out := decodeAs[listDNSOutput](t, callTool(t, cs, "list_dns", map[string]any{"device_id": 1}))
	assert.False(t, out.Recorded)
	assert.NotNil(t, out.Names, "empty, not absent")

	blocked := plugin.Query{Client: netip.MustParseAddr("192.0.2.10"), Name: "ads.example.net", Blocked: true, At: time.Now()}
	lookup := plugin.Query{Client: netip.MustParseAddr("192.0.2.10"), Name: "example.com", At: time.Now()}

	_, err := store.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS,
		[]plugin.Query{lookup, lookup, blocked}, "cursor")
	require.NoError(t, err)

	out = decodeAs[listDNSOutput](t, callTool(t, cs, "list_dns", map[string]any{"device_id": 1}))
	assert.True(t, out.Recorded)
	require.Len(t, out.Names, 2)
	assert.Equal(t, "example.com", out.Names[0].Name)
	assert.Equal(t, int64(2), out.Names[0].Lookups)
	assert.Equal(t, int64(1), out.Names[1].Blocked)

	out = decodeAs[listDNSOutput](t, callTool(t, cs, "list_dns", map[string]any{"device_id": 1, "limit": 1}))
	assert.Len(t, out.Names, 1)
}
