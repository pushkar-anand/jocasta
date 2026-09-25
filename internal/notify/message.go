package notify

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// MaxLines is how many changes one message lists before it counts the rest,
// so a large scan stays readable on a phone.
const MaxLines = 20

// Message is one notification: a title, a body of plain text lines, and the
// changes it describes, for a destination that takes them as data.
type Message struct {
	Title string
	Body  string

	// ScanID is the scan the changes came from; zero for a test.
	ScanID int64
	Events []*inventory.Event
}

// ForScan words what one scan changed, keeping only the events of the given
// kinds. It reports false when nothing is left to send.
//
// The first scan of a source and network finds every device at once, so it
// is sent as a count.
func ForScan(scanID int64, c *inventory.ScanChanges, kinds []dbtype.EventKind) (Message, bool) {
	events := slices.DeleteFunc(slices.Clone(c.Events), func(e *inventory.Event) bool {
		return !slices.Contains(kinds, e.Kind)
	})

	if len(events) == 0 {
		return Message{}, false
	}

	m := Message{ScanID: scanID, Events: events}

	if c.First && c.Kind == dbtype.ScanDiscovery {
		m.Title = "First scan " + where(c)
		m.Body = "Found " + count(c.Found, "device", "devices")

		return m, true
	}

	sortByKind(events)

	lines := make([]string, 0, min(len(events), MaxLines)+1)
	for _, e := range events[:min(len(events), MaxLines)] {
		lines = append(lines, Line(e))
	}

	if more := len(events) - MaxLines; more > 0 {
		lines = append(lines, fmt.Sprintf("and %d more", more))
	}

	m.Body = strings.Join(lines, "\n")

	if allOf(events, dbtype.EventDeviceDiscovered) {
		m.Title = count(len(events), "new device", "new devices") + " " + where(c)
	} else {
		m.Title = count(len(events), "change", "changes") + " " + where(c)
	}

	return m, true
}

// Line words one change. A new device is named with its address, since the
// title already says it is new; any other change reads as the activity log
// words it.
func Line(e *inventory.Event) string {
	if e.Kind == dbtype.EventDeviceDiscovered {
		return strings.Join(nonEmpty(e.DeviceName, e.NewValue), " · ")
	}

	return strings.Join(nonEmpty(e.DeviceName, inventory.Phrase(e.Kind), e.Change()), " ")
}

// Test returns a sample message, for checking that a destination works.
func Test() Message {
	return Message{
		Title: "Jocasta test",
		Body:  "Notifications from Jocasta reach this destination.",
	}
}

// where says where a scan looked: its network, its source for one that read
// every network at once, or the port scan.
func where(c *inventory.ScanChanges) string {
	switch {
	case c.Kind == dbtype.ScanPorts:
		return "in the port scan"
	case c.Network != "":
		return "on " + c.Network
	}

	return "from " + c.Source
}

// sortByKind groups events by kind, in the order dbtype.EventKinds lists
// them, so new devices lead; within a kind they keep the order they happened
// in.
func sortByKind(events []*inventory.Event) {
	rank := make(map[dbtype.EventKind]int)
	for i, k := range dbtype.EventKinds() {
		rank[k] = i
	}

	slices.SortStableFunc(events, func(a, b *inventory.Event) int {
		return cmp.Compare(rank[a.Kind], rank[b.Kind])
	})
}

func allOf(events []*inventory.Event, k dbtype.EventKind) bool {
	for _, e := range events {
		if e.Kind != k {
			return false
		}
	}

	return true
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return fmt.Sprintf("%d %s", n, many)
}

func nonEmpty(parts ...string) []string {
	return slices.DeleteFunc(parts, func(s string) bool { return s == "" })
}
