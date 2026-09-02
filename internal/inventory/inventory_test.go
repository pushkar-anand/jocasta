package inventory

import (
	"context"
	"database/sql"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/plugin"
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

// host builds a swept host the way a sweep does, so the ingest sees the same
// enrichment here as in production. A malformed argument is a broken test.
func host(ip, mac, hostname string) scanner.Host {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{IP: ip, MAC: mac, Hostname: hostname})
	if err != nil {
		panic(err)
	}

	return scanner.Host{Host: h}
}

func sweep(t *testing.T, s *Store, hosts ...scanner.Host) *Result {
	t.Helper()

	res, err := s.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), hosts)
	require.NoError(t, err)

	return res
}

// fact builds one source's claim about one address the way a plugin does. A
// malformed argument is a broken test.
func fact(ip, mac, hostname string, present bool, standing dbtype.HostnameSource) plugin.Fact {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{IP: ip, MAC: mac, Hostname: hostname})
	if err != nil {
		panic(err)
	}

	return plugin.Fact{Host: h, Present: present, HostnameSource: standing}
}

// report records facts from a source that names no network of its own, the way
// a plugin reading every VLAN at once does.
func report(t *testing.T, s *Store, facts ...plugin.Fact) *Result {
	t.Helper()

	res, err := s.report(t.Context(), reading{source: "test-router", kind: dbtype.SourceRouter, facts: facts})
	require.NoError(t, err)

	return res
}

func queryString(t *testing.T, conn *sql.DB, q string, args ...any) string {
	t.Helper()

	var v sql.NullString
	require.NoError(t, conn.QueryRowContext(t.Context(), q, args...).Scan(&v))

	return v.String
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

	// The locally administered bit is set on this address, so the flag is
	// derived rather than asserted into the host.
	sweep(t, s, host("192.0.2.10", "02:00:5e:00:53:01", ""))

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
		[]scanner.Host{host("192.0.2.10", macA, ""), host("", "", "")})
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

// A source reporting a device it has not seen may say what is already known
// about that device, and may not put a device nothing has ever met into the
// inventory: the device list is what is on the network, and a static lease for
// something unplugged is configuration.
func TestReportDoesNotCreateADeviceItHasNotSeen(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	res := report(t, s, fact("192.0.2.50", macA, "printer", false, dbtype.HostnameFromDHCPStatic))

	assert.Equal(t, 0, res.Seen)
	assert.Equal(t, 0, res.Discovered)
	assert.Equal(t, 0, queryInt(t, conn, `SELECT COUNT(*) FROM devices`))
	assert.Equal(t, 0, queryInt(t, conn, `SELECT COUNT(*) FROM addresses`))
}

// An address a source could not resolve names nothing at all: it identifies no
// device and does not say one is answering.
func TestReportRecordsNothingForAnAddressItCannotResolve(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	res := report(t, s, fact("192.0.2.51", "", "", false, ""))

	assert.Equal(t, 0, res.Seen)
	assert.Equal(t, 0, queryInt(t, conn, `SELECT COUNT(*) FROM devices`))
}

// The same fact against a device already known is worth recording: the source
// knows its name, which is the half a sweep cannot supply.
func TestReportNamesAKnownDeviceItHasNotSeen(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	id := deviceIDByMAC(t, conn, macA)
	seen := queryString(t, conn, `SELECT last_seen FROM devices WHERE id = ?`, id)

	res := report(t, s, fact("192.0.2.50", macA, "printer", false, dbtype.HostnameFromDHCPStatic))

	assert.Equal(t, 1, res.Seen)
	assert.Equal(t, "printer", queryString(t, conn, `SELECT hostname FROM devices WHERE id = ?`, id))

	// Naming a device is not seeing it, and the address it was named at is not
	// one it is holding.
	assert.Equal(t, seen, queryString(t, conn, `SELECT last_seen FROM devices WHERE id = ?`, id),
		"a source that did not see the device must not mark it seen")
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, id))
}

// The standing a source claims for a name is stored as given. Recording every
// name as DNS would leave nothing to weigh two sources by.
func TestReportKeepsTheStandingOfTheNameItCarries(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, string(dbtype.HostnameFromDHCPStatic),
		queryString(t, conn, `SELECT hostname_source FROM devices WHERE id = ?`, id))
}

// A source that has not seen a device must not take an address from whatever is
// answering on it: a stale lease pointing at a reused address would otherwise
// move it to a device that is not there.
func TestReportDoesNotTakeAnAddressFromItsHolder(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	holder := deviceIDByMAC(t, conn, macA)
	other := deviceIDByMAC(t, conn, macB)

	report(t, s, fact("192.0.2.10", macB, "", false, ""))

	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, holder))
	assert.Equal(t, []string{"192.0.2.11"}, currentIPs(t, conn, other))
}

// A source covering many networks names none of them, so each address is
// matched to the network holding it rather than to one carried for the reading.
func TestReportMatchesEachAddressToItsOwnNetwork(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	// Two sweeps, so both prefixes are recorded.
	sweep(t, s, host("192.0.2.10", macA, ""))

	_, err := s.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix("198.51.100.0/24"),
		[]scanner.Host{host("198.51.100.10", macB, "")})
	require.NoError(t, err)

	report(t, s,
		fact("192.0.2.20", "00:00:5e:00:53:03", "", true, ""),
		fact("198.51.100.20", "00:00:5e:00:53:04", "", true, ""),
	)

	assert.Equal(t, "192.0.2.0/24", queryString(t, conn,
		`SELECT n.cidr FROM addresses a JOIN networks n ON n.id = a.network_id WHERE a.ip = ?`, "192.0.2.20"))
	assert.Equal(t, "198.51.100.0/24", queryString(t, conn,
		`SELECT n.cidr FROM addresses a JOIN networks n ON n.id = a.network_id WHERE a.ip = ?`, "198.51.100.20"))
}

// An address on no recorded network is still recorded, without one.
func TestReportRecordsAnAddressOnNoKnownNetwork(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	report(t, s, fact("203.0.113.10", macA, "", true, ""))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, []string{"203.0.113.10"}, currentIPs(t, conn, id))
	assert.Equal(t, 1, queryInt(t, conn,
		`SELECT COUNT(*) FROM addresses WHERE ip = ? AND network_id IS NULL`, "203.0.113.10"))
}

// RefreshAddress coalesces rather than assigns, so a source that cannot say
// which network an address is on leaves the one a sweep established. The store
// path cannot reach this today -- an address is matched against every recorded
// network, so a prefix a sweep recorded still matches -- which is why the guard
// is asserted against the query itself.
func TestRefreshAddressKeepsTheNetworkASweepEstablished(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))

	var id int64
	require.NoError(t, conn.QueryRowContext(t.Context(),
		`SELECT id FROM addresses WHERE ip = ?`, "192.0.2.10").Scan(&id))

	err := s.q.RefreshAddress(t.Context(), models.RefreshAddressParams{
		NetworkID: sql.NullInt64{},
		LastSeen:  s.stamp(),
		ID:        id,
	})
	require.NoError(t, err)

	assert.Equal(t, "192.0.2.0/24", queryString(t, conn,
		`SELECT n.cidr FROM addresses a JOIN networks n ON n.id = a.network_id WHERE a.id = ?`, id))
}

// claimOf reads what one source says about a device, by the source's name.
func claimOf(t *testing.T, conn *sql.DB, deviceID int64, source string) (name, standing string) {
	t.Helper()

	var n, st sql.NullString

	err := conn.QueryRowContext(t.Context(),
		`SELECT ds.hostname, ds.hostname_source
		 FROM device_sources ds JOIN sources s ON s.id = ds.source_id
		 WHERE ds.device_id = ? AND s.name = ?`, deviceID, source,
	).Scan(&n, &st)
	require.NoError(t, err)

	return n.String, st.String
}

func TestEachSourceKeepsItsOwnClaim(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))
	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))

	id := deviceIDByMAC(t, conn, macA)

	name, standing := claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "host-a.example.com", name)
	assert.Equal(t, string(dbtype.HostnameFromDNS), standing)

	name, standing = claimOf(t, conn, id, "test-router")
	assert.Equal(t, "printer", name)
	assert.Equal(t, string(dbtype.HostnameFromDHCPStatic), standing)

	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM device_sources WHERE device_id = ?`, id))
}

// The name the list shows is elected from every claim, so a lease name does not
// displace the one that resolves just by being written later.
func TestElectedNamePrefersTheNameThatResolves(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))
	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))

	assert.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))
	assert.Equal(t, string(dbtype.HostnameFromDNS), queryString(t, conn, `SELECT hostname_source FROM devices`))
}

// The row takes the fuller spelling, but an operator is not told the device was
// renamed when only the domain arrived.
func TestTwoSpellingsOfOneNameAreNotARename(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	report(t, s, fact("192.0.2.10", macA, "host-a", true, dbtype.HostnameFromDHCPLease))

	id := deviceIDByMAC(t, conn, macA)
	require.Equal(t, "host-a", queryString(t, conn, `SELECT hostname FROM devices`))

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))

	assert.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventHostnameChanged)
}

// A source that stops reporting a name writes an empty claim over its old one,
// which is what lets the runner-up be elected instead.
func TestARetractedNameFallsBackToTheRunnerUp(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))
	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))

	id := deviceIDByMAC(t, conn, macA)
	require.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))

	// The resolver has no record for the address any more.
	sweep(t, s, host("192.0.2.10", macA, ""))

	name, standing := claimOf(t, conn, id, "test-sweep")
	assert.Empty(t, name)
	assert.Empty(t, standing)

	assert.Equal(t, "printer", queryString(t, conn, `SELECT hostname FROM devices`))
	assert.Equal(t, string(dbtype.HostnameFromDHCPStatic), queryString(t, conn, `SELECT hostname_source FROM devices`))
}

// Every source retracting leaves the device nameless rather than holding a name
// nothing claims any more.
func TestADeviceCanLoseItsNameEntirely(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))
	sweep(t, s, host("192.0.2.10", macA, ""))

	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM devices WHERE hostname IS NULL`))
	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM devices WHERE hostname_source IS NULL`))
}

func TestFoldCarriesClaimsToTheSurvivingDevice(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	report(t, s, fact("192.0.2.20", "", "printer", true, dbtype.HostnameFromDHCPStatic))

	var ghost int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT id FROM devices WHERE mac IS NULL`).Scan(&ghost))

	res := sweep(t, s, host("192.0.2.20", macA, ""))
	require.Equal(t, 1, res.Merged)

	id := deviceIDByMAC(t, conn, macA)

	name, standing := claimOf(t, conn, id, "test-router")
	assert.Equal(t, "printer", name)
	assert.Equal(t, string(dbtype.HostnameFromDHCPStatic), standing)

	assert.Zero(t, queryInt(t, conn, `SELECT count(*) FROM device_sources WHERE device_id = ?`, ghost))

	// The claim it kept is the one the surviving row is now named by.
	assert.Equal(t, "printer", queryString(t, conn, `SELECT hostname FROM devices`))
}

// A source that had filed against both rows collides on the primary key, so the
// claims merge instead of one of them being dropped. The fold is driven by a
// different source here, or this source's own upsert would mask the merge.
func TestFoldMergesTwoClaimsFromOneSource(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "first.example.com"))
	sweep(t, s, host("192.0.2.20", "", "second.example.com"))

	var ghost int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT id FROM devices WHERE mac IS NULL`).Scan(&ghost))

	firstSeen := queryString(t, conn,
		`SELECT first_seen FROM device_sources WHERE device_id = ?`, deviceIDByMAC(t, conn, macA))

	res := report(t, s, fact("192.0.2.20", macA, "printer", true, dbtype.HostnameFromDHCPStatic))
	require.Equal(t, 1, res.Merged)

	id := deviceIDByMAC(t, conn, macA)

	assert.Zero(t, queryInt(t, conn, `SELECT count(*) FROM device_sources WHERE device_id = ?`, ghost))
	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM device_sources WHERE device_id = ?`, id))

	// The newer of the two readings names the merged claim.
	name, _ := claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "second.example.com", name)

	// And the earlier of the two sightings still bounds it.
	assert.Equal(t, firstSeen,
		queryString(t, conn, `SELECT first_seen FROM device_sources ds JOIN sources s ON s.id = ds.source_id
		                      WHERE ds.device_id = ? AND s.name = 'test-sweep'`, id))
}

// Detail is stored verbatim for the device page and never merged into devices.
func TestAClaimStoresWhatOnlyItsSourceKnows(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	f := fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic)
	f.Detail = map[string]string{"interface": "vlan10", "dhcp_comment": "lab printer"}

	report(t, s, f)

	id := deviceIDByMAC(t, conn, macA)
	assert.JSONEq(t, `{"interface":"vlan10","dhcp_comment":"lab printer"}`,
		queryString(t, conn, `SELECT detail FROM device_sources WHERE device_id = ?`, id))
}

func TestAClaimWithoutDetailStoresNull(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	report(t, s, fact("192.0.2.10", macA, "", true, ""))

	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM device_sources WHERE detail IS NULL`))
}

// A fact nothing can be recorded against is counted rather than silently lost.
func TestReportCountsWhatItDropped(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	res := report(t, s, fact("192.0.2.10", "", "printer", false, dbtype.HostnameFromDHCPStatic))

	assert.Equal(t, 1, res.Dropped)
	assert.Zero(t, res.Seen)
	assert.Zero(t, res.Discovered)
}

// RecordFacts is the exported form the plugin path uses; a sweep reaches the
// same ingest through RecordSweep.
func TestRecordFactsFilesUnderTheNamedSource(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	res, err := s.RecordFacts(t.Context(), "routeros:gateway", dbtype.SourceRouter,
		[]plugin.Fact{fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic)})
	require.NoError(t, err)
	require.Equal(t, 1, res.Seen)

	id := deviceIDByMAC(t, conn, macA)

	name, _ := claimOf(t, conn, id, "routeros:gateway")
	assert.Equal(t, "printer", name)
	assert.Equal(t, string(dbtype.SourceRouter),
		queryString(t, conn, `SELECT kind FROM sources WHERE name = 'routeros:gateway'`))
}

func TestDeviceSourcesReportsEveryClaim(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.example.com"))

	f := fact("192.0.2.10", macA, "lab-printer", true, dbtype.HostnameFromDHCPStatic)
	f.Detail = map[string]string{"interface": "vlan10", "dhcp_comment": "bench unit"}
	report(t, s, f)

	claims, err := s.DeviceSources(t.Context(), deviceIDByMAC(t, conn, macA))
	require.NoError(t, err)
	require.Len(t, claims, 2)

	byName := map[string]*Claim{}
	for _, c := range claims {
		byName[c.Source] = c
	}

	swept := byName["test-sweep"]
	require.NotNil(t, swept)
	assert.Equal(t, "printer.example.com", swept.Hostname)
	assert.Equal(t, dbtype.HostnameFromDNS, swept.HostnameSource)
	assert.Equal(t, dbtype.SourceSweep, swept.Kind)
	assert.Empty(t, swept.Detail)

	router := byName["test-router"]
	require.NotNil(t, router)
	assert.Equal(t, "lab-printer", router.Hostname)
	assert.Equal(t, dbtype.HostnameFromDHCPStatic, router.HostnameSource)
	assert.Equal(t, dbtype.SourceRouter, router.Kind)

	// Detail is sorted by key, so a page renders it the same way twice.
	assert.Equal(t, []Field{
		{Key: "dhcp_comment", Value: "bench unit"},
		{Key: "interface", Value: "vlan10"},
	}, router.Detail)
}

// Detail that will not decode costs the page that aside, not the whole device.
func TestDeviceSourcesSurvivesUnreadableDetail(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))

	id := deviceIDByMAC(t, conn, macA)

	_, err := conn.ExecContext(t.Context(),
		`UPDATE device_sources SET detail = '[1,2,3]' WHERE device_id = ?`, id)
	require.NoError(t, err)

	claims, err := s.DeviceSources(t.Context(), id)
	require.NoError(t, err)
	require.Len(t, claims, 1)

	assert.Empty(t, claims[0].Detail)
	assert.Equal(t, "printer", claims[0].Hostname)
}

func TestDeviceSourcesOfAnUnknownDeviceIsEmpty(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	claims, err := s.DeviceSources(t.Context(), 999)
	require.NoError(t, err)
	assert.Empty(t, claims)
}
