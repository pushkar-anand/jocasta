package inventory

import (
	"database/sql"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// presentSince reads a device's present_since, empty while it is quiet.
func presentSince(t *testing.T, conn *sql.DB, id int64) string {
	t.Helper()

	return queryString(t, conn, `SELECT present_since FROM devices WHERE id = ?`, id)
}

// watch has the owner watch the device with the given hardware address.
func watch(t *testing.T, s *Store, conn *sql.DB, mac string) int64 {
	t.Helper()

	id := deviceIDByMAC(t, conn, mac)

	_, err := s.Watch(t.Context(), id, true)
	require.NoError(t, err)

	return id
}

// A device's first sighting makes it present. Discovering it is the event, so
// it does not also come back.
func TestAFirstSightingIsPresentWithoutComingBack(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	res := sweep(t, s, host("192.0.2.10", macA, "nas.local"))

	assert.Zero(t, res.Back)

	id := deviceIDByMAC(t, conn, macA)

	assert.NotEmpty(t, presentSince(t, conn, id))
	assert.Equal(t, []dbtype.EventKind{dbtype.EventDeviceDiscovered, dbtype.EventAddressAdded}, eventKinds(t, conn, id))
}

// Missing a sweep inside the online window is not going quiet: the window is
// what absorbs a dropped reply.
func TestStaysPresentThroughAMissInsideTheWindow(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"))
	id := watch(t, s, conn, macA)

	advance(10 * time.Minute)

	res := sweep(t, s)

	assert.Zero(t, res.Quiet)
	assert.NotEmpty(t, presentSince(t, conn, id))
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventDeviceQuiet)
}

// Past the window every device goes quiet, and only a watched one says so in
// the log, quoting the sighting it went quiet after.
func TestGoesQuietPastTheWindowAndLogsOnlyWhenWatched(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"), host("192.0.2.11", macB, "phone.local"))
	watched := watch(t, s, conn, macA)
	lastSeen := queryString(t, conn, `SELECT last_seen FROM devices WHERE id = ?`, watched)

	advance(20 * time.Minute)

	res := sweep(t, s)

	assert.Equal(t, 2, res.Quiet)

	other := deviceIDByMAC(t, conn, macB)

	assert.Empty(t, presentSince(t, conn, watched))
	assert.Empty(t, presentSince(t, conn, other))

	assert.Contains(t, eventKinds(t, conn, watched), dbtype.EventDeviceQuiet)
	assert.NotContains(t, eventKinds(t, conn, other), dbtype.EventDeviceQuiet)

	at, err := time.Parse(dbtype.Layout, lastSeen)
	require.NoError(t, err)

	old := queryString(t, conn, `SELECT old_value FROM events WHERE device_id = ? AND kind = 'DEVICE_QUIET'`, watched)
	assert.Equal(t, at.Format(time.RFC3339), old)
}

// A quiet device seen again is present from that sighting, and a watched one
// logs how long it was away.
func TestComesBackAndLogsTheGapWhenWatched(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"), host("192.0.2.11", macB, "phone.local"))
	watched := watch(t, s, conn, macA)

	advance(20 * time.Minute)
	sweep(t, s)
	advance(2 * time.Hour)

	res := sweep(t, s, host("192.0.2.10", macA, "nas.local"), host("192.0.2.11", macB, "phone.local"))

	assert.Equal(t, 2, res.Back)
	assert.NotEmpty(t, presentSince(t, conn, watched))
	assert.NotContains(t, eventKinds(t, conn, deviceIDByMAC(t, conn, macB)), dbtype.EventDeviceBack)

	events, err := s.ListEvents(t.Context(), Page{Limit: DefaultPageSize, Device: watched, EventKinds: []dbtype.EventKind{dbtype.EventDeviceBack}})
	require.NoError(t, err)
	require.Len(t, events.Events, 1)

	assert.Equal(t, "after 2 hours quiet", events.Events[0].Change())
}

// A sweep says nothing about a device it had no address of to probe.
func TestASweepJudgesOnlyDevicesInItsPrefix(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"))
	advance(20 * time.Minute)

	res := sweepPrefix(t, s, "198.51.100.0/24")

	assert.Zero(t, res.Quiet)
	assert.NotEmpty(t, presentSince(t, conn, deviceIDByMAC(t, conn, macA)))
}

// The window is judged by the last sighting from any source, so a device the
// router saw a moment ago stays present through a sweep that missed it.
func TestARouterSightingKeepsADevicePresentThroughAMissedSweep(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"))
	advance(20 * time.Minute)
	report(t, s, fact("192.0.2.10", macA, "", true, ""))

	res := sweep(t, s)

	assert.Zero(t, res.Quiet)
	assert.NotEmpty(t, presentSince(t, conn, deviceIDByMAC(t, conn, macA)))
}

// A source reading every network judges the devices it holds a claim for and
// no others.
func TestARouterReadJudgesOnlyDevicesItClaims(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"))
	report(t, s, fact("192.0.2.11", macB, "", true, ""))
	advance(20 * time.Minute)

	res := report(t, s)

	assert.Equal(t, 1, res.Quiet)
	assert.NotEmpty(t, presentSince(t, conn, deviceIDByMAC(t, conn, macA)))
	assert.Empty(t, presentSince(t, conn, deviceIDByMAC(t, conn, macB)))
}

// A source that could not finish its read says nothing about what it did not
// return.
func TestAPartialReadJudgesNothing(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	report(t, s, fact("192.0.2.11", macB, "", true, ""))
	advance(20 * time.Minute)

	res, err := s.RecordPartialFacts(t.Context(), "test-router", dbtype.SourceRouter, nil)
	require.NoError(t, err)

	assert.Zero(t, res.Quiet)
	assert.NotEmpty(t, presentSince(t, conn, deviceIDByMAC(t, conn, macB)))
}

// After downtime nothing judged the devices, so one still answering on the
// first sweep back never went quiet and does not come back.
func TestARestartLogsNothingForDevicesStillAnswering(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"))
	id := watch(t, s, conn, macA)

	advance(2 * time.Hour)

	res := sweep(t, s, host("192.0.2.10", macA, "nas.local"))

	assert.Zero(t, res.Back)
	assert.Zero(t, res.Quiet)
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventDeviceQuiet)
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventDeviceBack)
}

// A device recorded before presence was has no present_since. Seen within the
// window, it was never quiet, so it is marked present without coming back.
func TestADeviceFromBeforePresenceIsMarkedPresentQuietly(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"))
	id := watch(t, s, conn, macA)

	_, err := conn.ExecContext(t.Context(), `UPDATE devices SET present_since = NULL WHERE id = ?`, id)
	require.NoError(t, err)

	advance(5 * time.Minute)

	res := sweep(t, s, host("192.0.2.10", macA, "nas.local"))

	assert.Zero(t, res.Back)
	assert.NotEmpty(t, presentSince(t, conn, id))
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventDeviceBack)
}

// Watching is an edit the owner makes, logged once per change, and the device
// reads it back.
func TestWatchIsLoggedAsAnEdit(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.local"))
	id := deviceIDByMAC(t, conn, macA)

	d, err := s.Watch(t.Context(), id, true)
	require.NoError(t, err)
	assert.True(t, d.Watched)
	assert.False(t, d.PresentSince.IsZero())

	_, err = s.Watch(t.Context(), id, true)
	require.NoError(t, err)

	edits := queryStrings(t, conn,
		`SELECT old_value || '>' || new_value FROM events WHERE device_id = ? AND kind = 'DEVICE_EDITED' AND detail = 'watched'`, id)
	assert.Equal(t, []string{"no>yes"}, edits)

	_, err = s.Watch(t.Context(), id+100, true)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSpanWords(t *testing.T) {
	t.Parallel()

	for d, want := range map[time.Duration]string{
		30 * time.Second: "0 minutes",
		time.Minute:      "1 minute",
		18 * time.Minute: "18 minutes",
		time.Hour:        "1 hour",
		47 * time.Hour:   "47 hours",
		48 * time.Hour:   "2 days",
	} {
		assert.Equal(t, want, spanWords(d), d.String())
	}
}
