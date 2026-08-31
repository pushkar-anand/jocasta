package inventory

import (
	"database/sql"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Addresses come from RFC 5737 and hardware addresses from RFC 7042, both
// reserved for documentation, so nothing here names a real device.
const (
	prefix = "192.0.2.0/24"

	macA = "00:00:5e:00:53:01"
	macB = "00:00:5e:00:53:02"
)

// newStore opens an empty inventory with a clock that advances a second per
// call, so rows written by different sweeps are ordered and comparable.
func newStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	d, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = d.Conn.Close() })

	s := New(d.Conn, slog.New(slog.DiscardHandler))

	tick := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	return s, d.Conn
}

func host(ip, mac, hostname string) scanner.Host {
	return scanner.Host{
		Addr:     netip.MustParseAddr(ip),
		MAC:      mac,
		Hostname: hostname,
	}
}

func sweep(t *testing.T, s *Store, hosts ...scanner.Host) *Result {
	t.Helper()

	res, err := s.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), hosts)
	require.NoError(t, err)

	return res
}

func queryInt(t *testing.T, conn *sql.DB, q string, args ...any) int {
	t.Helper()

	var n int
	require.NoError(t, conn.QueryRowContext(t.Context(), q, args...).Scan(&n))

	return n
}

func queryStrings(t *testing.T, conn *sql.DB, q string, args ...any) []string {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), q, args...)
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

func deviceIDByMAC(t *testing.T, conn *sql.DB, mac string) int64 {
	t.Helper()

	var id int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT id FROM devices WHERE mac = ?`, mac).Scan(&id))

	return id
}

func currentIPs(t *testing.T, conn *sql.DB, deviceID int64) []string {
	t.Helper()

	return queryStrings(t, conn,
		`SELECT ip FROM addresses WHERE device_id = ? AND is_current = 1 ORDER BY ip`, deviceID)
}

func eventKinds(t *testing.T, conn *sql.DB, deviceID int64) []dbtype.EventKind {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), `SELECT kind FROM events WHERE device_id = ? ORDER BY id`, deviceID)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var kinds []dbtype.EventKind

	for rows.Next() {
		var k dbtype.EventKind

		require.NoError(t, rows.Scan(&k))

		kinds = append(kinds, k)
	}

	require.NoError(t, rows.Err())

	return kinds
}

func TestRecordSweepDiscovers(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	res := sweep(t, s,
		host("192.0.2.10", macA, "one.example"),
		host("192.0.2.11", "", ""),
	)

	assert.Equal(t, 2, res.Discovered)
	assert.Equal(t, 2, res.Seen)
	assert.Zero(t, res.Identified)
	assert.Zero(t, res.Merged)

	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM addresses WHERE is_current = 1`))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, id))
	assert.Equal(t, []dbtype.EventKind{dbtype.EventDeviceDiscovered, dbtype.EventAddressAdded}, eventKinds(t, conn, id))

	// The unidentified host is a device too, just a weaker one.
	assert.Equal(t, 1, queryInt(t, conn,
		`SELECT count(*) FROM devices WHERE mac IS NULL AND identity_source = 'IP'`))

	assert.Equal(t, string(dbtype.StatusOK), queryStrings(t, conn, `SELECT status FROM scans`)[0])
	assert.Equal(t, 2, queryInt(t, conn, `SELECT found_count FROM scans`))
}

func TestRecordSweepStoresHostname(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "one.example"))

	var (
		name   string
		source dbtype.HostnameSource
	)

	err := conn.QueryRowContext(t.Context(), `SELECT hostname, hostname_source FROM devices`).Scan(&name, &source)
	require.NoError(t, err)

	assert.Equal(t, "one.example", name)
	assert.Equal(t, dbtype.HostnameFromDNS, source)
}

// A repeated sweep of an unchanged network must not grow the inventory.
func TestRecordSweepIsIdempotent(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	h := host("192.0.2.10", macA, "one.example")

	sweep(t, s, h)
	res := sweep(t, s, h)

	assert.Zero(t, res.Discovered)
	assert.Zero(t, res.Merged)
	assert.Equal(t, 1, res.Seen)

	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM addresses`))
	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM events`))
	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM scans`))
}

// An address seen before its hardware address must become the identified
// device rather than a second one beside it.
func TestRecordSweepIdentifiesLateMAC(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", "", "one.example"))

	var before int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT id FROM devices`).Scan(&before))

	res := sweep(t, s, host("192.0.2.10", macA, "one.example"))

	assert.Equal(t, 1, res.Identified)
	assert.Zero(t, res.Discovered)
	assert.Zero(t, res.Merged)

	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, before, deviceIDByMAC(t, conn, macA))

	assert.Equal(t, string(dbtype.IdentityMAC), queryStrings(t, conn, `SELECT identity_source FROM devices`)[0])
	assert.Contains(t, eventKinds(t, conn, before), dbtype.EventDeviceIdentified)
}

// A device already known by its hardware address on one address, met again on
// a second where a weaker row had been standing in for it, absorbs that row.
func TestRecordSweepFoldsDuplicate(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	sweep(t, s, host("192.0.2.20", "", ""))

	// Curation applied before anyone knew the two rows were one device.
	_, err := conn.ExecContext(t.Context(), `UPDATE devices SET label = 'printer' WHERE mac IS NULL`)
	require.NoError(t, err)

	var ghost int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT id FROM devices WHERE mac IS NULL`).Scan(&ghost))

	res := sweep(t, s, host("192.0.2.20", macA, ""))

	assert.Equal(t, 1, res.Merged)
	assert.Zero(t, res.Discovered)

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, []string{"192.0.2.10", "192.0.2.20"}, currentIPs(t, conn, id))

	assert.Equal(t, "printer", queryStrings(t, conn, `SELECT label FROM devices`)[0])
	assert.Contains(t, eventKinds(t, conn, id), dbtype.EventDevicesMerged)
	assertNoRowSeenBeforeItExisted(t, conn)

	// The folded row's own history follows it rather than being orphaned.
	assert.Zero(t, queryInt(t, conn, `SELECT count(*) FROM events WHERE device_id = ?`, ghost))
	assert.Zero(t, queryInt(t, conn, `SELECT count(*) FROM events WHERE device_id IS NULL`))
}

// A lease handed to another device moves the address rather than duplicating
// it: only one device may hold an address as current.
func TestRecordSweepMovesAddressBetweenDevices(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	sweep(t, s, host("192.0.2.10", macB, ""))

	first := deviceIDByMAC(t, conn, macA)
	second := deviceIDByMAC(t, conn, macB)

	assert.Empty(t, currentIPs(t, conn, first))
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, second))

	// The old holder keeps the row, so where it used to live is still readable.
	assert.Equal(t, 1, queryInt(t, conn,
		`SELECT count(*) FROM addresses WHERE device_id = ? AND is_current = 0`, first))
}

func TestRecordSweepRecordsHostnameChange(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "one.example"))
	sweep(t, s, host("192.0.2.10", macA, "two.example"))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, "two.example", queryStrings(t, conn, `SELECT hostname FROM devices`)[0])
	assert.Contains(t, eventKinds(t, conn, id), dbtype.EventHostnameChanged)

	var from, to string

	err := conn.QueryRowContext(t.Context(),
		`SELECT old_value, new_value FROM events WHERE kind = ?`, dbtype.EventHostnameChanged,
	).Scan(&from, &to)
	require.NoError(t, err)

	assert.Equal(t, "one.example", from)
	assert.Equal(t, "two.example", to)
}

func TestRecordSweepMarksRandomisedAddress(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	h := host("192.0.2.10", "02:00:5e:00:53:01", "")
	h.Randomised = true

	sweep(t, s, h)

	assert.Equal(t, 1, queryInt(t, conn, `SELECT is_randomised FROM devices`))
}

func TestRecordSweepEmptyIsNotAnError(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	res := sweep(t, s)

	assert.Zero(t, res.Seen)
	assert.NotZero(t, res.ScanID)
	assert.Equal(t, string(dbtype.StatusOK), queryStrings(t, conn, `SELECT status FROM scans`)[0])
	assert.Zero(t, queryInt(t, conn, `SELECT count(*) FROM devices`))
}

// Inserts take their timestamps from Go and updates used to take theirs from
// SQLite's own clock, which stamped a device as last seen before it was first
// seen. Every row a sweep writes carries the one timestamp for that sweep.
func TestRecordSweepTimestampsAreOrdered(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))

	var first, last string

	err := conn.QueryRowContext(t.Context(), `SELECT first_seen, last_seen FROM devices`).Scan(&first, &last)
	require.NoError(t, err)
	assert.Equal(t, first, last, "a device discovered by a sweep was not first and last seen at once")

	err = conn.QueryRowContext(t.Context(), `SELECT first_seen, last_seen FROM addresses`).Scan(&first, &last)
	require.NoError(t, err)
	assert.Equal(t, first, last)

	// A sweep can open and close inside one second, so the invariant is that
	// it does not finish before it started, not that the two differ.
	var started, finished string

	err = conn.QueryRowContext(t.Context(), `SELECT started_at, finished_at FROM scans`).Scan(&started, &finished)
	require.NoError(t, err)
	assert.LessOrEqual(t, started, finished)

	// A later sweep moves last_seen forward and leaves first_seen where it was.
	sweep(t, s, host("192.0.2.10", macA, ""))

	var secondFirst, secondLast string

	err = conn.QueryRowContext(t.Context(), `SELECT first_seen, last_seen FROM devices`).Scan(&secondFirst, &secondLast)
	require.NoError(t, err)

	assert.Equal(t, first, secondFirst, "first_seen moved")
	assert.Less(t, secondFirst, secondLast)

	assertNoRowSeenBeforeItExisted(t, conn)
}

// assertNoRowSeenBeforeItExisted checks the invariant across every row rather
// than the one the calling test happened to look at.
func assertNoRowSeenBeforeItExisted(t *testing.T, conn *sql.DB) {
	t.Helper()

	for _, table := range []string{"devices", "addresses"} {
		n := queryInt(t, conn, `SELECT count(*) FROM `+table+` WHERE last_seen < first_seen`)
		assert.Zerof(t, n, "%s rows last seen before they were first seen", table)
	}

	n := queryInt(t, conn, `SELECT count(*) FROM scans WHERE finished_at < started_at`)
	assert.Zero(t, n, "scans finished before they started")
}

// A sweep that cannot be stored still has to leave a record that it was tried,
// which is the whole reason the scan row is closed outside the ingest.
func TestRecordSweepRecordsAFailedIngest(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	// The zero address is refused on the way to the column, so the ingest gives
	// up partway and rolls back the host ahead of it.
	res, err := s.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, ""), {}})
	require.Error(t, err)
	assert.Nil(t, res, "a failed sweep returns no result to read")

	assert.Equal(t, string(dbtype.StatusFailed),
		queryStrings(t, conn, `SELECT status FROM scans`)[0])
	assert.NotEmpty(t, queryStrings(t, conn, `SELECT error FROM scans`)[0])

	// Nothing the ingest counted survived it, so the scan must not claim any.
	assert.Equal(t, 0, queryInt(t, conn, `SELECT found_count FROM scans`))
	assert.Equal(t, 0, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, 0, queryInt(t, conn, `SELECT count(*) FROM events`))
}
