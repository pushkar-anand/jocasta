package web

import (
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
)

// now is fixed so every case reads as an offset from one instant.
var now = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func TestAgo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"never seen", time.Time{}, "never"},
		{"seconds", now.Add(-20 * time.Second), "just now"},
		{"minutes", now.Add(-4 * time.Minute), "4m ago"},
		{"an hour", now.Add(-90 * time.Minute), "1h ago"},
		{"hours", now.Add(-5 * time.Hour), "5h ago"},
		{"days", now.Add(-50 * time.Hour), "2d ago"},
		{"a date once it stops being relative", now.Add(-30 * 24 * time.Hour), "30 Jan 2026"},

		// A sighting stamped ahead of the clock is a clock difference between
		// two hosts, not a device seen in the future.
		{"ahead of the clock", now.Add(time.Minute), "just now"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ago(now, tc.at))
		})
	}
}

func TestStamp(t *testing.T) {
	t.Parallel()

	seen := now.Add(-4 * time.Minute)
	got := string(stamp(now, seen, ""))

	assert.Contains(t, got, "<time ")
	assert.Contains(t, got, ">4m ago</time>", "carries the relative label ago gives")
	assert.Contains(t, got, `datetime="`+seen.Local().Format(time.RFC3339)+`"`)
	assert.Contains(t, got, `title="`+seen.Local().Format("Mon 2 Jan 2006, 15:04 MST")+`"`,
		"the exact moment and zone are on the title")

	// A class rides through for the few timestamps that need styling.
	assert.Contains(t, string(stamp(now, seen, "act__when")), `class="act__when"`)

	// Nothing was ever seen: no moment to place, so no datetime or title.
	zero := string(stamp(now, time.Time{}, ""))
	assert.Equal(t, "<time>never</time>", zero)
	assert.Equal(t, `<time class="act__when">never</time>`, string(stamp(now, time.Time{}, "act__when")))
}

func TestPresenceLabel(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Not seen", presenceLabel(now, time.Time{}))
	assert.Equal(t, "Seen recently", presenceLabel(now, now.Add(-2*time.Minute)))
	assert.Equal(t, "Seen recently", presenceLabel(now, now.Add(-50*time.Minute)))
	assert.Equal(t, "Quiet", presenceLabel(now, now.Add(-3*time.Hour)))
	assert.Equal(t, "Long quiet", presenceLabel(now, now.Add(-100*time.Hour)))
}

func TestDot(t *testing.T) {
	t.Parallel()

	seen := string(dot(now, now.Add(-30*time.Minute)))
	assert.Contains(t, seen, `role="img"`)
	assert.Contains(t, seen, `class="dot decay--recent"`)
	assert.Contains(t, seen, `aria-label="Seen recently — 30m ago"`, "the colour has a text alternative")

	// Nothing was ever seen: the status stands alone, with no relative time.
	assert.Contains(t, string(dot(now, time.Time{})), `aria-label="Not seen"`)
}

func TestWindowWords(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a few minutes", windowWords(0))
	assert.Equal(t, "1 minute", windowWords(time.Minute))
	assert.Equal(t, "15 minutes", windowWords(15*time.Minute))
	assert.Equal(t, "1 hour", windowWords(time.Hour))
	assert.Equal(t, "2 hours", windowWords(2*time.Hour))
	assert.Equal(t, "90 minutes", windowWords(90*time.Minute))
}

func TestDecay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"never seen", time.Time{}, "decay--cold"},
		{"moments ago", now.Add(-time.Second), "decay--fresh"},
		{"within the hour", now.Add(-30 * time.Minute), "decay--recent"},
		{"within the day", now.Add(-6 * time.Hour), "decay--stale"},
		{"longer than that", now.Add(-100 * time.Hour), "decay--cold"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, decay(now, tc.at))
		})
	}
}

func TestPct(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "50.00", pct(1, 2))
	assert.Equal(t, "33.33", pct(1, 3))
	assert.Equal(t, "100", pct(3, 3))

	// An empty inventory divides by nothing, and a share cannot exceed the
	// whole, so neither case reaches the arithmetic.
	assert.Equal(t, "0", pct(0, 0))
	assert.Equal(t, "0", pct(5, 0))
	assert.Equal(t, "0", pct(0, 10))
	assert.Equal(t, "100", pct(11, 10))
}

func TestDash(t *testing.T) {
	t.Parallel()

	assert.Equal(t, em, dash(""))
	assert.Equal(t, "printer.local", dash("printer.local"))
}

func TestTook(t *testing.T) {
	t.Parallel()

	start := now

	assert.Equal(t, "1.5s", took(&inventory.Scan{StartedAt: start, FinishedAt: start.Add(1500 * time.Millisecond)}))
	assert.Equal(t, "12ms", took(&inventory.Scan{StartedAt: start, FinishedAt: start.Add(12 * time.Millisecond)}))

	// A scan still running has taken no time yet, which is not the same as
	// having taken none.
	assert.Equal(t, em, took(&inventory.Scan{StartedAt: start}))
}

func TestScanFound(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "12 hosts", scanFound(&inventory.Scan{Kind: dbtype.ScanDiscovery, Found: 12}))
	assert.Equal(t, "8 ports", scanFound(&inventory.Scan{Kind: dbtype.ScanPorts, Found: 8}))
	assert.Equal(t, "40 records", scanFound(&inventory.Scan{Kind: dbtype.ScanImport, Found: 40}))
}

func TestSourceKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Lease status", sourceKey("dhcp_status"))
	assert.Equal(t, "Dynamic ARP", sourceKey("arp_dynamic"))

	// A key with no wording falls back to its own name, de-underscored.
	assert.Equal(t, "Vlan pool", sourceKey("vlan_pool"))
}

func TestPhrase(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "was discovered", phrase(dbtype.EventDeviceDiscovered))
	assert.Equal(t, "was identified", phrase(dbtype.EventDeviceIdentified))
	assert.Equal(t, "was relabelled", phrase(dbtype.EventHostnameChanged))
	assert.Equal(t, "was edited", phrase(dbtype.EventDeviceEdited))
	assert.Equal(t, "picked up a new address", phrase(dbtype.EventAddressAdded))
	assert.Equal(t, "let go of an address", phrase(dbtype.EventAddressReleased))
	assert.Equal(t, "began answering on", phrase(dbtype.EventPortOpened))
	assert.Equal(t, "stopped answering on", phrase(dbtype.EventPortClosed))

	// events.kind carries no CHECK, so a kind added in Go without a phrase here
	// still has to render as something, and its own name is the most truthful
	// fallback.
	assert.Equal(t, "group assigned", phrase(dbtype.EventKind("GROUP_ASSIGNED")))
}

func TestTone(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "act--arrival", tone(dbtype.EventDeviceDiscovered))
	assert.Equal(t, "act--learned", tone(dbtype.EventDeviceIdentified))
	assert.Equal(t, "act--learned", tone(dbtype.EventAddressAdded))
	assert.Equal(t, "act--shape", tone(dbtype.EventDevicesMerged))
	assert.Equal(t, "act--shape", tone(dbtype.EventAddressReleased))
	assert.Equal(t, "act--edit", tone(dbtype.EventDeviceEdited))
	assert.Equal(t, "act--learned", tone(dbtype.EventPortOpened))
	assert.Equal(t, "act--shape", tone(dbtype.EventPortClosed))

	// Anything not worded yet reads as an edit rather than as nothing.
	assert.Equal(t, "act--edit", tone(dbtype.EventKind("GROUP_ASSIGNED")))
}

func TestEventIcon(t *testing.T) {
	t.Parallel()

	assert.Equal(t, glyphs[dbtype.EventDeviceDiscovered], eventIcon(dbtype.EventDeviceDiscovered))
	assert.Equal(t, glyphs[dbtype.EventPortOpened], eventIcon(dbtype.EventPortOpened))

	// A kind with no glyph of its own falls back rather than rendering an empty
	// tile, which would read as a rendering fault.
	assert.Equal(t, glyphs[dbtype.EventDeviceEdited], eventIcon(dbtype.EventKind("GROUP_ASSIGNED")))
}

func TestHealth(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "dot--quiet", health(nil))
	assert.Equal(t, "dot--quiet", health(&inventory.Network{}))
	assert.Equal(t, "dot--ok", health(&inventory.Network{Total: 12, Online: 12}))
	assert.Equal(t, "dot--ok", health(&inventory.Network{Total: 12, Online: 10, Offline: 2}))
	assert.Equal(t, "dot--warn", health(&inventory.Network{Total: 12, Online: 7, Offline: 5}))
}

func TestHealthLabel(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Nothing recorded yet", healthLabel(nil))
	assert.Equal(t, "Nothing recorded yet", healthLabel(&inventory.Network{}))
	assert.Equal(t, "Mostly answering", healthLabel(&inventory.Network{Total: 12, Online: 10, Offline: 2}))
	assert.Equal(t, "A third or more quiet", healthLabel(&inventory.Network{Total: 12, Online: 7, Offline: 5}))
}

func TestStatusClass(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "chip chip--ok", statusClass(dbtype.StatusOK))
	assert.Equal(t, "chip chip--fail", statusClass(dbtype.StatusFailed))
	assert.Equal(t, "chip chip--quiet", statusClass(dbtype.StatusCancelled))
	assert.Equal(t, "chip chip--brand", statusClass(dbtype.StatusRunning))
}

func TestChange(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "old → new", change(&inventory.Event{OldValue: "old", NewValue: "new"}))
	assert.Equal(t, "192.0.2.10", change(&inventory.Event{NewValue: "192.0.2.10"}))
	assert.Equal(t, "device 2 folded into 1", change(&inventory.Event{Detail: "device 2 folded into 1"}))

	// A discovery changed nothing; it is the thing that happened.
	assert.Empty(t, change(&inventory.Event{}))

	// A port event names the port and, where the port is a familiar one, the
	// service, from whichever value the flip wrote.
	assert.Equal(t, "port 22 (ssh)",
		change(&inventory.Event{Kind: dbtype.EventPortOpened, NewValue: "22", Detail: "ssh"}))
	assert.Equal(t, "port 44321",
		change(&inventory.Event{Kind: dbtype.EventPortClosed, OldValue: "44321"}))

	// An edit names the field, since the user owns several of them.
	edit := &inventory.Event{Kind: dbtype.EventDeviceEdited, Detail: "label"}

	edit.NewValue = "Office printer"
	assert.Equal(t, "label: Office printer", change(edit))

	edit.OldValue = "Printer"
	assert.Equal(t, "label: Printer → Office printer", change(edit))

	// Emptying a field is a change, not the setting of the value that went away.
	edit.NewValue = ""
	assert.Equal(t, "label: Printer → cleared", change(edit))
}

// The map is what the templates are parsed against, so a helper renamed in one
// place and not the other is a runtime failure otherwise.
func TestFuncsCoverEveryHelperTheTemplatesUse(t *testing.T) {
	t.Parallel()

	registered := funcs(func() time.Time { return now })

	for _, name := range []string{"ago", "stamp", "decay", "dot", "healthLabel", "dash", "pct", "took", "found", "sourcekey", "phrase", "tone", "eventIcon", "health", "statusClass", "change"} {
		assert.Contains(t, registered, name)
	}
}
