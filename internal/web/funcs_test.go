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

func TestEventLabel(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "discovered", eventLabel(dbtype.EventDeviceDiscovered))
	assert.Equal(t, "identified", eventLabel(dbtype.EventDeviceIdentified))
	assert.Equal(t, "renamed", eventLabel(dbtype.EventHostnameChanged))
	assert.Equal(t, "edited", eventLabel(dbtype.EventDeviceEdited))

	// events.kind carries no CHECK, so a kind added in Go without a phrase here
	// still has to render as something, and its own name is the most truthful
	// fallback.
	assert.Equal(t, "ports scanned", eventLabel(dbtype.EventKind("PORTS_SCANNED")))
}

func TestStatusClass(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "status", statusClass(dbtype.StatusOK))
	assert.Equal(t, "status status--failed", statusClass(dbtype.StatusFailed))
	assert.Equal(t, "status status--failed", statusClass(dbtype.StatusCancelled))
	assert.Equal(t, "status status--running", statusClass(dbtype.StatusRunning))
}

func TestChange(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "old → new", change(&inventory.Event{OldValue: "old", NewValue: "new"}))
	assert.Equal(t, "192.0.2.10", change(&inventory.Event{NewValue: "192.0.2.10"}))
	assert.Equal(t, "device 2 folded into 1", change(&inventory.Event{Detail: "device 2 folded into 1"}))

	// A discovery changed nothing; it is the thing that happened.
	assert.Empty(t, change(&inventory.Event{}))

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

	for _, name := range []string{"ago", "decay", "dash", "pct", "took", "event", "statusClass", "change"} {
		assert.Contains(t, registered, name)
	}
}
