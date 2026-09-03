package inventory

import (
	"net/netip"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// portScan builds one address's scan result the way the scanner hands it over.
func portScan(addr string, open []uint16, scanned []uint16) scanner.PortScan {
	return scanner.PortScan{
		Addr:    netip.MustParseAddr(addr),
		Open:    open,
		Scanned: scanned,
	}
}

func recordPorts(t *testing.T, s *Store, scans ...scanner.PortScan) *PortSummary {
	t.Helper()

	sum, err := s.RecordPorts(t.Context(), "test-sweep", scans)
	require.NoError(t, err)

	return sum
}

// portRow is device_ports read back for one (device, port), for asserting the
// state machine through raw SQL rather than a reader that does not exist yet.
type portRow struct {
	state             string
	service           string
	firstSeen         string
	lastSeen          string
	changedAt         string
	firstEqualsChange bool
}

func devicePort(t *testing.T, s *Store, deviceID int64, port uint16) (portRow, bool) {
	t.Helper()

	var r portRow

	err := s.conn.QueryRowContext(t.Context(),
		`SELECT state, COALESCE(service, ''), first_seen, last_seen, changed_at
		 FROM device_ports WHERE device_id = ? AND port = ?`, deviceID, port,
	).Scan(&r.state, &r.service, &r.firstSeen, &r.lastSeen, &r.changedAt)
	if err != nil {
		return portRow{}, false
	}

	r.firstEqualsChange = r.firstSeen == r.changedAt

	return r, true
}

func TestRecordPortsRecordsOpenPorts(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	id := deviceIDByMAC(t, conn, macA)

	sum := recordPorts(t, s, portScan("192.0.2.10", []uint16{22, 443}, []uint16{22, 80, 443}))

	assert.Equal(t, 1, sum.Targets)
	assert.Equal(t, 1, sum.Devices)
	assert.Equal(t, 2, sum.Open)
	assert.Equal(t, 2, sum.Opened)
	assert.Zero(t, sum.Closed)
	assert.Zero(t, sum.Dropped)

	ssh, ok := devicePort(t, s, id, 22)
	require.True(t, ok)
	assert.Equal(t, "open", ssh.state)
	assert.Equal(t, "ssh", ssh.service)
	assert.True(t, ssh.firstEqualsChange, "a first sighting has not changed state")

	https, ok := devicePort(t, s, id, 443)
	require.True(t, ok)
	assert.Equal(t, "https", https.service)

	assert.Equal(t, []dbtype.EventKind{
		dbtype.EventDeviceDiscovered,
		dbtype.EventAddressAdded,
		dbtype.EventPortOpened,
		dbtype.EventPortOpened,
	}, eventKinds(t, conn, id))
}

func TestRecordPortsIsIdempotent(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	id := deviceIDByMAC(t, conn, macA)

	scan := portScan("192.0.2.10", []uint16{22}, []uint16{22, 80})
	recordPorts(t, s, scan)

	first, ok := devicePort(t, s, id, 22)
	require.True(t, ok)

	sum := recordPorts(t, s, scan)

	assert.Zero(t, sum.Opened, "a port already open is not opened again")
	assert.Zero(t, sum.Closed)

	second, ok := devicePort(t, s, id, 22)
	require.True(t, ok)
	assert.Equal(t, first.firstSeen, second.firstSeen, "first_seen is fixed")
	assert.Equal(t, first.changedAt, second.changedAt, "an unchanged port does not move changed_at")
	assert.Greater(t, second.lastSeen, first.lastSeen, "last_seen advances every scan")

	// One PORT_OPENED, not two.
	assert.Equal(t, 1, queryInt(t, conn,
		`SELECT COUNT(*) FROM events WHERE device_id = ? AND kind = 'PORT_OPENED'`, id))
}

func TestRecordPortsClosesAPortThatStoppedAnswering(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	id := deviceIDByMAC(t, conn, macA)

	recordPorts(t, s, portScan("192.0.2.10", []uint16{22, 443}, []uint16{22, 80, 443}))
	opened, _ := devicePort(t, s, id, 443)

	sum := recordPorts(t, s, portScan("192.0.2.10", []uint16{22}, []uint16{22, 80, 443}))

	assert.Equal(t, 1, sum.Closed)
	assert.Zero(t, sum.Opened)
	assert.Equal(t, 1, sum.Open, "only 22 answered the second run")

	closed, ok := devicePort(t, s, id, 443)
	require.True(t, ok, "a closed port keeps its row")
	assert.Equal(t, "closed", closed.state)
	assert.Equal(t, opened.firstSeen, closed.firstSeen, "first_seen still marks when it was first open")
	assert.Greater(t, closed.changedAt, opened.changedAt, "changed_at moved when it closed")

	assert.Equal(t, []dbtype.EventKind{
		dbtype.EventDeviceDiscovered,
		dbtype.EventAddressAdded,
		dbtype.EventPortOpened,
		dbtype.EventPortOpened,
		dbtype.EventPortClosed,
	}, eventKinds(t, conn, id))
}

func TestRecordPortsLeavesAnUnscannedPortAlone(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	id := deviceIDByMAC(t, conn, macA)

	recordPorts(t, s, portScan("192.0.2.10", []uint16{22, 443}, []uint16{22, 443}))

	// A later run with a narrower set: 443 was not looked at, so nothing is
	// concluded about it.
	sum := recordPorts(t, s, portScan("192.0.2.10", []uint16{22}, []uint16{22}))

	assert.Zero(t, sum.Closed)

	https, ok := devicePort(t, s, id, 443)
	require.True(t, ok)
	assert.Equal(t, "open", https.state)
}

func TestRecordPortsReopensAClosedPort(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	id := deviceIDByMAC(t, conn, macA)

	scanned := []uint16{22}
	recordPorts(t, s, portScan("192.0.2.10", []uint16{22}, scanned))
	firstOpen, _ := devicePort(t, s, id, 22)

	recordPorts(t, s, portScan("192.0.2.10", nil, scanned)) // 22 goes quiet

	sum := recordPorts(t, s, portScan("192.0.2.10", []uint16{22}, scanned)) // and comes back

	assert.Equal(t, 1, sum.Opened)

	reopened, ok := devicePort(t, s, id, 22)
	require.True(t, ok)
	assert.Equal(t, "open", reopened.state)
	assert.Equal(t, firstOpen.firstSeen, reopened.firstSeen, "first_seen is the first time it was ever open")
	assert.Greater(t, reopened.changedAt, firstOpen.changedAt, "changed_at tracks the latest flip")

	assert.Equal(t, 2, queryInt(t, conn,
		`SELECT COUNT(*) FROM events WHERE device_id = ? AND kind = 'PORT_OPENED'`, id))
}

func TestRecordPortsDropsAnAddressNoDeviceHolds(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	sum := recordPorts(t, s, portScan("192.0.2.99", []uint16{22}, []uint16{22}))

	assert.Equal(t, 1, sum.Targets)
	assert.Zero(t, sum.Devices)
	assert.Equal(t, 1, sum.Dropped)
	assert.Zero(t, sum.Open)

	assert.Zero(t, queryInt(t, s.conn, `SELECT COUNT(*) FROM device_ports`))
}

func TestRecordPortsFilesUnderTheSweepSource(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	recordPorts(t, s, portScan("192.0.2.10", []uint16{22}, []uint16{22}))

	// The sweep and the port scan name the same source, so there is one row and
	// both scans point at it.
	assert.Equal(t, 1, queryInt(t, conn, `SELECT COUNT(*) FROM sources WHERE name = 'test-sweep'`))
	assert.Equal(t, 1, queryInt(t, conn, `SELECT COUNT(DISTINCT source_id) FROM scans`))
	assert.Equal(t, "PORTS", queryString(t, conn,
		`SELECT kind FROM scans ORDER BY id DESC LIMIT 1`))
}

func TestRecordPortsAdvancesTheSchedule(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	_, err := s.LastSuccessfulScanAt(t.Context(), dbtype.ScanPorts)
	require.ErrorIs(t, err, ErrNotFound)

	sweep(t, s, host("192.0.2.10", macA, "host-a"))
	recordPorts(t, s, portScan("192.0.2.10", nil, []uint16{22}))

	_, err = s.LastSuccessfulScanAt(t.Context(), dbtype.ScanPorts)
	require.NoError(t, err, "a port scan that found nothing still records a finished scan")
}

func TestRecordPortsEmpty(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	sum := recordPorts(t, s)

	assert.Zero(t, sum.Targets)
	assert.NotZero(t, sum.ScanID)
}

func TestPortScanTargetsSkipsIgnoredDevices(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s,
		host("192.0.2.10", macA, "host-a"),
		host("192.0.2.11", macB, "host-b"),
	)

	_, err := conn.ExecContext(t.Context(),
		`UPDATE devices SET is_ignored = 1 WHERE mac = ?`, macB)
	require.NoError(t, err)

	targets, err := s.PortScanTargets(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.10")}, targets)
}
