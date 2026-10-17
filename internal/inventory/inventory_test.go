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

// Addresses come from RFC 5737, and hardware addresses from RFC 7042. Both are
// for documentation. So nothing here names a real device.
const (
	prefix = "192.0.2.0/24"

	macA = "00:00:5e:00:53:01"
	macB = "00:00:5e:00:53:02"
)

// newStore opens an empty inventory with a clock that moves one second on each
// call. So rows from different sweeps have an order and compare.
func newStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	d, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = d.Close() })

	s := New(d, slog.New(slog.DiscardHandler))

	tick := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	return s, d
}

// clockStore is newStore with a clock that the test can move forward. Use it to
// assert behaviour that depends on the time between two sweeps. Address
// retirement is the main example.
func clockStore(t *testing.T) (*Store, *sql.DB, func(time.Duration)) {
	t.Helper()

	d, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = d.Close() })

	s := New(d, slog.New(slog.DiscardHandler))

	tick := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}

	return s, d, func(by time.Duration) { tick = tick.Add(by) }
}

// sweepPrefix records a sweep of a named prefix, for the retirement tests that
// need more than one prefix.
func sweepPrefix(t *testing.T, s *Store, cidr string, hosts ...scanner.Host) *Result {
	t.Helper()

	res, err := s.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(cidr), hosts)
	require.NoError(t, err)

	return res
}

// host builds a swept host as a sweep does. So the ingest sees the same
// enrichment here as in production. A malformed argument means a broken
// test.
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

// fact builds the claim of one source about one address, as a plugin does. A
// malformed argument means a broken test.
func fact(ip, mac, hostname string, present bool, standing dbtype.HostnameSource) plugin.Fact {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{IP: ip, MAC: mac, Hostname: hostname})
	if err != nil {
		panic(err)
	}

	return plugin.Fact{Host: h, Present: present, HostnameSource: standing}
}

// report records facts from a source that names no network, as a plugin that
// reads every VLAN at once does.
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
	assert.ElementsMatch(t, []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("192.0.2.11")}, res.New)
	assert.Equal(t, 2, res.Seen)
	assert.Zero(t, res.Identified)
	assert.Zero(t, res.Merged)

	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM addresses WHERE is_current = 1`))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, id))
	assert.Equal(t, []dbtype.EventKind{dbtype.EventDeviceDiscovered, dbtype.EventAddressAdded}, eventKinds(t, conn, id))

	// The unidentified host is also a device, but a weaker one.
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

// A second sweep of an unchanged network must not make the inventory larger.
func TestRecordSweepIsIdempotent(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	h := host("192.0.2.10", macA, "one.example")

	sweep(t, s, h)
	res := sweep(t, s, h)

	assert.Zero(t, res.Discovered)
	assert.Empty(t, res.New)
	assert.Zero(t, res.Merged)
	assert.Equal(t, 1, res.Seen)

	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM addresses`))
	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM events`))
	assert.Equal(t, 2, queryInt(t, conn, `SELECT count(*) FROM scans`))
}

// An address seen before its hardware address must become the identified
// device. A second row next to it would count the device twice.
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

// A sighting records the current vendor from the OUI table. So a device
// recorded before a table refresh, or before the store kept the registered
// name, gets the current vendor. A sighting with no hardware address has no
// vendor, and changes neither.
func TestRecordSweepRefreshesTheVendor(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))

	_, err := conn.ExecContext(t.Context(), `UPDATE devices SET vendor = NULL, vendor_short = 'Stale'`)
	require.NoError(t, err)

	sweep(t, s, host("192.0.2.10", macA, ""))

	vendor := `SELECT vendor || '|' || vendor_short FROM devices`
	assert.Equal(t, "ICANN, IANA Department|ICANNIANADep", queryString(t, conn, vendor))

	sweep(t, s, host("192.0.2.10", "", ""))

	assert.Equal(t, "ICANN, IANA Department|ICANNIANADep", queryString(t, conn, vendor))
}

// A device is known by its hardware address on one address. A sweep then finds
// it on a second address, where a weaker row stood for it. The device takes
// that row.
func TestRecordSweepFoldsDuplicate(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	sweep(t, s, host("192.0.2.20", "", ""))

	// Curation from before anybody knew that the two rows were one device.
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

	// The history of the merged row goes with it.
	assert.Zero(t, queryInt(t, conn, `SELECT count(*) FROM events WHERE device_id = ?`, ghost))
	assert.Zero(t, queryInt(t, conn, `SELECT count(*) FROM events WHERE device_id IS NULL`))
}

// Every field that the owner can set on the weaker row stays after the merge.
// If both rows have a type, the identified device keeps its own type.
func TestRecordSweepFoldKeepsEveryCurationField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		twinType string
		want     string
	}{
		{name: "the weaker row's type carries over", want: "nas"},
		{name: "the identified device's own type wins", twinType: "server", want: "server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, conn := newStore(t)

			sweep(t, s, host("192.0.2.10", macA, ""))
			sweep(t, s, host("192.0.2.20", "", ""))

			_, err := conn.ExecContext(t.Context(),
				`UPDATE devices SET device_type = 'nas', is_ignored = 1, is_watched = 1 WHERE mac IS NULL`)
			require.NoError(t, err)

			if tt.twinType != "" {
				curate(t, conn, deviceIDByMAC(t, conn, macA), "device_type", tt.twinType)
			}

			sweep(t, s, host("192.0.2.20", macA, ""))

			id := deviceIDByMAC(t, conn, macA)
			assert.Equal(t, tt.want, queryString(t, conn, `SELECT device_type FROM devices WHERE id = ?`, id))
			assert.Equal(t, 1, queryInt(t, conn, `SELECT is_ignored FROM devices WHERE id = ?`, id))
			assert.Equal(t, 1, queryInt(t, conn, `SELECT is_watched FROM devices WHERE id = ?`, id))
		})
	}
}

// A watched weaker row that merges into a quiet device does not bring that
// device back. Sweeps saw the weaker row all the time. So the present run of
// the device is the run that the weaker row began.
func TestRecordSweepFoldOfAWatchedRowIsNotABack(t *testing.T) {
	t.Parallel()

	s, conn, advance := clockStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))

	advance(2 * DefaultOnlineWindow)

	sweep(t, s, host("192.0.2.20", "", ""))

	var ghost int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT id FROM devices WHERE mac IS NULL`).Scan(&ghost))

	curate(t, conn, ghost, "is_watched", 1)
	since := presentSince(t, conn, ghost)

	res := sweep(t, s, host("192.0.2.20", macA, ""))

	id := deviceIDByMAC(t, conn, macA)
	assert.Zero(t, res.Back)
	assert.Equal(t, since, presentSince(t, conn, id))
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventDeviceBack)
}

// What was recorded against the weaker row goes with it into the identified
// device. If both rows have the same key, the counts add up, and the newer
// reading of a port or service stays. The rows of other devices that name the
// weaker row as their peer then name the identified device.
func TestRecordSweepFoldMovesWhatWasRecordedAgainstTheWeakerRow(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.30", macB, ""))
	sweep(t, s, host("192.0.2.20", "", ""))

	var ghost int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT id FROM devices WHERE mac IS NULL`).Scan(&ghost))

	twin := deviceIDByMAC(t, conn, macA)
	other := deviceIDByMAC(t, conn, macB)
	source := queryInt(t, conn, `SELECT id FROM sources WHERE name = 'test-sweep'`)

	const (
		hour  = "2026-01-01T00:00:00.000Z"
		early = "2026-01-01T00:00:01.000Z"
		mid   = "2026-01-01T00:00:02.000Z"
		late  = "2026-01-01T00:00:03.000Z"
	)

	_, err := conn.ExecContext(t.Context(), `
		INSERT INTO device_ports (device_id, port, state, service, first_seen, last_seen, changed_at)
		VALUES (:ghost, 22, 'open', 'ssh', :early, :late, :early),
		       (:ghost, 80, 'closed', 'http', :mid, :late, :late),
		       (:twin, 80, 'open', 'http', :early, :mid, :early),
		       (:ghost, 443, 'closed', 'https', :early, :mid, :mid),
		       (:twin, 443, 'open', 'https', :mid, :late, :mid);

		INSERT INTO device_services (device_id, type, instance, port, first_seen, last_seen)
		VALUES (:ghost, '_http._tcp', '', 80, :early, :late),
		       (:ghost, '_ipp._tcp', 'printer-a', 631, :mid, :late),
		       (:twin, '_ipp._tcp', 'printer-a', 0, :early, :mid);

		INSERT INTO traffic_hourly (source_id, device_id, hour, peer_device_id, peer_ip, protocol, service_port,
		                            bytes_out, bytes_in, packets_out, packets_in, connections, connections_in)
		VALUES (:source, :ghost, :hour, NULL, '203.0.113.5', 6, 443, 100, 200, 1, 2, 1, 0),
		       (:source, :twin, :hour, NULL, '203.0.113.5', 6, 443, 10, 20, 1, 1, 1, 1),
		       (:source, :ghost, :hour, NULL, '203.0.113.5', 6, 80, 5, 5, 1, 1, 1, 0),
		       (:source, :other, :hour, :ghost, '192.0.2.20', 6, 22, 7, 7, 1, 1, 1, 0);

		INSERT INTO attempts_hourly (source_id, device_id, hour, peer_device_id, peer_ip, protocol,
		                             attempts, answered, port_count, ports)
		VALUES (:source, :ghost, :hour, NULL, '198.51.100.7', 6, 5, 1, 3, '22,23,80'),
		       (:source, :twin, :hour, NULL, '198.51.100.7', 6, 2, 0, 1, '22'),
		       (:source, :other, :hour, :ghost, '192.0.2.20', 6, 1, 0, 1, '23');

		INSERT INTO probes_hourly (source_id, device_id, hour, peer_ip, protocol,
		                           attempts, answered, port_count, ports, outside)
		VALUES (:source, :ghost, :hour, '198.51.100.9', 6, 4, 0, 2, '22,3389', 1),
		       (:source, :twin, :hour, '198.51.100.9', 6, 1, 1, 1, '22', 0);

		INSERT INTO broadcasts_hourly (source_id, device_id, hour, dst_ip, kind, protocol, port, bytes, packets)
		VALUES (:source, :ghost, :hour, '192.0.2.255', 'subnet', 17, 137, 50, 1),
		       (:source, :twin, :hour, '192.0.2.255', 'subnet', 17, 137, 25, 1);

		INSERT INTO dns_hourly (source_id, device_id, hour, name, lookups, blocked, failed)
		VALUES (:source, :ghost, :hour, 'example.com', 4, 1, 0),
		       (:source, :twin, :hour, 'example.com', 2, 0, 1),
		       (:source, :ghost, :hour, 'example.org', 3, 0, 0);

		INSERT INTO dns_answers (device_id, addr, name, lookups, last_hour)
		VALUES (:ghost, '203.0.113.1', 'example.com', 4, :late),
		       (:twin, '203.0.113.1', 'example.com', 2, :early),
		       (:ghost, '203.0.113.2', 'example.org', 3, :mid);`,
		sql.Named("ghost", ghost), sql.Named("twin", twin), sql.Named("other", other),
		sql.Named("source", source), sql.Named("hour", hour),
		sql.Named("early", early), sql.Named("mid", mid), sql.Named("late", late))
	require.NoError(t, err)

	sweep(t, s, host("192.0.2.20", macA, ""))

	require.Equal(t, twin, deviceIDByMAC(t, conn, macA))

	t.Run("ports", func(t *testing.T) {
		assert.Equal(t, []string{
			"22 open " + early + " " + late,
			"80 closed " + early + " " + late,
			"443 open " + early + " " + late,
		}, queryStrings(t, conn, `
			SELECT port || ' ' || state || ' ' || first_seen || ' ' || last_seen
			FROM device_ports WHERE device_id = ? ORDER BY port`, twin))
	})

	t.Run("services", func(t *testing.T) {
		assert.Equal(t, []string{
			"_http._tcp 80 " + early + " " + late,
			"_ipp._tcp 631 " + early + " " + late,
		}, queryStrings(t, conn, `
			SELECT type || ' ' || port || ' ' || first_seen || ' ' || last_seen
			FROM device_services WHERE device_id = ? ORDER BY type`, twin))
	})

	t.Run("traffic", func(t *testing.T) {
		assert.Equal(t, []string{
			"80 5 5 1 1 1 0",
			"443 110 220 2 3 2 1",
		}, queryStrings(t, conn, `
			SELECT service_port || ' ' || bytes_out || ' ' || bytes_in || ' ' || packets_out || ' ' ||
			       packets_in || ' ' || connections || ' ' || connections_in
			FROM traffic_hourly WHERE device_id = ? ORDER BY service_port`, twin))
	})

	t.Run("attempts", func(t *testing.T) {
		assert.Equal(t, []string{"7 1 3 22,23,80"}, queryStrings(t, conn, `
			SELECT attempts || ' ' || answered || ' ' || port_count || ' ' || ports
			FROM attempts_hourly WHERE device_id = ?`, twin))
	})

	t.Run("probes", func(t *testing.T) {
		assert.Equal(t, []string{"5 1 2 22,3389 1"}, queryStrings(t, conn, `
			SELECT attempts || ' ' || answered || ' ' || port_count || ' ' || ports || ' ' || outside
			FROM probes_hourly WHERE device_id = ?`, twin))
	})

	t.Run("broadcasts", func(t *testing.T) {
		assert.Equal(t, []string{"75 2"}, queryStrings(t, conn, `
			SELECT bytes || ' ' || packets FROM broadcasts_hourly WHERE device_id = ?`, twin))
	})

	t.Run("lookups", func(t *testing.T) {
		assert.Equal(t, []string{"example.com 6 1 1", "example.org 3 0 0"}, queryStrings(t, conn, `
			SELECT name || ' ' || lookups || ' ' || blocked || ' ' || failed
			FROM dns_hourly WHERE device_id = ? ORDER BY name`, twin))
	})

	t.Run("answers", func(t *testing.T) {
		assert.Equal(t, []string{
			"203.0.113.1 example.com 6 " + late,
			"203.0.113.2 example.org 3 " + mid,
		}, queryStrings(t, conn, `
			SELECT addr || ' ' || name || ' ' || lookups || ' ' || last_hour
			FROM dns_answers WHERE device_id = ? ORDER BY addr`, twin))
	})

	t.Run("peers", func(t *testing.T) {
		assert.Equal(t, 1, queryInt(t, conn,
			`SELECT count(*) FROM traffic_hourly WHERE device_id = ? AND peer_device_id = ?`, other, twin))
		assert.Equal(t, 1, queryInt(t, conn,
			`SELECT count(*) FROM attempts_hourly WHERE device_id = ? AND peer_device_id = ?`, other, twin))
	})
}

// A lease that goes to a different device moves the address. Only one device
// can hold an address as current.
func TestRecordSweepMovesAddressBetweenDevices(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	sweep(t, s, host("192.0.2.10", macB, ""))

	first := deviceIDByMAC(t, conn, macA)
	second := deviceIDByMAC(t, conn, macB)

	assert.Empty(t, currentIPs(t, conn, first))
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, second))

	// The old holder keeps the row. So its old place is still readable.
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

// A device can answer on two addresses with different reverse names. It gets
// the name of the lower address on every sweep, and logs no rename. If it took
// the name of each address in turn, it would get two renames in every sweep.
func TestRecordSweepKeepsOneNameForADeviceOnTwoAddresses(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	for range 3 {
		sweep(t, s,
			host("192.0.2.10", macA, "host-b.example"),
			host("192.0.2.20", macA, "host-a.example"),
		)
	}

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, []string{"host-b.example"}, queryStrings(t, conn, `SELECT hostname FROM devices`))
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventHostnameChanged)
}

func TestRecordSweepMarksRandomisedAddress(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	// The locally administered bit is set on this address. So the flag comes
	// from it.
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

// Every row that a sweep writes has the one timestamp of that sweep. So no
// device has a last seen time before its first seen time.
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

	// A sweep can open and close within one second. So the invariant is only
	// that it does not finish before it started.
	var started, finished string

	err = conn.QueryRowContext(t.Context(), `SELECT started_at, finished_at FROM scans`).Scan(&started, &finished)
	require.NoError(t, err)
	assert.LessOrEqual(t, started, finished)

	// A later sweep moves last_seen forward, and does not change
	// first_seen.
	sweep(t, s, host("192.0.2.10", macA, ""))

	var secondFirst, secondLast string

	err = conn.QueryRowContext(t.Context(), `SELECT first_seen, last_seen FROM devices`).Scan(&secondFirst, &secondLast)
	require.NoError(t, err)

	assert.Equal(t, first, secondFirst, "first_seen moved")
	assert.Less(t, secondFirst, secondLast)

	assertNoRowSeenBeforeItExisted(t, conn)
}

// assertNoRowSeenBeforeItExisted checks the invariant across every row of the
// database.
func assertNoRowSeenBeforeItExisted(t *testing.T, conn *sql.DB) {
	t.Helper()

	for _, table := range []string{"devices", "addresses"} {
		n := queryInt(t, conn, `SELECT count(*) FROM `+table+` WHERE last_seen < first_seen`)
		assert.Zerof(t, n, "%s rows last seen before they were first seen", table)
	}

	n := queryInt(t, conn, `SELECT count(*) FROM scans WHERE finished_at < started_at`)
	assert.Zero(t, n, "scans finished before they started")
}

// A sweep that cannot be stored must still leave a record of the attempt. For
// this reason, the scan row closes outside the ingest.
func TestRecordSweepRecordsAFailedIngest(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	// The column refuses the zero address. So the ingest stops in the middle,
	// and rolls back the host before it.
	res, err := s.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix),
		[]scanner.Host{host("192.0.2.10", macA, ""), host("", "", "")})
	require.Error(t, err)
	assert.Nil(t, res, "a failed sweep returns no result to read")

	assert.Equal(t, string(dbtype.StatusFailed),
		queryStrings(t, conn, `SELECT status FROM scans`)[0])
	assert.NotEmpty(t, queryStrings(t, conn, `SELECT error FROM scans`)[0])

	// Nothing that the ingest counted stayed. So the scan must not claim
	// anything.
	assert.Equal(t, 0, queryInt(t, conn, `SELECT found_count FROM scans`))
	assert.Equal(t, 0, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, 0, queryInt(t, conn, `SELECT count(*) FROM events`))
}

// A source that reports a device that it did not see can say what is already
// known about it. It cannot put a device that nothing ever saw into the
// inventory. The device list is what is on the network. A static lease for a
// disconnected device is configuration.
func TestReportDoesNotCreateADeviceItHasNotSeen(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	res := report(t, s, fact("192.0.2.50", macA, "printer", false, dbtype.HostnameFromDHCPStatic))

	assert.Equal(t, 0, res.Seen)
	assert.Equal(t, 0, res.Discovered)
	assert.Equal(t, 0, queryInt(t, conn, `SELECT COUNT(*) FROM devices`))
	assert.Equal(t, 0, queryInt(t, conn, `SELECT COUNT(*) FROM addresses`))
}

// An address that a source could not resolve names nothing. It identifies no
// device, and does not say that a device answers.
func TestReportRecordsNothingForAnAddressItCannotResolve(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	res := report(t, s, fact("192.0.2.51", "", "", false, ""))

	assert.Equal(t, 0, res.Seen)
	assert.Equal(t, 0, queryInt(t, conn, `SELECT COUNT(*) FROM devices`))
}

// The same fact about a known device is worth a record. The source knows its
// name, which is the half that a sweep cannot give.
func TestReportNamesAKnownDeviceItHasNotSeen(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	id := deviceIDByMAC(t, conn, macA)
	seen := queryString(t, conn, `SELECT last_seen FROM devices WHERE id = ?`, id)

	res := report(t, s, fact("192.0.2.50", macA, "printer", false, dbtype.HostnameFromDHCPStatic))

	assert.Equal(t, 1, res.Seen)
	assert.Equal(t, "printer", queryString(t, conn, `SELECT hostname FROM devices WHERE id = ?`, id))

	// A name for a device is not a sighting. The address of the name is not
	// an address that the device holds.
	assert.Equal(t, seen, queryString(t, conn, `SELECT last_seen FROM devices WHERE id = ?`, id),
		"a source that did not see the device must not mark it seen")
	assert.Equal(t, []string{"192.0.2.10"}, currentIPs(t, conn, id))
}

// The store keeps the standing that a source claims for a name, as it is. If
// every name were DNS, nothing could compare two sources.
func TestReportKeepsTheStandingOfTheNameItCarries(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, ""))
	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, string(dbtype.HostnameFromDHCPStatic),
		queryString(t, conn, `SELECT hostname_source FROM devices WHERE id = ?`, id))
}

// A source that did not see a device must not take an address from the device
// that answers on it. If not, an old lease on a reused address would move the
// address to a device that is not there.
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

// A source that covers many networks names none of them. So the store matches
// each address to its network.
func TestReportMatchesEachAddressToItsOwnNetwork(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	// Two sweeps, so both prefixes are in the store.
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

// The store still records an address on no recorded network, without a
// network.
func TestReportRecordsAnAddressOnNoKnownNetwork(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	report(t, s, fact("203.0.113.10", macA, "", true, ""))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, []string{"203.0.113.10"}, currentIPs(t, conn, id))
	assert.Equal(t, 1, queryInt(t, conn,
		`SELECT COUNT(*) FROM addresses WHERE ip = ? AND network_id IS NULL`, "203.0.113.10"))
}

// RefreshAddress coalesces the network. So a source that cannot tell the
// network of an address keeps the network from a sweep. The store path cannot
// reach this. The store matches an address against every recorded network, and
// a prefix from a sweep still matches. So the test asserts the guard against
// the query itself.
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

// claimOf reads what one source says about a device, by the name of the
// source.
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

// The name in the list comes from an election over every claim. So a lease
// name does not replace the name that resolves only because it came later.
func TestElectedNamePrefersTheNameThatResolves(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))
	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))

	assert.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))
	assert.Equal(t, string(dbtype.HostnameFromDNS), queryString(t, conn, `SELECT hostname_source FROM devices`))
}

// The row takes the longer spelling. But when only the domain arrived, the
// store does not tell an operator that the device got a new name.
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

// A device whose claims have no name keeps its name. This happens after 000022
// dropped the names that it could not place. An election from claims with no
// name cleared it, and the classifier lost what the name told it.
func TestADeviceKeepsItsNameWhenNoClaimGivesOne(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))

	_, err := conn.ExecContext(t.Context(), `UPDATE device_sources SET hostname = NULL, hostname_source = NULL`)
	require.NoError(t, err)

	sweep(t, s, host("192.0.2.10", macA, ""))

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventHostnameChanged)
}

// A sweep that resolved a name before, and now resolves nothing, did not rename
// the device. The claim keeps the last name. The elected name does not go
// back to a lower source.
func TestASilentSourceKeepsItsLastName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))
	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))

	id := deviceIDByMAC(t, conn, macA)
	require.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))

	// The resolver no longer has a record for the address.
	sweep(t, s, host("192.0.2.10", macA, ""))

	name, standing := claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "host-a.example.com", name)
	assert.Equal(t, string(dbtype.HostnameFromDNS), standing)

	assert.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))
	assert.Equal(t, string(dbtype.HostnameFromDNS), queryString(t, conn, `SELECT hostname_source FROM devices`))
}

// If the only name source of a device goes quiet, the device keeps that name.
// last_seen on the claim gets older. This shows the age.
func TestADeviceKeepsItsNameWhenTheResolverGoesQuiet(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "host-a.example.com"))
	sweep(t, s, host("192.0.2.10", macA, ""))

	assert.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))
	assert.Equal(t, string(dbtype.HostnameFromDNS), queryString(t, conn, `SELECT hostname_source FROM devices`))
}

// A host with several addresses answers a sweep on each of them. The reverse
// lookup succeeds for one, and not for the other. The address with no PTR must
// not clear the name from the other. A second sweep must not log a rename on
// every pass.
func TestAMultiHomedHostKeepsAResolvedNameAcrossAllItsAddresses(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	named := func() {
		sweep(t, s,
			host("192.0.2.10", macA, ""),
			host("192.0.2.11", macA, "host-a.example.com"),
		)
	}

	named()

	id := deviceIDByMAC(t, conn, macA)
	require.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))

	named()
	named()

	assert.Equal(t, "host-a.example.com", queryString(t, conn, `SELECT hostname FROM devices`))

	var changes int

	for _, k := range eventKinds(t, conn, id) {
		if k == dbtype.EventHostnameChanged {
			changes++
		}
	}

	assert.Zero(t, changes, "a stable multi-homed host should log no renames")
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

	// The kept claim now gives the name of the row that stays.
	assert.Equal(t, "printer", queryString(t, conn, `SELECT hostname FROM devices`))
}

// A router that reports its own address merges the row that a sweep across the
// router left for that address. The name of that row then names the router.
func TestRouterOwnAddressFoldsTheAddressOnlyRow(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	// A sweep of the own segment of the router, where its hardware address
	// shows. Then a later sweep that reaches it from a different segment.
	sweep(t, s, host("192.0.2.1", macA, ""))
	sweep(t, s, host("192.0.2.20", "", "gateway.example.test"))

	res := report(t, s, fact("192.0.2.20", macA, "", true, ""))
	require.Equal(t, 1, res.Merged)

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM devices`))
	assert.Equal(t, []string{"192.0.2.1", "192.0.2.20"}, currentIPs(t, conn, id))
	assert.Equal(t, "gateway.example.test", queryString(t, conn, `SELECT hostname FROM devices`))
}

// A source with claims on both rows, at different addresses, keeps a claim for
// each. The device keeps its name. A different source starts the merge here.
// If not, the upsert of this source would hide the merge.
func TestFoldKeepsEachAddressesClaimFromOneSource(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "first.example.com"))
	sweep(t, s, host("192.0.2.20", "", "second.example.com"))

	var ghost int64
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT id FROM devices WHERE mac IS NULL`).Scan(&ghost))

	res := report(t, s, fact("192.0.2.20", macA, "printer", true, dbtype.HostnameFromDHCPStatic))
	require.Equal(t, 1, res.Merged)

	id := deviceIDByMAC(t, conn, macA)

	assert.Zero(t, queryInt(t, conn, `SELECT count(*) FROM device_sources WHERE device_id = ?`, ghost))
	assert.Equal(t, []string{"192.0.2.10 first.example.com", "192.0.2.20 second.example.com"},
		queryStrings(t, conn, `SELECT ds.address || ' ' || ds.hostname FROM device_sources ds
		                       JOIN sources s ON s.id = ds.source_id
		                       WHERE ds.device_id = ? AND s.name = 'test-sweep' ORDER BY ds.address`, id))

	// Both names are reverse DNS. The current name of the device stays.
	assert.Equal(t, "first.example.com", queryString(t, conn, `SELECT hostname FROM devices WHERE id = ?`, id))
}

// A source with claims on both rows at one address collides on the primary key.
// The merged claim keeps a name when the newer reading had none. If not, the
// merge would clear the name of the device.
func TestFoldKeepsANameTheNewerClaimLacks(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.20", "", "named.example.com"))
	sweep(t, s, host("192.0.2.10", macA, ""))

	ghost := queryInt(t, conn, `SELECT id FROM devices WHERE mac IS NULL`)
	id := deviceIDByMAC(t, conn, macA)

	// Both claims about one address. The claim with no name came later.
	_, err := conn.ExecContext(t.Context(),
		`UPDATE device_sources SET address = '192.0.2.10' WHERE device_id = ?`, ghost)
	require.NoError(t, err)

	err = models.New(conn).MoveDeviceSources(t.Context(),
		models.MoveDeviceSourcesParams{IntoID: id, FromID: int64(ghost)})
	require.NoError(t, err)

	name, standing := claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "named.example.com", name)
	assert.Equal(t, string(dbtype.HostnameFromDNS), standing)
}

// A device on two swept networks is two readings from one source, each with a
// different reverse DNS name. Its first name stays across cycles.
func TestTwoNetworksNamingOneDeviceDoNotRenameIt(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	for range 3 {
		sweepPrefix(t, s, "192.0.2.0/24", host("192.0.2.1", macA, "host-a.example"))
		sweepPrefix(t, s, "198.51.100.0/24", host("198.51.100.1", macA, "gw.example"))
	}

	id := deviceIDByMAC(t, conn, macA)
	assert.Equal(t, "host-a.example", queryString(t, conn, `SELECT hostname FROM devices WHERE id = ?`, id))
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventHostnameChanged)

	// A new name on the address that gives the name is one rename.
	sweepPrefix(t, s, "192.0.2.0/24", host("192.0.2.1", macA, "host-c.example"))
	sweepPrefix(t, s, "198.51.100.0/24", host("198.51.100.1", macA, "gw.example"))

	assert.Equal(t, "host-c.example", queryString(t, conn, `SELECT hostname FROM devices WHERE id = ?`, id))
	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM events WHERE device_id = ? AND kind = ?`,
		id, dbtype.EventHostnameChanged))
}

// After the claims backfill, a device with several addresses has claims with no
// name. A sweep cycle names it from the address that has a PTR. It does not
// use a name that no claim gives now.
func TestABackfilledDeviceTakesItsNameFromTheAddressThatHasOne(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweepPrefix(t, s, "192.0.2.0/24", host("192.0.2.1", macA, ""))
	sweepPrefix(t, s, "198.51.100.0/24", host("198.51.100.1", macA, ""))

	id := deviceIDByMAC(t, conn, macA)

	_, err := conn.ExecContext(t.Context(),
		`UPDATE devices SET hostname = 'guess.example', hostname_source = 'DNS' WHERE id = ?`, id)
	require.NoError(t, err)

	sweepPrefix(t, s, "192.0.2.0/24", host("192.0.2.1", macA, ""))
	sweepPrefix(t, s, "198.51.100.0/24", host("198.51.100.1", macA, "gw.example"))

	assert.Equal(t, "gw.example", queryString(t, conn, `SELECT hostname FROM devices WHERE id = ?`, id))
	assert.Equal(t, 1, queryInt(t, conn, `SELECT count(*) FROM events WHERE device_id = ? AND kind = ?`,
		id, dbtype.EventHostnameChanged), "one rename, with no step through no name")
}

// The store keeps Detail exactly, for the device page. It never merges it into
// devices.
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

// Dropped counts a fact with no device to record against.
func TestReportCountsWhatItDropped(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	res := report(t, s, fact("192.0.2.10", "", "printer", false, dbtype.HostnameFromDHCPStatic))

	assert.Equal(t, 1, res.Dropped)
	assert.Zero(t, res.Seen)
	assert.Zero(t, res.Discovered)
}

// RecordFacts is the exported form for the plugin path. A sweep reaches the
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

	// Detail is sorted by key. So a page renders it in the same way twice.
	assert.Equal(t, []Field{
		{Key: "dhcp_comment", Value: "bench unit"},
		{Key: "interface", Value: "vlan10"},
	}, router.Detail)
}

// If Detail does not decode, the page loses only that aside.
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

// mdnsHost builds a swept host with a name from mDNS. A sweep names a host in
// this way when reverse DNS gives no name.
func mdnsHost(ip, mac, hostname string) scanner.Host {
	h := host(ip, mac, hostname)
	h.NameSource = dbtype.HostnameFromMDNS

	return h
}

// ssdpHost builds a swept host with its UPnP friendlyName as name. A sweep
// names a host in this way when no other lookup gives a name.
func ssdpHost(ip, mac, label string) scanner.Host {
	h := host(ip, mac, label)
	h.NameSource = dbtype.HostnameFromSSDP

	return h
}

// dnssdHost builds a swept host with the name that it gives its services. A
// sweep names a host in this way when reverse DNS, mDNS and NetBIOS give no
// name.
func dnssdHost(ip, mac, label string) scanner.Host {
	h := host(ip, mac, label)
	h.NameSource = dbtype.HostnameFromDNSSD

	return h
}

// netbiosHost builds a swept host with a name from NetBIOS. A sweep names a
// host in this way when reverse DNS and mDNS give no name.
func netbiosHost(ip, mac, hostname string) scanner.Host {
	h := host(ip, mac, hostname)
	h.NameSource = dbtype.HostnameFromNetBIOS

	return h
}

// A reverse lookup that times out leaves a host with no name. So the sweep asks
// it over mDNS. That answer must not replace the DNS name of the sweep. If it
// did, the device would get a new name now, and the old name on the next
// sweep.
func TestAnMDNSNameDoesNotReplaceTheSweepsDNSName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "nas.example.com"))
	sweep(t, s, mdnsHost("192.0.2.10", macA, "diskstation.local"))
	sweep(t, s, host("192.0.2.10", macA, "nas.example.com"))

	id := deviceIDByMAC(t, conn, macA)

	name, standing := claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "nas.example.com", name)
	assert.Equal(t, string(dbtype.HostnameFromDNS), standing)

	assert.Equal(t, "nas.example.com", queryString(t, conn, `SELECT hostname FROM devices`))
	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventHostnameChanged)
}

// When reverse DNS names a host that the sweep only knew over mDNS, the DNS
// name takes the claim.
func TestADNSNameReplacesTheSweepsMDNSName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, mdnsHost("192.0.2.10", macA, "diskstation.local"))
	sweep(t, s, host("192.0.2.10", macA, "nas.example.com"))

	name, standing := claimOf(t, conn, deviceIDByMAC(t, conn, macA), "test-sweep")
	assert.Equal(t, "nas.example.com", name)
	assert.Equal(t, string(dbtype.HostnameFromDNS), standing)
}

// A device that gives itself a new name over mDNS gets the new name on the
// claim.
func TestAnMDNSNameReplacesAnEarlierMDNSName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, mdnsHost("192.0.2.10", macA, "diskstation.local"))
	sweep(t, s, mdnsHost("192.0.2.10", macA, "backup.local"))

	name, standing := claimOf(t, conn, deviceIDByMAC(t, conn, macA), "test-sweep")
	assert.Equal(t, "backup.local", name)
	assert.Equal(t, string(dbtype.HostnameFromMDNS), standing)
}

// If an mDNS query gets no answer for one sweep, the sweep asks the host over
// NetBIOS. That answer must not replace the mDNS name of the sweep.
func TestANetBIOSNameDoesNotReplaceTheSweepsMDNSName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, mdnsHost("192.0.2.10", macA, "gaming-pc.local"))
	sweep(t, s, netbiosHost("192.0.2.10", macA, "DESKTOP-4F2K"))

	id := deviceIDByMAC(t, conn, macA)

	name, standing := claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "gaming-pc.local", name)
	assert.Equal(t, string(dbtype.HostnameFromMDNS), standing)

	assert.NotContains(t, eventKinds(t, conn, id), dbtype.EventHostnameChanged)
}

// When a host that the sweep only knew over NetBIOS answers mDNS, the mDNS name
// takes the claim.
func TestAnMDNSNameReplacesTheSweepsNetBIOSName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, netbiosHost("192.0.2.10", macA, "DESKTOP-4F2K"))
	sweep(t, s, mdnsHost("192.0.2.10", macA, "gaming-pc.local"))

	name, standing := claimOf(t, conn, deviceIDByMAC(t, conn, macA), "test-sweep")
	assert.Equal(t, "gaming-pc.local", name)
	assert.Equal(t, string(dbtype.HostnameFromMDNS), standing)
}

// The claim of a router holds no swept name. So a dynamic lease still replaces
// its static lease.
func TestARouterClaimTakesALowerRankedName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	report(t, s, fact("192.0.2.10", macA, "printer", true, dbtype.HostnameFromDHCPStatic))
	report(t, s, fact("192.0.2.10", macA, "office-printer", true, dbtype.HostnameFromDHCPLease))

	name, standing := claimOf(t, conn, deviceIDByMAC(t, conn, macA), "test-router")
	assert.Equal(t, "office-printer", name)
	assert.Equal(t, string(dbtype.HostnameFromDHCPLease), standing)
}

// A friendlyName names a device that nothing else names, with its spaces. It
// ranks below a NetBIOS name on the claim of the sweep.
func TestAnSSDPLabelNamesADeviceBelowNetBIOS(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, ssdpHost("192.0.2.10", macA, "Living Room TV"))

	id := deviceIDByMAC(t, conn, macA)
	require.Equal(t, "Living Room TV", queryString(t, conn, `SELECT hostname FROM devices`))

	sweep(t, s, netbiosHost("192.0.2.10", macA, "LIVINGROOM"))
	sweep(t, s, ssdpHost("192.0.2.10", macA, "Living Room TV"))

	name, standing := claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "LIVINGROOM", name)
	assert.Equal(t, string(dbtype.HostnameFromNetBIOS), standing)
}

// A DNS-SD name ranks below a NetBIOS name and above an SSDP friendlyName, on
// the claim of the sweep.
func TestADNSSDNameRanksBetweenNetBIOSAndSSDP(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, ssdpHost("192.0.2.10", macA, "UPnP TV"))
	sweep(t, s, dnssdHost("192.0.2.10", macA, "Living Room TV"))

	id := deviceIDByMAC(t, conn, macA)
	name, standing := claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "Living Room TV", name)
	assert.Equal(t, string(dbtype.HostnameFromDNSSD), standing)

	sweep(t, s, netbiosHost("192.0.2.10", macA, "LIVINGROOM"))
	sweep(t, s, dnssdHost("192.0.2.10", macA, "Living Room TV"))

	name, standing = claimOf(t, conn, id, "test-sweep")
	assert.Equal(t, "LIVINGROOM", name)
	assert.Equal(t, string(dbtype.HostnameFromNetBIOS), standing)
}
