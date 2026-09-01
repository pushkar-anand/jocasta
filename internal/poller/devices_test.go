package poller

import (
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDeviceTask builds a device task over an empty inventory. The scanner is
// nil because nothing here sweeps: these tests are about when the sweep is due.
func newDeviceTask(t *testing.T, interval time.Duration) (*Device, *inventory.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Conn.Close() })

	store := inventory.New(conn.Conn, slog.New(slog.DiscardHandler))

	d, err := NewDevice(
		slog.New(slog.DiscardHandler),
		nil,
		store,
		"test-sweep",
		interval,
		[]string{"192.0.2.0/24"},
	)
	require.NoError(t, err)

	return d, store, conn.Conn
}

func TestDeviceDueInWithoutHistoryIsNow(t *testing.T) {
	t.Parallel()

	d, _, _ := newDeviceTask(t, time.Hour)

	assert.Zero(t, d.DueIn(t.Context()), "a database with no sweep in it has nothing to wait for")
}

func TestDeviceDueInResumesTheInterval(t *testing.T) {
	t.Parallel()

	d, store, _ := newDeviceTask(t, time.Hour)

	_, err := store.RecordSweep(t.Context(), "test-sweep", d.networks[0].Masked(), []scanner.Host{})
	require.NoError(t, err)

	// The sweep just ran, so nearly the whole interval is still owed. It is the
	// remainder that matters here, not the millisecond: reporting the elapsed
	// time instead would give a few milliseconds, and reporting nothing at all
	// would sweep again immediately.
	due := d.DueIn(t.Context())
	assert.Greater(t, due, 59*time.Minute)
	assert.LessOrEqual(t, due, time.Hour)
}

// A failed sweep gathered nothing, so it buys no time: crediting it would leave
// the inventory untouched for two intervals after one bad run.
func TestDeviceDueInIgnoresAFailedSweep(t *testing.T) {
	t.Parallel()

	d, _, conn := newDeviceTask(t, time.Hour)

	_, err := conn.ExecContext(t.Context(),
		`INSERT INTO sources (kind, name) VALUES ('SWEEP', 'test-sweep');
		 INSERT INTO scans (source_id, kind, status, error, started_at, finished_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'test-sweep'), 'DISCOVERY', 'FAILED', 'boom',
		         STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now'), STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now'));`)
	require.NoError(t, err)

	// Guard against the test passing because no scan row exists at all: it must
	// be the status that is disqualifying, not the absence of a row.
	var n int
	require.NoError(t, conn.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM scans WHERE kind = 'DISCOVERY' AND finished_at IS NOT NULL`).Scan(&n))
	require.Equal(t, 1, n, "a finished DISCOVERY scan must be present for this test to mean anything")

	assert.Zero(t, d.DueIn(t.Context()), "a failed sweep must not count as one that ran")
}

// Not knowing whether the work is due is a reason to hold off: sweeping anyway
// would turn a restart loop into a scan loop.
func TestDeviceDueInHoldsOffWhenTheStoreCannotBeRead(t *testing.T) {
	t.Parallel()

	d, _, _ := newDeviceTask(t, time.Hour)

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "closed.db"})
	require.NoError(t, err)
	require.NoError(t, conn.Conn.Close())

	d.store = inventory.New(conn.Conn, slog.New(slog.DiscardHandler))

	assert.Equal(t, time.Hour, d.DueIn(t.Context()),
		"an unreadable store must not pass for no history")
}
