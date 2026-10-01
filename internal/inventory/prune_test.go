package inventory

import (
	"database/sql"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRetention = 90 * 24 * time.Hour

func countRows(t *testing.T, conn *sql.DB, q string, args ...any) int64 {
	t.Helper()

	var n int64
	require.NoError(t, conn.QueryRowContext(t.Context(), q, args...).Scan(&n))

	return n
}

// A sweep older than the window goes with its events; a recent one stays.
func TestPruneDeletesEventsAndScansPastRetention(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	old := sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	oldEvents := countRows(t, conn, `SELECT COUNT(*) FROM events WHERE scan_id = ?`, old.ScanID)
	require.Positive(t, oldEvents)

	advance(testRetention + time.Hour)

	recent := sweep(t, s, host("192.0.2.11", macB, "laptop.local"))
	recentEvents := countRows(t, conn, `SELECT COUNT(*) FROM events WHERE scan_id = ?`, recent.ScanID)
	require.Positive(t, recentEvents)

	res, err := s.Prune(t.Context(), Retention{History: testRetention})
	require.NoError(t, err)

	assert.Equal(t, oldEvents, res.Events)
	assert.Equal(t, int64(1), res.Scans)

	assert.Zero(t, countRows(t, conn, `SELECT COUNT(*) FROM scans WHERE id = ?`, old.ScanID))
	assert.Equal(t, int64(1), countRows(t, conn, `SELECT COUNT(*) FROM scans WHERE id = ?`, recent.ScanID))
	assert.Equal(t, recentEvents, countRows(t, conn, `SELECT COUNT(*) FROM events`))

	// With no device window, the device the old sweep found is still there.
	assert.NotZero(t, deviceIDByMAC(t, conn, macA))
}

// A scan left RUNNING is kept however old, since it is not finished history.
func TestPruneKeepsARunningScan(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	old := sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	_, err := conn.ExecContext(t.Context(), `UPDATE scans SET status = 'RUNNING' WHERE id = ?`, old.ScanID)
	require.NoError(t, err)

	advance(testRetention + time.Hour)

	res, err := s.Prune(t.Context(), Retention{History: testRetention})
	require.NoError(t, err)

	assert.Zero(t, res.Scans)
	assert.Positive(t, res.Events, "the running scan's events still age out")
	assert.Equal(t, int64(1), countRows(t, conn, `SELECT COUNT(*) FROM scans WHERE id = ?`, old.ScanID))
}

// The cutoff is exclusive: an event stamped exactly at it is kept.
func TestPruneKeepsAnEventAtTheCutoff(t *testing.T) {
	t.Parallel()

	s, conn, _ := clockStore(t)

	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	cutoff := now.Add(-testRetention)

	for _, at := range []time.Time{cutoff.Add(-time.Millisecond), cutoff, cutoff.Add(time.Millisecond)} {
		_, err := conn.ExecContext(t.Context(),
			`INSERT INTO events (kind, occurred_at) VALUES ('TEST', ?)`, dbtype.NewTime(at))
		require.NoError(t, err)
	}

	res, err := s.Prune(t.Context(), Retention{History: testRetention})
	require.NoError(t, err)

	assert.Equal(t, int64(1), res.Events)
	assert.Equal(t, int64(2), countRows(t, conn, `SELECT COUNT(*) FROM events`))
}

// A device away longer than the window goes when nobody curated it, and stays
// when its owner set any one field or marked it ignored.
func TestPruneDeletesOnlyUncuratedStaleDevices(t *testing.T) {
	t.Parallel()

	curations := map[string]Curation{
		"label":   {Label: "Hallway camera"},
		"notes":   {Notes: "Spare"},
		"group":   {Group: "Garage"},
		"type":    {Type: "camera"},
		"ignored": {Ignored: true},
	}

	for name, c := range curations {
		t.Run("kept for its "+name, func(t *testing.T) {
			t.Parallel()

			s, conn, advance := clockStore(t)
			sweep(t, s, host("192.0.2.10", macA, "host-a"))
			id := deviceIDByMAC(t, conn, macA)

			_, err := s.UpdateCuration(t.Context(), id, c)
			require.NoError(t, err)

			advance(testRetention + time.Hour)

			res, err := s.Prune(t.Context(), Retention{Devices: testRetention})
			require.NoError(t, err)

			assert.Zero(t, res.Devices)
			assert.Equal(t, int64(1), countRows(t, conn, `SELECT COUNT(*) FROM devices WHERE id = ?`, id))
		})
	}

	t.Run("kept for being watched", func(t *testing.T) {
		t.Parallel()

		s, conn, advance := clockStore(t)
		sweep(t, s, host("192.0.2.10", macA, "host-a"))
		id := deviceIDByMAC(t, conn, macA)

		_, err := s.Watch(t.Context(), id, true)
		require.NoError(t, err)

		advance(testRetention + time.Hour)

		res, err := s.Prune(t.Context(), Retention{Devices: testRetention})
		require.NoError(t, err)

		assert.Zero(t, res.Devices)
		assert.Equal(t, int64(1), countRows(t, conn, `SELECT COUNT(*) FROM devices WHERE id = ?`, id))
	})

	t.Run("deleted when uncurated", func(t *testing.T) {
		t.Parallel()

		s, conn, advance := clockStore(t)
		sweep(t, s, host("192.0.2.10", macA, "host-a"))
		stale := deviceIDByMAC(t, conn, macA)

		advance(testRetention + time.Hour)

		sweep(t, s, host("192.0.2.11", macB, "host-b"))
		recent := deviceIDByMAC(t, conn, macB)

		res, err := s.Prune(t.Context(), Retention{Devices: testRetention})
		require.NoError(t, err)

		assert.Equal(t, int64(1), res.Devices)
		assert.Zero(t, countRows(t, conn, `SELECT COUNT(*) FROM devices WHERE id = ?`, stale))
		assert.Equal(t, int64(1), countRows(t, conn, `SELECT COUNT(*) FROM devices WHERE id = ?`, recent))

		// Its addresses and claims go with it; its events stay, unattached.
		assert.Zero(t, countRows(t, conn, `SELECT COUNT(*) FROM addresses WHERE device_id = ?`, stale))
		assert.Zero(t, countRows(t, conn, `SELECT COUNT(*) FROM device_sources WHERE device_id = ?`, stale))
		assert.Zero(t, countRows(t, conn, `SELECT COUNT(*) FROM events WHERE device_id = ?`, stale))
		assert.Positive(t, countRows(t, conn, `SELECT COUNT(*) FROM events WHERE device_id IS NULL`))
	})

	t.Run("seen again, it comes back as a new device", func(t *testing.T) {
		t.Parallel()

		s, conn, advance := clockStore(t)
		sweep(t, s, host("192.0.2.10", macA, "host-a"))
		first := deviceIDByMAC(t, conn, macA)

		advance(testRetention + time.Hour)

		_, err := s.Prune(t.Context(), Retention{Devices: testRetention})
		require.NoError(t, err)

		sweep(t, s, host("192.0.2.10", macA, "host-a"))
		assert.NotEqual(t, first, deviceIDByMAC(t, conn, macA))
	})
}
