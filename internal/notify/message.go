package notify

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// MaxLines is how many changes one message lists. For the rest, the message
// gives only a count. So a large scan stays readable on a phone.
const MaxLines = 20

// Message is one notification: a title, a body of plain text lines, and the
// changes that it describes, for a destination that takes them as data.
type Message struct {
	Title string
	Body  string

	// ScanID is the scan that the changes came from. It is zero for a test.
	ScanID int64
	Events []*inventory.Event
}

// ForScan writes the text for the changes of one scan. It keeps only the
// events of the given kinds. It reports false when nothing is left to send.
//
// The first scan of a source and network into an empty inventory finds every
// device at once. So if a first scan found only new devices, the message is a
// count. If a first scan also found known devices, the message is the same as
// for any other scan. So it names the new devices.
func ForScan(scanID int64, c *inventory.ScanChanges, kinds []dbtype.EventKind) (Message, bool) {
	events := slices.DeleteFunc(slices.Clone(c.Events), func(e *inventory.Event) bool {
		return !slices.Contains(kinds, e.Kind)
	})

	if len(events) == 0 {
		return Message{}, false
	}

	m := Message{ScanID: scanID, Events: events}

	if c.First && c.Kind == dbtype.ScanDiscovery && allNew(c) {
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

	switch {
	case allOf(events, dbtype.EventDeviceDiscovered):
		m.Title = count(len(events), "new device", "new devices") + " " + where(c)
	case allOf(events, dbtype.EventDeviceQuiet):
		m.Title = count(len(events), "device went quiet", "devices went quiet") + " " + where(c)
	case allOf(events, dbtype.EventDeviceBack):
		m.Title = count(len(events), "device came back", "devices came back") + " " + where(c)
	default:
		m.Title = count(len(events), "change", "changes") + " " + where(c)
	}

	return m, true
}

// Line writes the text for one change. For a new device, it gives the name and
// address, because the title already tells that it is new. Any other change
// has the text of the activity log.
func Line(e *inventory.Event) string {
	if e.Kind == dbtype.EventDeviceDiscovered {
		return strings.Join(nonEmpty(e.DeviceName, e.NewValue), " · ")
	}

	return strings.Join(nonEmpty(e.DeviceName, inventory.Phrase(e.Kind), e.Change()), " ")
}

// Test returns a sample message. Use it to check that a destination works.
func Test() Message {
	return Message{
		Title: "Jocasta test",
		Body:  "Notifications from Jocasta reach this destination.",
	}
}

// where tells where a scan looked: its network, or its source for a scan that
// read every network at once, or the port scan.
func where(c *inventory.ScanChanges) string {
	switch {
	case c.Kind == dbtype.ScanPorts:
		return "in the port scan"
	case c.Network != "":
		return "on " + c.Network
	}

	return "from " + c.Source
}

// sortByKind groups events by kind, in the order of dbtype.EventKinds. So a
// watched device that goes quiet comes first. In a kind, events keep the order
// in which they happened.
func sortByKind(events []*inventory.Event) {
	rank := make(map[dbtype.EventKind]int)
	for i, k := range dbtype.EventKinds() {
		rank[k] = i
	}

	slices.SortStableFunc(events, func(a, b *inventory.Event) int {
		return cmp.Compare(rank[a.Kind], rank[b.Kind])
	})
}

// allNew reports whether every device that the scan found is new to the
// inventory. It counts every discovery of the scan, for all kinds that the
// message keeps.
func allNew(c *inventory.ScanChanges) bool {
	discovered := 0

	for _, e := range c.Events {
		if e.Kind == dbtype.EventDeviceDiscovered {
			discovered++
		}
	}

	return discovered == c.Found
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
