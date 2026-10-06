package notify_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/notify"
)

var (
	discovered = dbtype.EventDeviceDiscovered
	opened     = dbtype.EventPortOpened
	closed     = dbtype.EventPortClosed
	all        = []dbtype.EventKind{discovered, opened, closed, dbtype.EventHostnameChanged}
)

func sweepOf(events ...*inventory.Event) *inventory.ScanChanges {
	return &inventory.ScanChanges{Kind: dbtype.ScanDiscovery, Source: "sweep", Network: "192.0.2.0/24", Events: events}
}

func TestNewDevices(t *testing.T) {
	m, ok := notify.ForScan(9, sweepOf(
		&inventory.Event{ID: 1, Kind: discovered, DeviceName: "Espressif device", NewValue: "192.0.2.47"},
		&inventory.Event{ID: 2, Kind: discovered, DeviceName: "Pixel-7", NewValue: "192.0.2.63"},
	), all)
	require.True(t, ok)

	assert.Equal(t, "2 new devices on 192.0.2.0/24", m.Title)
	assert.Equal(t, "Espressif device · 192.0.2.47\nPixel-7 · 192.0.2.63", m.Body)
	assert.Equal(t, int64(9), m.ScanID)
	assert.Len(t, m.Events, 2)
}

func TestMixedChangesLeadWithNewDevices(t *testing.T) {
	m, ok := notify.ForScan(9, sweepOf(
		&inventory.Event{ID: 1, Kind: dbtype.EventHostnameChanged, DeviceName: "host-a", OldValue: "old", NewValue: "new"},
		&inventory.Event{ID: 2, Kind: discovered, DeviceName: "host-b", NewValue: "192.0.2.11"},
	), all)
	require.True(t, ok)

	assert.Equal(t, "2 changes on 192.0.2.0/24", m.Title)
	assert.Equal(t, "host-b · 192.0.2.11\nhost-a changed its hostname old → new", m.Body)
}

func TestPortScan(t *testing.T) {
	m, ok := notify.ForScan(9, &inventory.ScanChanges{Kind: dbtype.ScanPorts, Source: "sweep", Events: []*inventory.Event{
		{ID: 1, Kind: opened, DeviceName: "nas", NewValue: "22", Detail: "ssh"},
		{ID: 2, Kind: closed, DeviceName: "printer", OldValue: "9100"},
	}}, all)
	require.True(t, ok)

	assert.Equal(t, "2 changes in the port scan", m.Title)
	assert.Equal(t, "nas started listening on port 22 (ssh)\nprinter stopped listening on port 9100", m.Body)
}

func TestOnlyTheKindsChosen(t *testing.T) {
	c := sweepOf(
		&inventory.Event{ID: 1, Kind: discovered, DeviceName: "host-a", NewValue: "192.0.2.10"},
		&inventory.Event{ID: 2, Kind: dbtype.EventHostnameChanged, DeviceName: "host-b", NewValue: "b"},
	)

	m, ok := notify.ForScan(9, c, []dbtype.EventKind{discovered})
	require.True(t, ok)
	assert.Equal(t, "1 new device on 192.0.2.0/24", m.Title)
	assert.Len(t, m.Events, 1)

	_, ok = notify.ForScan(9, c, []dbtype.EventKind{opened})
	assert.False(t, ok, "nothing chosen changed")

	_, ok = notify.ForScan(9, c, nil)
	assert.False(t, ok, "no kinds chosen")

	assert.Len(t, c.Events, 2, "the scan's changes are left as they were")
}

// A first scan that found nothing but new devices, as one into an empty
// inventory does, is sent as a count.
func TestFirstScanOfNewDevicesIsACount(t *testing.T) {
	c := sweepOf(
		&inventory.Event{ID: 1, Kind: discovered, DeviceName: "host-a", NewValue: "192.0.2.10"},
		&inventory.Event{ID: 2, Kind: discovered, DeviceName: "host-b", NewValue: "192.0.2.11"},
	)
	c.First, c.Found = true, 2

	m, ok := notify.ForScan(9, c, all)
	require.True(t, ok)
	assert.Equal(t, "First scan on 192.0.2.0/24", m.Title)
	assert.Equal(t, "Found 2 devices", m.Body)
}

// A first scan that found devices the inventory already knew, as when a
// router joins, names the ones that are new.
func TestFirstScanNamesTheNewDevicesAmongKnownOnes(t *testing.T) {
	c := sweepOf(&inventory.Event{ID: 1, Kind: discovered, DeviceName: "host-a", NewValue: "192.0.2.10"})
	c.First, c.Found = true, 3

	m, ok := notify.ForScan(9, c, all)
	require.True(t, ok)
	assert.Equal(t, "1 new device on 192.0.2.0/24", m.Title)
	assert.Equal(t, "host-a · 192.0.2.10", m.Body)
}

func TestARouterSourceIsNamed(t *testing.T) {
	c := &inventory.ScanChanges{Kind: dbtype.ScanDiscovery, Source: "routeros:gateway", Events: []*inventory.Event{
		{ID: 1, Kind: discovered, DeviceName: "host-a", NewValue: "192.0.2.10"},
	}}

	m, _ := notify.ForScan(9, c, all)
	assert.Equal(t, "1 new device from routeros:gateway", m.Title)
}

func TestALargeScanCountsWhatItLeavesOut(t *testing.T) {
	events := make([]*inventory.Event, notify.MaxLines+3)
	for i := range events {
		events[i] = &inventory.Event{ID: int64(i + 1), Kind: discovered, DeviceName: fmt.Sprintf("host-%d", i)}
	}

	m, _ := notify.ForScan(9, sweepOf(events...), all)
	lines := strings.Split(m.Body, "\n")

	assert.Len(t, lines, notify.MaxLines+1)
	assert.Equal(t, "and 3 more", lines[len(lines)-1])
	assert.Equal(t, fmt.Sprintf("%d new devices on 192.0.2.0/24", notify.MaxLines+3), m.Title)
}

func TestLineWithoutADevice(t *testing.T) {
	// The device was deleted since; the change still reads.
	assert.Equal(t, "192.0.2.10", notify.Line(&inventory.Event{Kind: discovered, NewValue: "192.0.2.10"}))
	assert.Equal(t, "started listening on port 22", notify.Line(&inventory.Event{Kind: opened, NewValue: "22"}))
}

func TestWatchedDevicesGoingQuiet(t *testing.T) {
	quiet := []dbtype.EventKind{dbtype.EventDeviceQuiet, dbtype.EventDeviceBack, discovered}
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	m, ok := notify.ForScan(9, sweepOf(
		&inventory.Event{ID: 1, Kind: dbtype.EventDeviceQuiet, DeviceName: "nas", OldValue: "2026-01-01T11:42:00Z", At: at},
	), quiet)
	require.True(t, ok)

	assert.Equal(t, "1 device went quiet on 192.0.2.0/24", m.Title)
	assert.Equal(t, "nas went quiet after 18 minutes without an answer", m.Body)

	m, ok = notify.ForScan(9, sweepOf(
		&inventory.Event{ID: 1, Kind: dbtype.EventDeviceBack, DeviceName: "nas", OldValue: "2026-01-01T10:00:00Z", At: at},
		&inventory.Event{ID: 2, Kind: dbtype.EventDeviceBack, DeviceName: "camera", OldValue: "2026-01-01T11:00:00Z", At: at},
	), quiet)
	require.True(t, ok)

	assert.Equal(t, "2 devices came back on 192.0.2.0/24", m.Title)
	assert.Equal(t, "nas came back after 2 hours quiet\ncamera came back after 1 hour quiet", m.Body)
}

func TestAQuietDeviceLeadsAMixedMessage(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	m, ok := notify.ForScan(9, sweepOf(
		&inventory.Event{ID: 1, Kind: discovered, DeviceName: "host-b", NewValue: "192.0.2.11", At: at},
		&inventory.Event{ID: 2, Kind: dbtype.EventDeviceQuiet, DeviceName: "nas", OldValue: "2026-01-01T11:40:00Z", At: at},
	), []dbtype.EventKind{dbtype.EventDeviceQuiet, discovered})
	require.True(t, ok)

	assert.Equal(t, "2 changes on 192.0.2.0/24", m.Title)
	assert.Equal(t, "nas went quiet after 20 minutes without an answer\nhost-b · 192.0.2.11", m.Body)
}
