package poller

import (
	"database/sql"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPortsTask builds a port-scan task over an empty inventory. The scanner is
// real; the scheduling tests just never call it.
func newPortsTask(t *testing.T, interval time.Duration, opts ...scanner.PortOption) (*Ports, *inventory.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	store := inventory.New(conn, slog.New(slog.DiscardHandler))
	sc := scanner.NewPortScanner(slog.New(slog.DiscardHandler), opts...)

	return NewPorts(slog.New(slog.DiscardHandler), sc, store, "test-sweep", interval), store, conn
}

// sweptDevice records a discovery sweep that finds one host, so the port scan
// has an address to aim at.
func sweptDevice(t *testing.T, store *inventory.Store, addr string) {
	t.Helper()

	h, err := hosts.BuildHost(t.Context(), hosts.HostInput{IP: addr, MAC: "00:00:5e:00:53:01"})
	require.NoError(t, err)

	_, err = store.RecordSweep(t.Context(), "test-sweep",
		netip.MustParsePrefix("127.0.0.0/8"), []scanner.Host{{Host: h}})
	require.NoError(t, err)
}

func TestPortsName(t *testing.T) {
	t.Parallel()

	p, _, _ := newPortsTask(t, time.Hour)
	assert.Equal(t, "port_scanner", p.Name())
}

func TestPortsDueInWithoutHistoryIsNow(t *testing.T) {
	t.Parallel()

	p, _, _ := newPortsTask(t, time.Hour)
	assert.Zero(t, p.DueIn(t.Context()), "a database with no port scan in it has nothing to wait for")
}

func TestPortsDueInResumesTheInterval(t *testing.T) {
	t.Parallel()

	p, store, _ := newPortsTask(t, time.Hour)

	_, err := store.RecordPorts(t.Context(), "test-sweep", nil)
	require.NoError(t, err)

	due := p.DueIn(t.Context())
	assert.Greater(t, due, 59*time.Minute)
	assert.LessOrEqual(t, due, time.Hour)
}

// A port scan does not credit the discovery schedule and a discovery sweep does
// not credit the port scan's: the two run to their own clocks.
func TestPortsDueInIgnoresADiscoverySweep(t *testing.T) {
	t.Parallel()

	p, store, _ := newPortsTask(t, time.Hour)

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix("192.0.2.0/24"), nil)
	require.NoError(t, err)

	assert.Zero(t, p.DueIn(t.Context()), "a discovery sweep says nothing about when to port-scan")
}

func TestPortsDueInHoldsOffWhenTheStoreCannotBeRead(t *testing.T) {
	t.Parallel()

	p, _, _ := newPortsTask(t, time.Hour)

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "closed.db"})
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	p.store = inventory.New(conn, slog.New(slog.DiscardHandler))

	assert.Equal(t, time.Hour, p.DueIn(t.Context()), "an unreadable store must not pass for no history")
}

func TestPortsRunScansEveryCurrentAddress(t *testing.T) {
	t.Parallel()

	// A listener on loopback, so the scan has a port that genuinely answers.
	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	open := uint16(l.Addr().(*net.TCPAddr).Port) //nolint:gosec // kernel-assigned port.

	p, store, conn := newPortsTask(t, time.Hour,
		scanner.WithPorts([]uint16{open}),
		scanner.WithDialTimeout(time.Second),
	)
	sweptDevice(t, store, "127.0.0.1")

	require.NoError(t, p.Run(t.Context()))

	_, err = store.LastSuccessfulScanAt(t.Context(), dbtype.ScanPorts)
	require.NoError(t, err, "the run recorded a finished port scan")

	var openRows int
	require.NoError(t, conn.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM device_ports WHERE port = ? AND state = 'open'`, open).Scan(&openRows))
	assert.Equal(t, 1, openRows)

	assert.Equal(t, 1, countRows(t, conn, `SELECT COUNT(*) FROM events WHERE kind = 'PORT_OPENED'`))
}

func TestPortsRunWithNoTargetsAsksToRetry(t *testing.T) {
	t.Parallel()

	p, store, _ := newPortsTask(t, time.Hour)

	// errNotReady, not nil: nothing to scan means retry soon, not sit out the interval.
	require.ErrorIs(t, p.Run(t.Context()), errNotReady)

	_, err := store.LastSuccessfulScanAt(t.Context(), dbtype.ScanPorts)
	require.ErrorIs(t, err, inventory.ErrNotFound, "an empty inventory records no scan")
}
