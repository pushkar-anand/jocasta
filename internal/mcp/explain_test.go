package mcp

import (
	"log/slog"
	"net/http"
	"net/netip"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplainDevice(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
		listDevices(store)(s, log)
		explainDevice(store)(s, log)
	})

	id := deviceID(t, cs, "printer")

	t.Run("the facts and the rule behind the guess", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[inventory.ClassExplanation](t, callTool(t, cs, "explain_device", map[string]any{"id": id}))

		assert.Equal(t, classify.Printer, out.Class)
		assert.Equal(t, classify.Printer, out.Guess)
		assert.False(t, out.Overridden)
		assert.Equal(t, "printer.local", out.Facts.Hostname)

		require.NotNil(t, out.Rule)
		assert.Equal(t, classify.Printer, out.Rule.Class)
		assert.NotEmpty(t, out.Rule.Reason)
	})

	t.Run("a device that does not exist is a 404", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "explain_device", map[string]any{"id": 9999}))
		assert.Equal(t, float64(http.StatusNotFound), doc["status"])
		assert.Equal(t, "explain_device", doc["instance"])
	})

	t.Run("an id the inventory never issues is refused", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "explain_device", map[string]any{"id": 0}))
		assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
	})
}

func TestExplainDeviceNoRuleMatches(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, "host-a")})
	require.NoError(t, err)

	cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
		listDevices(store)(s, log)
		explainDevice(store)(s, log)
	})

	out := decodeAs[inventory.ClassExplanation](t, callTool(t, cs, "explain_device",
		map[string]any{"id": deviceID(t, cs, "host-a")}))

	assert.Equal(t, classify.Unknown, out.Guess)
	assert.Nil(t, out.Rule)
	assert.NotNil(t, out.OtherRules, "an empty list should still be a list, not null")
	assert.NotNil(t, out.Facts.OpenPorts, "an empty list should still be a list, not null")
}
