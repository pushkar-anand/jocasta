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
// real, but the scheduling tests never call it.
func newPortsTask(t *testing.T, interval time.Duration, opts ...scanner.PortOption) (*Ports, *inventory.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	store := inventory.New(conn, slog.New(slog.DiscardHandler))
	sc := scanner.NewPortScanner(slog.New(slog.DiscardHandler), opts...)

	return NewPorts(slog.New(slog.DiscardHandler), sc, store, "test-sweep", interval), store, conn
}

// sweptDevice records a discovery sweep that finds one host. So the port scan
// has an address to scan.
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

func TestPortsRunScansEveryCurrentAddress(t *testing.T) {
	t.Parallel()

	// A listener on loopback, so the scan has a port that really answers.
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

	// Nothing to scan gives errNotReady, so the poller tries again soon.
	require.ErrorIs(t, p.Run(t.Context()), errNotReady)

	_, err := store.LastSuccessfulScanAt(t.Context(), dbtype.ScanPorts)
	require.ErrorIs(t, err, inventory.ErrNotFound, "an empty inventory records no scan")
}

// listener opens a port on loopback. So a scan has a port that really
// answers.
func listener(t *testing.T) uint16 {
	t.Helper()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	return uint16(l.Addr().(*net.TCPAddr).Port) //nolint:gosec // kernel-assigned port.
}

// newDeviceScans counts the port scans of new devices only.
func newDeviceScans(t *testing.T, conn *sql.DB) int {
	t.Helper()

	return countRows(t, conn, `SELECT COUNT(*) FROM scans WHERE kind = 'PORTS' AND scope = 'NEW_DEVICES'`)
}

func TestPortsScanNewRecordsANewDeviceScan(t *testing.T) {
	t.Parallel()

	open := listener(t)

	p, store, conn := newPortsTask(t, time.Hour,
		scanner.WithPorts([]uint16{open}),
		scanner.WithDialTimeout(time.Second),
	)
	sweptDevice(t, store, "127.0.0.1")

	require.NoError(t, p.ScanNew(t.Context(), []netip.Addr{netip.MustParseAddr("127.0.0.1")}))

	assert.Equal(t, 1, newDeviceScans(t, conn))
	assert.Equal(t, 1, countRows(t, conn, `SELECT COUNT(*) FROM device_ports WHERE state = 'open'`))

	_, err := store.LatestScanOfKind(t.Context(), dbtype.ScanPorts)
	require.ErrorIs(t, err, inventory.ErrNotFound, "no full port scan has run")
}

// The full scan would not reach an address on no recorded network. So the
// new-device scan also leaves it out. With nothing left, it records no scan.
func TestPortsScanNewSkipsAddressesOffEveryNetwork(t *testing.T) {
	t.Parallel()

	p, store, conn := newPortsTask(t, time.Hour)
	sweptDevice(t, store, "127.0.0.1")

	require.NoError(t, p.ScanNew(t.Context(), []netip.Addr{netip.MustParseAddr("192.0.2.10")}))

	assert.Zero(t, newDeviceScans(t, conn))
}
