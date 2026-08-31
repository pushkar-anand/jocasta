package inventory

import (
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// curated returns a store holding one swept device, and its id.
func curated(t *testing.T) (*Store, int64) {
	t.Helper()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	return s, deviceIDByMAC(t, conn, macA)
}

func TestUpdateCurationApplies(t *testing.T) {
	t.Parallel()

	s, id := curated(t)

	got, err := s.UpdateCuration(t.Context(), id, Curation{
		Label: "Office printer",
		Notes: "Hallway, second floor.",
		Group: "office",
		Type:  "printer",
	})
	require.NoError(t, err)

	assert.Equal(t, "Office printer", got.Label)
	assert.Equal(t, "Hallway, second floor.", got.Notes)
	assert.Equal(t, "office", got.Group)
	assert.Equal(t, "printer", got.Type)
	assert.False(t, got.Ignored)

	// The label is what the device is called once it has one.
	assert.Equal(t, "Office printer", got.Name())

	// And it is what came back that was stored, not only what was returned.
	reread, err := s.GetDevice(t.Context(), id)
	require.NoError(t, err)
	assert.Equal(t, "Office printer", reread.Label)
	assert.Equal(t, "office", reread.Group)
}

// Nothing a scan writes is the user's to change, so curating must not disturb
// the identity the network reported.
func TestUpdateCurationLeavesTheScannedFieldsAlone(t *testing.T) {
	t.Parallel()

	s, id := curated(t)

	before, err := s.GetDevice(t.Context(), id)
	require.NoError(t, err)

	after, err := s.UpdateCuration(t.Context(), id, Curation{Label: "Office printer"})
	require.NoError(t, err)

	assert.Equal(t, before.MAC, after.MAC)
	assert.Equal(t, before.Hostname, after.Hostname)
	assert.Equal(t, before.Vendor, after.Vendor)
	assert.Equal(t, before.IdentitySource, after.IdentitySource)
	assert.Equal(t, before.FirstSeen, after.FirstSeen)
	assert.Equal(t, before.LastSeen, after.LastSeen)
	assert.Equal(t, before.Current, after.Current)
}

func TestUpdateCurationTrims(t *testing.T) {
	t.Parallel()

	s, id := curated(t)

	got, err := s.UpdateCuration(t.Context(), id, Curation{Label: "  Office printer  ", Group: " office "})
	require.NoError(t, err)

	assert.Equal(t, "Office printer", got.Label)
	assert.Equal(t, "office", got.Group)

	// A label of only spaces is not a name, so it clears rather than becoming
	// one that renders as nothing.
	got, err = s.UpdateCuration(t.Context(), id, Curation{Label: "   "})
	require.NoError(t, err)
	assert.Empty(t, got.Label)
	assert.Equal(t, "printer.local", got.Name(), "it falls back to what the network reported")
}

// A form submits every field, so one left empty means cleared.
func TestUpdateCurationClearsOmittedFields(t *testing.T) {
	t.Parallel()

	s, id := curated(t)

	_, err := s.UpdateCuration(t.Context(), id, Curation{Label: "Office printer", Group: "office"})
	require.NoError(t, err)

	got, err := s.UpdateCuration(t.Context(), id, Curation{Label: "Office printer"})
	require.NoError(t, err)

	assert.Equal(t, "Office printer", got.Label)
	assert.Empty(t, got.Group)
}

func TestUpdateCurationIgnores(t *testing.T) {
	t.Parallel()

	s, id := curated(t)

	_, err := s.UpdateCuration(t.Context(), id, Curation{Ignored: true})
	require.NoError(t, err)

	// An ignored device is left out of a list unless it is asked for, and is
	// still counted.
	devices, err := s.ListDevices(t.Context(), DeviceFilter{})
	require.NoError(t, err)
	assert.Empty(t, devices)

	devices, err = s.ListDevices(t.Context(), DeviceFilter{IncludeIgnored: true})
	require.NoError(t, err)
	assert.Len(t, devices, 1)

	stats, err := s.Stats(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Ignored)
	assert.Equal(t, 1, stats.Total)
}

func TestUpdateCurationRecordsWhatChanged(t *testing.T) {
	t.Parallel()

	s, id := curated(t)

	_, err := s.UpdateCuration(t.Context(), id, Curation{Label: "Office printer", Group: "office"})
	require.NoError(t, err)

	events, err := s.DeviceEvents(t.Context(), id, 10)
	require.NoError(t, err)

	edited := make(map[string]Event)

	for _, e := range events {
		if e.Kind == dbtype.EventDeviceEdited {
			edited[e.Detail] = e
		}
	}

	require.Len(t, edited, 2, "one event per field that moved")

	assert.Equal(t, "Office printer", edited["label"].NewValue)
	assert.Empty(t, edited["label"].OldValue, "it had no label before")
	assert.Equal(t, "office", edited["group"].NewValue)

	// A second edit records the value it replaced.
	_, err = s.UpdateCuration(t.Context(), id, Curation{Label: "Hallway printer", Group: "office"})
	require.NoError(t, err)

	events, err = s.DeviceEvents(t.Context(), id, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Equal(t, dbtype.EventDeviceEdited, events[0].Kind)
	assert.Equal(t, "label", events[0].Detail)
	assert.Equal(t, "Office printer", events[0].OldValue)
	assert.Equal(t, "Hallway printer", events[0].NewValue)
}

// An edit that changed nothing is not a change, and should not fill the log
// with entries saying so.
func TestUpdateCurationWithoutAChangeRecordsNothing(t *testing.T) {
	t.Parallel()

	s, id := curated(t)

	_, err := s.UpdateCuration(t.Context(), id, Curation{Label: "Office printer"})
	require.NoError(t, err)

	before, err := s.DeviceEvents(t.Context(), id, 50)
	require.NoError(t, err)

	_, err = s.UpdateCuration(t.Context(), id, Curation{Label: "Office printer"})
	require.NoError(t, err)

	after, err := s.DeviceEvents(t.Context(), id, 50)
	require.NoError(t, err)

	assert.Len(t, after, len(before))
}

func TestUpdateCurationUnknownDeviceIsNotFound(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	_, err := s.UpdateCuration(t.Context(), 404, Curation{Label: "Nothing"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestEditsNamesEveryUserOwnedField(t *testing.T) {
	t.Parallel()

	s, id := curated(t)

	_, err := s.UpdateCuration(t.Context(), id, Curation{Type: "printer", Ignored: true})
	require.NoError(t, err)

	events, err := s.DeviceEvents(t.Context(), id, 10)
	require.NoError(t, err)

	fields := make([]string, 0, 2)

	for _, e := range events {
		if e.Kind == dbtype.EventDeviceEdited {
			fields = append(fields, e.Detail)
		}
	}

	assert.ElementsMatch(t, []string{"type", "ignored"}, fields)

	for _, e := range events {
		if e.Detail == "ignored" {
			assert.Equal(t, "no", e.OldValue)
			assert.Equal(t, "yes", e.NewValue)
		}
	}
}
