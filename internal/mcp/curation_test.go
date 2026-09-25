package mcp

import (
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// curation is a full set of the five fields, for the device named.
func curation(id int64) map[string]any {
	return map[string]any{
		"id":      id,
		"label":   "Office printer",
		"group":   "office",
		"type":    string(classify.Printer),
		"notes":   "Second floor.",
		"ignored": false,
	}
}

func TestUpdateDeviceCuration(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
		listDevices(store)(s, log)
		updateDeviceCuration(store)(s, log)
	})

	id := deviceID(t, cs, "printer")

	t.Run("every field is set, and the change is logged", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[updateDeviceCurationOutput](t, callTool(t, cs, "update_device_curation", curation(id)))
		require.NotNil(t, out.Device)
		assert.Equal(t, id, out.Device.ID)
		assert.Equal(t, "Office printer", out.Device.Label)
		assert.Equal(t, "office", out.Device.Group)
		assert.Equal(t, string(classify.Printer), out.Device.Type)
		assert.Equal(t, classify.Printer, out.Device.Class, "the owner's type overrides the guess")
		assert.Equal(t, "Second floor.", out.Device.Notes)

		page, err := store.ListEvents(t.Context(), inventory.Page{
			Limit: inventory.DefaultPageSize, Device: id, EventKinds: []dbtype.EventKind{dbtype.EventDeviceEdited},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, page.Events)
	})

	t.Run("a device that does not exist is a 404", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "update_device_curation", curation(9999)))
		assert.Equal(t, float64(http.StatusNotFound), doc["status"])
		assert.Equal(t, "update_device_curation", doc["instance"])
	})

	// Every field is replaced, so one left out would be cleared without the
	// agent meaning to, and the call is refused.
	for _, field := range []string{"id", "label", "group", "type", "notes", "ignored"} {
		t.Run("without "+field+" is refused", func(t *testing.T) {
			t.Parallel()

			args := curation(id)
			delete(args, field)

			doc := problemOf(t, callTool(t, cs, "update_device_curation", args))
			assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
		})
	}

	for name, change := range map[string]map[string]any{
		"an unknown type":    {"type": "toaster"},
		"a label too long":   {"label": strings.Repeat("a", labelMaxLength+1)},
		"a group too long":   {"group": strings.Repeat("a", groupMaxLength+1)},
		"notes too long":     {"notes": strings.Repeat("a", notesMaxLength+1)},
		"an id never issued": {"id": 0},
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			t.Parallel()

			args := curation(id)
			maps.Copy(args, change)

			doc := problemOf(t, callTool(t, cs, "update_device_curation", args))
			assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
		})
	}
}

// Empty strings clear the fields, and an empty type hands the device back to
// the classifier.
func TestUpdateDeviceCurationClears(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
		listDevices(store)(s, log)
		updateDeviceCuration(store)(s, log)
	})

	id := deviceID(t, cs, "printer")
	decodeAs[updateDeviceCurationOutput](t, callTool(t, cs, "update_device_curation", curation(id)))

	cleared := map[string]any{"id": id, "label": "", "group": "", "type": "", "notes": "", "ignored": true}
	out := decodeAs[updateDeviceCurationOutput](t, callTool(t, cs, "update_device_curation", cleared))

	assert.Empty(t, out.Device.Label)
	assert.Empty(t, out.Device.Group)
	assert.Empty(t, out.Device.Type)
	assert.Equal(t, out.Device.ClassGuess, out.Device.Class, "with no override the guess applies")
	assert.Empty(t, out.Device.Notes)
	assert.True(t, out.Device.Ignored)
}

// The tool tells a client it changes things, so a client that asks before
// such a tool runs asks before this one.
func TestUpdateDeviceCurationIsNotReadOnly(t *testing.T) {
	t.Parallel()

	cs := connect(t, updateDeviceCuration(testStore(t)))

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)

	ann := res.Tools[0].Annotations
	require.NotNil(t, ann)
	assert.False(t, ann.ReadOnlyHint)
	require.NotNil(t, ann.DestructiveHint)
	assert.True(t, *ann.DestructiveHint)
}
