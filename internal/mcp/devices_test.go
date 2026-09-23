package mcp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connect joins a client to a server holding only the given tool over an
// in-memory transport, so a tool is tested without HTTP or a token.
func connect(t *testing.T, register func(*mcpsdk.Server, *slog.Logger)) *mcpsdk.ClientSession {
	t.Helper()

	return connectLogging(t, testLogger(), register)
}

// connectLogging is connect with the server logging to log, for a test that
// asserts on what a failure logged.
func connectLogging(t *testing.T, log *slog.Logger, register func(*mcpsdk.Server, *slog.Logger)) *mcpsdk.ClientSession {
	t.Helper()

	s := newServer()
	register(s, log)

	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	ss, err := s.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)

	t.Cleanup(func() { _ = ss.Close() })

	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client"}, nil).
		Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)

	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

// decodeDevices reads list_devices' structured result back.
func decodeDevices(t *testing.T, res *mcpsdk.CallToolResult) listDevicesOutput {
	t.Helper()

	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)

	var out listDevicesOutput
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

func callListDevices(t *testing.T, cs *mcpsdk.ClientSession, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "list_devices", Arguments: args})
	require.NoError(t, err)

	return res
}

func TestListDevices(t *testing.T) {
	t.Parallel()

	cs := connect(t, listDevices(seededStore(t)))

	t.Run("every device", func(t *testing.T) {
		t.Parallel()

		res := callListDevices(t, cs, nil)
		require.False(t, res.IsError, "list_devices failed: %v", res.Content)

		out := decodeDevices(t, res)
		assert.Equal(t, 2, out.Count)
		require.Len(t, out.Devices, 2)
	})

	t.Run("search narrows the list", func(t *testing.T) {
		t.Parallel()

		res := callListDevices(t, cs, map[string]any{"q": "printer"})
		require.False(t, res.IsError, "list_devices failed: %v", res.Content)

		out := decodeDevices(t, res)
		require.Equal(t, 1, out.Count)
		assert.Equal(t, "printer.local", out.Devices[0].Hostname)
		assert.Equal(t, macA, out.Devices[0].MAC)
	})

	// Both devices were swept moments ago, so both are online.
	t.Run("status filters", func(t *testing.T) {
		t.Parallel()

		online := decodeDevices(t, callListDevices(t, cs, map[string]any{"status": string(inventory.StatusOnline)}))
		assert.Equal(t, 2, online.Count)

		offline := decodeDevices(t, callListDevices(t, cs, map[string]any{"status": string(inventory.StatusOffline)}))
		assert.Equal(t, 0, offline.Count)
		assert.NotNil(t, offline.Devices, "an empty list should still be a list, not null")
	})

	// A misspelt filter is refused rather than ignored, since silently listing
	// everything looks like the filter matched everything.
	t.Run("an unknown status is refused", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callListDevices(t, cs, map[string]any{"status": "asleep"}))
		assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
		assert.Contains(t, doc["detail"], "online")
		assert.Contains(t, doc["detail"], "offline")
	})

	t.Run("an unknown sort is refused", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callListDevices(t, cs, map[string]any{"sort": "vendor"}))
		assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
	})
}

// The tool tells a client it only reads, so a client that asks before running
// a tool that changes things can run this one without asking.
func TestListDevicesIsReadOnly(t *testing.T) {
	t.Parallel()

	cs := connect(t, listDevices(testStore(t)))

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)

	ann := res.Tools[0].Annotations
	require.NotNil(t, ann)
	assert.True(t, ann.ReadOnlyHint)
	require.NotNil(t, ann.OpenWorldHint)
	assert.False(t, *ann.OpenWorldHint)
}
