package inventory

import (
	"database/sql"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// curate applies what the user owns directly, since the queries that write it
// do not exist yet.
func curate(t *testing.T, conn *sql.DB, id int64, column string, value any) {
	t.Helper()

	// The column name is one of a fixed set chosen by the calling test, never
	// user input, so the interpolation is safe.
	_, err := conn.ExecContext(t.Context(), fmt.Sprintf(`UPDATE devices SET %s = ? WHERE id = ?`, column), value, id) // #nosec G202
	require.NoError(t, err)
}

// names returns what each device would be called, which is the one value that
// says which rows came back without depending on their order.
func names(devices []*Device) []string {
	out := make([]string, 0, len(devices))
	for _, d := range devices {
		out = append(out, d.Name())
	}

	return out
}

func TestListDevicesReturnsWhatWasSwept(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s,
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, ""),
	)

	devices, err := s.ListDevices(t.Context(), DeviceFilter{})
	require.NoError(t, err)
	require.Len(t, devices, 2)

	assert.ElementsMatch(t, []string{"printer.local", macB}, names(devices))

	for _, d := range devices {
		assert.True(t, d.Online, "just swept, so inside the online window")
		assert.Len(t, d.Current, 1)
		// A list reports the current addresses but not their history.
		assert.Empty(t, d.Addresses)
	}
}

// The flattened view is the reason these types exist: an absent column has to
// arrive as an empty string, not as a sql.NullString a template would have to
// unwrap.
func TestListDevicesFlattensAbsentColumns(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", "", ""))

	devices, err := s.ListDevices(t.Context(), DeviceFilter{})
	require.NoError(t, err)
	require.Len(t, devices, 1)

	d := devices[0]
	assert.Empty(t, d.MAC)
	assert.Empty(t, d.Hostname)
	assert.Empty(t, d.Vendor)
	assert.Empty(t, d.Label)
	assert.Equal(t, dbtype.IdentityIP, d.IdentitySource)

	// With nothing else known, the address it answered on names it.
	assert.Equal(t, "192.0.2.10", d.Name())
}

func TestListDevicesSearch(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s,
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	)

	curate(t, conn, deviceIDByMAC(t, conn, macA), "label", "Office printer")

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"by hostname", "nas", []string{"nas.local"}},
		{"by label", "office", []string{"Office printer"}},
		{"by address", "192.0.2.11", []string{"nas.local"}},
		{"by hardware address", "53:02", []string{"nas.local"}},
		{"matching both", "local", []string{"Office printer", "nas.local"}},
		{"matching none", "nothing-here", nil},
	}

	for _, tc := range tests {
		// Not parallel: the subtests share one store, whose test clock advances
		// on every read.
		t.Run(tc.name, func(t *testing.T) {
			devices, err := s.ListDevices(t.Context(), DeviceFilter{Query: tc.query})
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.want, names(devices))
		})
	}
}

func TestListDevicesFiltersByGroup(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s,
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	)

	curate(t, conn, deviceIDByMAC(t, conn, macA), "group_name", "office")

	devices, err := s.ListDevices(t.Context(), DeviceFilter{Group: "office"})
	require.NoError(t, err)
	assert.Equal(t, []string{"printer.local"}, names(devices))

	groups, err := s.Groups(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"office"}, groups)
}

func TestListDevicesHidesIgnoredUnlessAsked(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s,
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	)

	curate(t, conn, deviceIDByMAC(t, conn, macA), "is_ignored", 1)

	devices, err := s.ListDevices(t.Context(), DeviceFilter{})
	require.NoError(t, err)
	assert.Equal(t, []string{"nas.local"}, names(devices))

	devices, err = s.ListDevices(t.Context(), DeviceFilter{IncludeIgnored: true})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"printer.local", "nas.local"}, names(devices))
}

func TestListDevicesStatusFollowsTheOnlineWindow(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	online, err := s.ListDevices(t.Context(), DeviceFilter{Status: StatusOnline})
	require.NoError(t, err)
	assert.Len(t, online, 1)

	offline, err := s.ListDevices(t.Context(), DeviceFilter{Status: StatusOffline})
	require.NoError(t, err)
	assert.Empty(t, offline)

	// The same sighting, judged against a window narrower than the clock's own
	// tick, is now too stale to count.
	s.onlineWindow = time.Nanosecond

	online, err = s.ListDevices(t.Context(), DeviceFilter{Status: StatusOnline})
	require.NoError(t, err)
	assert.Empty(t, online)

	offline, err = s.ListDevices(t.Context(), DeviceFilter{Status: StatusOffline})
	require.NoError(t, err)
	require.Len(t, offline, 1)
	assert.False(t, offline[0].Online)
}

// The column is TEXT, so this ordering is exactly the one SQL cannot do.
func TestListDevicesSortsAddressesNumerically(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s,
		host("192.0.2.100", macA, ""),
		host("192.0.2.9", macB, ""),
	)

	devices, err := s.ListDevices(t.Context(), DeviceFilter{Sort: SortAddress})
	require.NoError(t, err)
	require.Len(t, devices, 2)

	assert.Equal(t, "192.0.2.9", devices[0].Current[0].String())
	assert.Equal(t, "192.0.2.100", devices[1].Current[0].String())
}

func TestListDevicesSortsByName(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s,
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	)

	devices, err := s.ListDevices(t.Context(), DeviceFilter{Sort: SortName})
	require.NoError(t, err)
	assert.Equal(t, []string{"nas.local", "printer.local"}, names(devices))
}

func TestGetDeviceCarriesAddressHistory(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	sweep(t, s, host("192.0.2.11", macA, "printer.local"))

	id := deviceIDByMAC(t, conn, macA)

	d, err := s.Device(t.Context(), id)
	require.NoError(t, err)

	assert.Equal(t, "printer.local", d.Name())
	assert.Equal(t, macA, d.MAC)
	assert.Equal(t, dbtype.IdentityMAC, d.IdentitySource)
	require.Len(t, d.Addresses, 2)

	for _, a := range d.Addresses {
		assert.False(t, a.FirstSeen.IsZero())
		assert.False(t, a.LastSeen.IsZero())
	}

	// A sweep only reports what answered, so an address a device answered on
	// before is not released the moment another appears: within the grace
	// window -- these two sweeps are a clock tick apart -- both stay current.
	// Retirement past the window is TestRetiresAnAddressADeviceMovedOff.
	assert.Equal(t, []netip.Addr{
		netip.MustParseAddr("192.0.2.10"),
		netip.MustParseAddr("192.0.2.11"),
	}, d.Current)
}

func TestGetDeviceUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	_, err := s.Device(t.Context(), 404)
	require.ErrorIs(t, err, ErrNotFound)
}

// A sweep records the prefix it swept, so an address it found sits on a known
// network and the detail page can name it.
func TestGetDeviceCarriesEachAddressNetwork(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	id := deviceIDByMAC(t, conn, macA)

	d, err := s.Device(t.Context(), id)
	require.NoError(t, err)

	require.Len(t, d.Addresses, 1)
	require.NotNil(t, d.Addresses[0].Network)
	assert.Equal(t, prefix, d.Addresses[0].Network.CIDR)
}

// The device read carries the ports a scan has found, still-open ones first,
// and only the detail read fills them.
func TestGetDeviceCarriesOpenPorts(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	id := deviceIDByMAC(t, conn, macA)

	recordPorts(t, s, portScan("192.0.2.10", []uint16{22, 443}, []uint16{22, 80, 443}))
	recordPorts(t, s, portScan("192.0.2.10", []uint16{22}, []uint16{22, 80, 443})) // 443 goes quiet

	d, err := s.Device(t.Context(), id)
	require.NoError(t, err)

	require.Len(t, d.Ports, 2)

	assert.Equal(t, uint16(22), d.Ports[0].Number)
	assert.Equal(t, "ssh", d.Ports[0].Service)
	assert.True(t, d.Ports[0].Open())

	assert.Equal(t, uint16(443), d.Ports[1].Number)
	assert.False(t, d.Ports[1].Open(), "the port that stopped answering keeps its row, closed")

	// A list read does not pay for the full port history, but it does carry the
	// numbers of the open ones so a row can show what the device exposes.
	list, err := s.ListDevices(t.Context(), DeviceFilter{})
	require.NoError(t, err)
	require.NotEmpty(t, list)
	assert.Nil(t, list[0].Ports)
	assert.Equal(t, []uint16{22}, list[0].OpenPorts, "the closed port is not one it exposes now")
}

// The list read carries the prefixes a device currently sits on, so a row can
// name and link the network without a query of its own.
func TestListDevicesCarriesNetworks(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	list, err := s.ListDevices(t.Context(), DeviceFilter{})
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.Len(t, list[0].Networks, 1)
	assert.Equal(t, prefix, list[0].Networks[0].CIDR)
}

func TestDeviceEventsAreMostRecentFirst(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	sweep(t, s, host("192.0.2.10", macA, "renamed.local"))

	id := deviceIDByMAC(t, conn, macA)

	events, err := s.DeviceEvents(t.Context(), id, 10)
	require.NoError(t, err)
	require.Len(t, events, 3)

	assert.Equal(t, dbtype.EventHostnameChanged, events[0].Kind)
	assert.Equal(t, "printer.local", events[0].OldValue)
	assert.Equal(t, "renamed.local", events[0].NewValue)

	// The two the discovery wrote share a timestamp, so only the pair is
	// ordered against the change, not the two against each other.
	assert.ElementsMatch(t,
		[]dbtype.EventKind{dbtype.EventDeviceDiscovered, dbtype.EventAddressAdded},
		[]dbtype.EventKind{events[1].Kind, events[2].Kind},
	)

	limited, err := s.DeviceEvents(t.Context(), id, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestListEventsNamesTheDevice(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	page, err := s.ListEvents(t.Context(), Page{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, page.Events)

	// The whole log fits in one page, so there is nothing after it.
	assert.True(t, page.Next.IsZero())

	for _, e := range page.Events {
		assert.Equal(t, "printer.local", e.DeviceName)
		assert.NotZero(t, e.ScanID)
		assert.False(t, e.At.IsZero())
	}
}

func TestListScansDescribesTheRun(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	page, err := s.ListScans(t.Context(), Page{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Scans, 1)

	sc := page.Scans[0]
	assert.Equal(t, "test-sweep", sc.Source)
	assert.Equal(t, prefix, sc.Network)
	assert.Equal(t, dbtype.StatusOK, sc.Status)
	assert.Equal(t, dbtype.ScanDiscovery, sc.Kind)
	assert.Equal(t, 2, sc.Found)
	assert.Empty(t, sc.Error)
	assert.Positive(t, sc.Took(), "the clock advances between opening and closing the scan")
	assert.Zero(t, (&Scan{StartedAt: sc.StartedAt}).Took(), "a running scan has taken no time yet")

	latest, err := s.LatestScan(t.Context())
	require.NoError(t, err)
	assert.Equal(t, sc.ID, latest.ID)
}

func TestLatestScanWithoutAnyIsNotFound(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	_, err := s.LatestScan(t.Context())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestLastSuccessfulScanAtReportsWhenTheScanFinished(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "a"))

	latest, err := s.LatestScan(t.Context())
	require.NoError(t, err)
	require.False(t, latest.FinishedAt.IsZero())
	require.NotEqual(t, latest.StartedAt, latest.FinishedAt, "the clock advances during the scan")

	at, err := s.LastSuccessfulScanAt(t.Context(), dbtype.ScanDiscovery)
	require.NoError(t, err)
	assert.Equal(t, latest.FinishedAt, at, "the schedule is anchored on the finish, not the start")
}

// A scan whose process died mid-run never finished, so the work never landed.
// Counting it would hold the next sweep off for an interval it was never owed.
func TestLastSuccessfulScanAtIgnoresAnUnfinishedScan(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	_, err := conn.ExecContext(t.Context(),
		`INSERT INTO sources (kind, name) VALUES ('SWEEP', 'interrupted');
		 INSERT INTO scans (source_id, kind, status, started_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'interrupted'), 'DISCOVERY', 'RUNNING',
		         '2026-01-01T00:00:00.000Z');`)
	require.NoError(t, err)

	_, err = s.LastSuccessfulScanAt(t.Context(), dbtype.ScanDiscovery)
	require.ErrorIs(t, err, ErrNotFound)
}

// A sweep in progress must not mask the last completed one: answering
// ErrNotFound here would report "never swept" mid-sweep and start a second.
func TestLastSuccessfulScanAtSkipsPastAnUnfinishedScan(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	_, err := conn.ExecContext(t.Context(),
		`INSERT INTO sources (kind, name) VALUES ('SWEEP', 'mid-sweep');
		 INSERT INTO scans (source_id, kind, status, started_at, finished_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'mid-sweep'), 'DISCOVERY', 'OK',
		         '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:05.000Z');
		 INSERT INTO scans (source_id, kind, status, started_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'mid-sweep'), 'DISCOVERY', 'RUNNING',
		         '2026-01-01T00:10:00.000Z');`)
	require.NoError(t, err)

	at, err := s.LastSuccessfulScanAt(t.Context(), dbtype.ScanDiscovery)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC), at)
}

// A scan that finished but failed gathered nothing. Crediting it would hold the
// next run off for an interval that produced no data, which is the staleness
// the interval is there to bound.
func TestLastSuccessfulScanAtIgnoresAFailedScan(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	_, err := conn.ExecContext(t.Context(),
		`INSERT INTO sources (kind, name) VALUES ('SWEEP', 'failed');
		 INSERT INTO scans (source_id, kind, status, error, started_at, finished_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'failed'), 'DISCOVERY', 'FAILED', 'boom',
		         '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:05.000Z');`)
	require.NoError(t, err)

	_, err = s.LastSuccessfulScanAt(t.Context(), dbtype.ScanDiscovery)
	require.ErrorIs(t, err, ErrNotFound)
}

// A failed scan must not mask the successful one before it either: the schedule
// still resumes from the last run that actually gathered something.
func TestLastSuccessfulScanAtSkipsPastAFailedScan(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	_, err := conn.ExecContext(t.Context(),
		`INSERT INTO sources (kind, name) VALUES ('SWEEP', 'mixed');
		 INSERT INTO scans (source_id, kind, status, started_at, finished_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'mixed'), 'DISCOVERY', 'OK',
		         '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:05.000Z');
		 INSERT INTO scans (source_id, kind, status, error, started_at, finished_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'mixed'), 'DISCOVERY', 'FAILED', 'boom',
		         '2026-01-01T00:10:00.000Z', '2026-01-01T00:10:05.000Z');`)
	require.NoError(t, err)

	at, err := s.LastSuccessfulScanAt(t.Context(), dbtype.ScanDiscovery)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC), at)
}

func TestLastSuccessfulScanAtWithoutAnyIsNotFound(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	_, err := s.LastSuccessfulScanAt(t.Context(), dbtype.ScanDiscovery)
	require.ErrorIs(t, err, ErrNotFound)
}

// The kind is the point of the query: a port scan says nothing about whether
// the devices are due to be swept again, and answering with one would hold the
// sweep off for an interval it was owed.
func TestLastSuccessfulScanAtIgnoresOtherKinds(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	_, err := conn.ExecContext(t.Context(),
		`INSERT INTO sources (kind, name) VALUES ('SWEEP', 'ports-only');
		 INSERT INTO scans (source_id, kind, status, started_at, finished_at)
		 VALUES ((SELECT id FROM sources WHERE name = 'ports-only'), 'PORTS', 'OK',
		         '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:05.000Z');`)
	require.NoError(t, err)

	_, err = s.LastSuccessfulScanAt(t.Context(), dbtype.ScanDiscovery)
	require.ErrorIs(t, err, ErrNotFound, "a ports scan must not answer for a discovery sweep")

	at, err := s.LastSuccessfulScanAt(t.Context(), dbtype.ScanPorts)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC), at)
}

func TestStatsCountsTheInventory(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s,
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	)

	stats, err := s.Stats(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &Stats{Total: 2, Online: 2, Offline: 0, Ignored: 0, Discovered: 2}, stats)

	curate(t, conn, deviceIDByMAC(t, conn, macA), "is_ignored", 1)

	stats, err = s.Stats(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Ignored)
	assert.Equal(t, 2, stats.Total, "an ignored device is still a device")

	s.onlineWindow = time.Nanosecond

	stats, err = s.Stats(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &Stats{Total: 2, Online: 0, Offline: 2, Ignored: 1, Discovered: 2}, stats)
}

func TestListNetworksCountsWhatIsOnEachPrefix(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s,
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	)

	_, err := s.RecordSweep(t.Context(), "test-sweep",
		netip.MustParsePrefix("198.51.100.0/24"),
		[]scanner.Host{host("198.51.100.5", "00:00:5e:00:53:03", "camera.local")})
	require.NoError(t, err)

	// Named directly rather than through RecordNetworks, so this test covers
	// the read and not the write beside it.
	_, err = conn.ExecContext(t.Context(),
		`UPDATE networks SET name = ?, vlan_id = ? WHERE cidr = ?`, "Home", 10, prefix)
	require.NoError(t, err)

	networks, err := s.ListNetworks(t.Context())
	require.NoError(t, err)
	require.Len(t, networks, 2)

	byCIDR := make(map[string]*Network, len(networks))
	for _, n := range networks {
		byCIDR[n.CIDR] = n
	}

	home := byCIDR[prefix]
	require.NotNil(t, home)
	assert.Equal(t, "Home", home.Name)
	assert.Equal(t, 10, home.VLAN)
	assert.Equal(t, 2, home.Total)
	assert.Equal(t, 2, home.Online)
	assert.Equal(t, 0, home.Offline)

	// A network nobody has named is still a network, and reads as one.
	other := byCIDR["198.51.100.0/24"]
	require.NotNil(t, other)
	assert.Empty(t, other.Name)
	assert.Zero(t, other.VLAN)
	assert.Equal(t, 1, other.Total)
	assert.Equal(t, 1, other.Online)

	// The counts follow the same window a device list is judged by, so the two
	// surfaces cannot disagree about who is answering.
	s.onlineWindow = time.Nanosecond

	networks, err = s.ListNetworks(t.Context())
	require.NoError(t, err)
	require.Len(t, networks, 2)

	for _, n := range networks {
		assert.Zero(t, n.Online, n.CIDR)
		assert.Equal(t, n.Total, n.Offline, n.CIDR)
	}
}

func TestEmptyInventoryReadsCleanly(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	devices, err := s.ListDevices(t.Context(), DeviceFilter{})
	require.NoError(t, err)
	assert.Empty(t, devices)

	page, err := s.ListEvents(t.Context(), Page{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, page.Events)

	groups, err := s.Groups(t.Context())
	require.NoError(t, err)
	assert.Empty(t, groups)

	stats, err := s.Stats(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &Stats{}, stats)

	networks, err := s.ListNetworks(t.Context())
	require.NoError(t, err)
	assert.Empty(t, networks)
}

func TestWithOnlineWindow(t *testing.T) {
	t.Parallel()

	assert.Equal(t, DefaultOnlineWindow, New(nil, nil).onlineWindow)
	assert.Equal(t, time.Hour, New(nil, nil, WithOnlineWindow(time.Hour)).onlineWindow)

	// A window has to be positive to mean anything, and a zero one would
	// otherwise report every device offline.
	assert.Equal(t, DefaultOnlineWindow, New(nil, nil, WithOnlineWindow(0)).onlineWindow)
	assert.Equal(t, DefaultOnlineWindow, New(nil, nil, WithOnlineWindow(-time.Hour)).onlineWindow)
}

func TestWithClock(t *testing.T) {
	t.Parallel()

	pinned := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s := New(nil, nil, WithClock(func() time.Time { return pinned }))

	assert.Equal(t, pinned, s.now())
	assert.Equal(t, pinned.Add(-DefaultOnlineWindow), s.onlineCutoff())

	// A nil clock would panic on the first write rather than fall back.
	assert.NotNil(t, New(nil, nil, WithClock(nil)).now)
}

func TestDisplayNameFallsBackInOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		device *Device
		want   string
	}{
		{"label wins", &Device{ID: 1, Label: "Printer", Hostname: "h", MAC: macA}, "Printer"},
		{"then hostname", &Device{ID: 1, Hostname: "h", MAC: macA}, "h"},
		{"then hardware address", &Device{ID: 1, MAC: macA}, macA},
		{
			"then the address it answered on",
			&Device{ID: 1, Current: []netip.Addr{netip.MustParseAddr("192.0.2.10")}},
			"192.0.2.10",
		},
		{"and finally the row itself", &Device{ID: 7}, "device 7"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.device.Name())
		})
	}
}

func TestParseAddrs(t *testing.T) {
	t.Parallel()

	assert.Nil(t, parseAddrs(""))

	assert.Equal(t, []netip.Addr{
		netip.MustParseAddr("192.0.2.9"),
		netip.MustParseAddr("192.0.2.100"),
	}, parseAddrs("192.0.2.100 192.0.2.9"))

	// Nothing reaches the column except through dbtype.Addr, so unparseable
	// text is dropped rather than failing the read of every other device.
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.9")}, parseAddrs("not-an-address 192.0.2.9"))
}

func TestParsePorts(t *testing.T) {
	t.Parallel()

	assert.Nil(t, parsePorts(""))

	// The aggregate has no order worth keeping, so the numbers come back sorted.
	assert.Equal(t, []uint16{22, 80, 443}, parsePorts("443 22 80"))

	// The column is written from a CHECK-constrained one, so anything that is not
	// a port is a row written around the application and is dropped.
	assert.Equal(t, []uint16{443}, parsePorts("nope 443 0 99999"))
}

func TestStatusAdmits(t *testing.T) {
	t.Parallel()

	assert.True(t, StatusAny.admits(true))
	assert.True(t, StatusAny.admits(false))
	assert.True(t, StatusOnline.admits(true))
	assert.False(t, StatusOnline.admits(false))
	assert.False(t, StatusOffline.admits(true))
	assert.True(t, StatusOffline.admits(false))

	// An unrecognised filter widens the list rather than emptying it.
	assert.True(t, Status("nonsense").admits(false))
}
