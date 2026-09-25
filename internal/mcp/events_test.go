package mcp

import (
	"log/slog"
	"net/http"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListEvents(t *testing.T) {
	t.Parallel()

	store := seededStore(t)
	cs := connect(t, func(s *mcpsdk.Server, log *slog.Logger) {
		listDevices(store)(s, log)
		listEvents(store)(s, log)
	})

	all := decodeAs[listEventsOutput](t, callTool(t, cs, "list_events", nil))
	require.NotEmpty(t, all.Events, "sweeping two new devices should have logged something")
	assert.Equal(t, len(all.Events), all.Count)
	assert.Nil(t, all.NextCursor, "the whole log fits one default page")

	t.Run("newest first", func(t *testing.T) {
		t.Parallel()

		for i := 1; i < len(all.Events); i++ {
			assert.False(t, all.Events[i].At.After(all.Events[i-1].At), "event %d is newer than the one before it", i)
		}
	})

	t.Run("kinds narrow the log", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[listEventsOutput](t, callTool(t, cs, "list_events", map[string]any{
			"kinds": []string{string(dbtype.EventDeviceDiscovered)},
		}))

		require.Equal(t, 2, out.Count, "both seeded devices were discovered")

		for _, e := range out.Events {
			assert.Equal(t, dbtype.EventDeviceDiscovered, e.Kind)
		}
	})

	t.Run("a device narrows the log to its own events", func(t *testing.T) {
		t.Parallel()

		id := deviceID(t, cs, "printer")

		out := decodeAs[listEventsOutput](t, callTool(t, cs, "list_events", map[string]any{"device_id": id}))
		require.NotEmpty(t, out.Events)

		for _, e := range out.Events {
			assert.Equal(t, id, e.DeviceID)
		}
	})

	// Following next_cursor one event at a time reads the same log, in the same
	// order, as reading it in one go.
	t.Run("paging walks the whole log", func(t *testing.T) {
		t.Parallel()

		var (
			walked []*inventory.Event
			cursor string
		)

		for range len(all.Events) + 1 {
			args := map[string]any{"limit": 1}
			if cursor != "" {
				args["cursor"] = cursor
			}

			page := decodeAs[listEventsOutput](t, callTool(t, cs, "list_events", args))
			walked = append(walked, page.Events...)

			if page.NextCursor == nil {
				break
			}

			token, err := page.NextCursor.Encode()
			require.NoError(t, err)

			cursor = token
		}

		require.Len(t, walked, len(all.Events))

		for i := range walked {
			assert.Equal(t, all.Events[i].ID, walked[i].ID)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()

		for name, tc := range map[string]struct {
			args   map[string]any
			status int
		}{
			"a cursor this server did not issue": {map[string]any{"cursor": "not-a-cursor"}, http.StatusBadRequest},
			"an unknown kind":                    {map[string]any{"kinds": []string{"DEVICE_EXPLODED"}}, http.StatusBadRequest},
			"a page past the ceiling":            {map[string]any{"limit": inventory.MaxPageSize + 1}, http.StatusBadRequest},
			"a device that does not exist":       {map[string]any{"device_id": 9999}, http.StatusNotFound},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := problemOf(t, callTool(t, cs, "list_events", tc.args))
				assert.Equal(t, float64(tc.status), doc["status"])
				assert.Equal(t, "list_events", doc["instance"])
			})
		}
	})
}

// The description names every event kind, so an agent can filter without
// guessing; a kind added to dbtype reaches it without an edit here.
func TestListEventsDescribesEveryKind(t *testing.T) {
	t.Parallel()

	cs := connect(t, listEvents(testStore(t)))

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)

	for _, k := range dbtype.EventKinds() {
		assert.Contains(t, res.Tools[0].Description, string(k))
	}
}
