package poller

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
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

// stubDiscoverer is a source that answers with whatever it was given.
type stubDiscoverer struct {
	name  string
	facts []plugin.Fact
	err   error

	calls int
}

func (s *stubDiscoverer) Name() string            { return s.name }
func (s *stubDiscoverer) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

func (s *stubDiscoverer) Discover(context.Context) ([]plugin.Fact, error) {
	s.calls++

	return s.facts, s.err
}

// discoveredFact is what a router claims about one device.
func discoveredFact(t *testing.T, ip, mac string) plugin.Fact {
	t.Helper()

	h, err := hosts.BuildHost(t.Context(), hosts.HostInput{IP: ip, MAC: mac})
	require.NoError(t, err)

	return plugin.Fact{Host: h, Present: true}
}

// newDiscoveryTask builds a device task with no networks to sweep, so Run
// exercises only the sources.
func newDiscoveryTask(t *testing.T, ds ...plugin.HostDiscoverer) (*Device, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Conn.Close() })

	d, err := NewDevice(
		slog.New(slog.DiscardHandler),
		nil,
		inventory.New(conn.Conn, slog.New(slog.DiscardHandler)),
		"test-sweep",
		time.Hour,
		nil,
		WithDiscoverers(ds...),
	)
	require.NoError(t, err)

	return d, conn.Conn
}

func TestRunRecordsWhatEachSourceKnows(t *testing.T) {
	t.Parallel()

	one := &stubDiscoverer{name: "routeros:gateway", facts: []plugin.Fact{discoveredFact(t, "192.0.2.10", "00:00:5e:00:53:01")}}
	two := &stubDiscoverer{name: "routeros:rack", facts: []plugin.Fact{discoveredFact(t, "198.51.100.10", "00:00:5e:00:53:02")}}

	d, conn := newDiscoveryTask(t, one, two)

	require.NoError(t, d.Run(t.Context()))

	assert.Equal(t, 1, one.calls)
	assert.Equal(t, 1, two.calls)

	// Each source files its own row, so the two stay distinguishable.
	assert.Equal(t, []string{"routeros:gateway", "routeros:rack"}, sourceNames(t, conn))
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM devices`))
}

// A source that cannot be read must not cost the sweep or the other sources
// their readings.
func TestRunIsolatesAFailedSource(t *testing.T) {
	t.Parallel()

	broken := &stubDiscoverer{name: "routeros:broken", err: plugin.ErrAuth}
	working := &stubDiscoverer{name: "routeros:gateway", facts: []plugin.Fact{discoveredFact(t, "192.0.2.10", "00:00:5e:00:53:01")}}

	d, conn := newDiscoveryTask(t, broken, working)

	err := d.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, plugin.ErrAuth)

	assert.Equal(t, 1, working.calls, "the working source is still read")
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM devices`))
}

// Facts and an error together are one table answering while another timed out.
// The half that arrived is true, so it is recorded rather than discarded.
func TestRunRecordsAPartialRead(t *testing.T) {
	t.Parallel()

	partial := &stubDiscoverer{
		name:  "routeros:gateway",
		facts: []plugin.Fact{discoveredFact(t, "192.0.2.10", "00:00:5e:00:53:01")},
		err:   plugin.ErrUnreachable,
	}

	d, conn := newDiscoveryTask(t, partial)

	require.NoError(t, d.Run(t.Context()), "a source that answered in part has not failed")
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM devices`))
}

func TestRunWithoutSourcesDoesNothing(t *testing.T) {
	t.Parallel()

	d, conn := newDiscoveryTask(t)

	require.NoError(t, d.Run(t.Context()))
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM scans`))
}

func countRows(t *testing.T, conn *sql.DB, q string) int {
	t.Helper()

	var n int
	require.NoError(t, conn.QueryRowContext(t.Context(), q).Scan(&n))

	return n
}

func sourceNames(t *testing.T, conn *sql.DB) []string {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), `SELECT name FROM sources ORDER BY name`)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var out []string

	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))

		out = append(out, s)
	}

	require.NoError(t, rows.Err())

	return out
}
