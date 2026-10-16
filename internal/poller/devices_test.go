package poller

import (
	"context"
	"database/sql"
	"log/slog"
	"net/netip"
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

// stubDiscoverer is a source that answers with what it got.
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

// discoveredFact is what a router says about one device.
func discoveredFact(t *testing.T, ip, mac string) plugin.Fact {
	t.Helper()

	h, err := hosts.BuildHost(t.Context(), hosts.HostInput{IP: ip, MAC: mac})
	require.NoError(t, err)

	return plugin.Fact{Host: h, Present: true}
}

// newDiscoveryTask builds a device task with no networks to sweep. So Run
// exercises only the sources.
func newDiscoveryTask(t *testing.T, ds ...plugin.HostDiscoverer) (*Device, *sql.DB) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	d, err := NewDevice(
		slog.New(slog.DiscardHandler),
		nil,
		inventory.New(conn, slog.New(slog.DiscardHandler)),
		"test-sweep",
		time.Hour,
		nil,
		WithDiscoverers(ds...),
	)
	require.NoError(t, err)

	return d, conn
}

func TestRunRecordsWhatEachSourceKnows(t *testing.T) {
	t.Parallel()

	one := &stubDiscoverer{name: "routeros:gateway", facts: []plugin.Fact{discoveredFact(t, "192.0.2.10", "00:00:5e:00:53:01")}}
	two := &stubDiscoverer{name: "routeros:rack", facts: []plugin.Fact{discoveredFact(t, "198.51.100.10", "00:00:5e:00:53:02")}}

	d, conn := newDiscoveryTask(t, one, two)

	require.NoError(t, d.Run(t.Context()))

	assert.Equal(t, 1, one.calls)
	assert.Equal(t, 1, two.calls)

	// Each source has its own row, so the two stay separate.
	assert.Equal(t, []string{"routeros:gateway", "routeros:rack"}, sourceNames(t, conn))
	assert.Equal(t, 2, countRows(t, conn, `SELECT count(*) FROM devices`))
}

// If a source cannot be read, the sweep and the other sources must keep their
// readings.
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

// A source that returns facts with an error breaks the contract. The task does
// not record the facts.
func TestRunRecordsNothingFromAFailedRead(t *testing.T) {
	t.Parallel()

	failed := &stubDiscoverer{
		name:  "routeros:gateway",
		facts: []plugin.Fact{discoveredFact(t, "192.0.2.10", "00:00:5e:00:53:01")},
		err:   plugin.ErrUnreachable,
	}

	d, conn := newDiscoveryTask(t, failed)

	require.ErrorIs(t, d.Run(t.Context()), plugin.ErrUnreachable)
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM devices`))
}

// segmentDiscoverer is a source that also describes the segments that it
// serves.
type segmentDiscoverer struct {
	stubDiscoverer

	networks []plugin.Network
	netErr   error

	netCalls int
}

func (s *segmentDiscoverer) Networks(context.Context) ([]plugin.Network, error) {
	s.netCalls++

	return s.networks, s.netErr
}

func TestRunRecordsTheSegmentsASourceServes(t *testing.T) {
	t.Parallel()

	src := &segmentDiscoverer{
		stubDiscoverer: stubDiscoverer{
			name:  "routeros:gateway",
			facts: []plugin.Fact{discoveredFact(t, "192.0.2.10", "00:00:5e:00:53:01")},
		},
		networks: []plugin.Network{{
			Prefix: netip.MustParsePrefix("192.0.2.0/24"),
			Name:   "Home",
			VLAN:   10,
		}},
	}

	d, conn := newDiscoveryTask(t, src)

	require.NoError(t, d.Run(t.Context()))

	assert.Equal(t, 1, src.netCalls)
	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM networks WHERE name = 'Home' AND vlan_id = 10`))

	// The task recorded the segment before the facts. So the address went to
	// it.
	assert.Equal(t, 1, countRows(t, conn,
		`SELECT count(*) FROM addresses WHERE network_id IS NOT NULL`))
}

// The segments only add names to the devices. So the task still reads a source
// that does not describe them.
func TestRunKeepsTheDevicesWhenTheSegmentsCannotBeRead(t *testing.T) {
	t.Parallel()

	src := &segmentDiscoverer{
		stubDiscoverer: stubDiscoverer{
			name:  "routeros:gateway",
			facts: []plugin.Fact{discoveredFact(t, "192.0.2.10", "00:00:5e:00:53:01")},
		},
		netErr: plugin.ErrUnreachable,
	}

	d, conn := newDiscoveryTask(t, src)

	require.NoError(t, d.Run(t.Context()))

	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM devices`))
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM networks`))
}

// The task asks only a source that can describe its segments for them.
func TestRunAsksOnlyTheSourcesThatKnowTheirSegments(t *testing.T) {
	t.Parallel()

	plain := &stubDiscoverer{
		name:  "routeros:gateway",
		facts: []plugin.Fact{discoveredFact(t, "192.0.2.10", "00:00:5e:00:53:01")},
	}

	d, conn := newDiscoveryTask(t, plain)

	require.NoError(t, d.Run(t.Context()))

	assert.Equal(t, 1, countRows(t, conn, `SELECT count(*) FROM devices`))
	assert.Zero(t, countRows(t, conn, `SELECT count(*) FROM networks`))
}

// The run scans the ports of a device that it finds in the same run. A device
// that it already knew waits for the full port scan.
func TestRunPortScansTheDevicesItDiscovered(t *testing.T) {
	t.Parallel()

	open := listener(t)

	ports, _, conn := newPortsTask(t, time.Hour,
		scanner.WithPorts([]uint16{open}),
		scanner.WithDialTimeout(time.Second),
	)

	src := &segmentDiscoverer{
		stubDiscoverer: stubDiscoverer{
			name:  "routeros:gateway",
			facts: []plugin.Fact{discoveredFact(t, "127.0.0.1", "00:00:5e:00:53:01")},
		},
		networks: []plugin.Network{{Prefix: netip.MustParsePrefix("127.0.0.0/8")}},
	}

	d, err := NewDevice(
		slog.New(slog.DiscardHandler),
		nil,
		inventory.New(conn, slog.New(slog.DiscardHandler)),
		"test-sweep",
		time.Hour,
		nil,
		WithDiscoverers(src),
		WithPortScan(ports),
	)
	require.NoError(t, err)

	require.NoError(t, d.Run(t.Context()))
	assert.Equal(t, 1, newDeviceScans(t, conn))
	assert.Equal(t, 1, countRows(t, conn, `SELECT COUNT(*) FROM device_ports WHERE state = 'open'`))

	require.NoError(t, d.Run(t.Context()))
	assert.Equal(t, 1, newDeviceScans(t, conn), "a device already known is not scanned again")
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
