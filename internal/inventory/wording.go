package inventory

import (
	"cmp"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// wording is the text of each kind of event for a person. It is in one table.
// So a new kind gets text for the log and for notification settings at the
// same time.
var wording = map[dbtype.EventKind]struct {
	// phrase is what the event says a device did.
	phrase string

	// label names the kind as a thing to be told about.
	label string
}{
	dbtype.EventDeviceQuiet:      {"went quiet", "Watched device went quiet"},
	dbtype.EventDeviceBack:       {"came back", "Watched device came back"},
	dbtype.EventDeviceDiscovered: {"was discovered", "New device"},
	dbtype.EventDeviceIdentified: {"was identified", "Hardware address learned"},
	dbtype.EventDevicesMerged:    {"was merged with a duplicate", "Duplicates merged"},
	dbtype.EventAddressAdded:     {"got a new address", "New address"},
	dbtype.EventAddressReleased:  {"dropped an address", "Address dropped"},
	dbtype.EventHostnameChanged:  {"changed its hostname", "Hostname changed"},
	dbtype.EventDeviceEdited:     {"was edited", "Device edited"},
	dbtype.EventPortOpened:       {"started listening on", "Port opened"},
	dbtype.EventPortClosed:       {"stopped listening on", "Port closed"},
	dbtype.EventDeviceClassified: {"was reclassified", "Type changed"},
}

// Phrase returns what an event of kind k says that a device did, such as "was
// discovered". A line of the log is the name of the device, then the phrase.
// For a kind with no text, it returns the kind in lower case with spaces.
func Phrase(k dbtype.EventKind) string {
	if w, ok := wording[k]; ok {
		return w.phrase
	}

	// A kind added in Go with no text here yet must still render. Its own
	// name is the most true fallback.
	return strings.ToLower(strings.ReplaceAll(string(k), "_", " "))
}

// Label returns the name of kind k as a thing to be told about, such as "New
// device". For a kind with no text, it uses its [Phrase].
func Label(k dbtype.EventKind) string {
	if w, ok := wording[k]; ok {
		return w.label
	}

	return Phrase(k)
}

// Change describes what an event changed, if it changed a value. An event that
// changed nothing, such as a discovery, has nothing to show here.
func (e *Event) Change() string {
	if e == nil {
		return ""
	}

	// A port event has the number in the value that changed. If the port has
	// a known service, the detail has its name. Neither is a before and
	// after. So the text comes from here.
	if e.Kind == dbtype.EventPortOpened || e.Kind == dbtype.EventPortClosed {
		port := cmp.Or(e.NewValue, e.OldValue)
		switch {
		case port == "":
			return ""
		case e.Detail != "":
			return "port " + port + " (" + e.Detail + ")"
		default:
			return "port " + port
		}
	}

	if e.Kind == dbtype.EventDeviceQuiet || e.Kind == dbtype.EventDeviceBack {
		return presenceChange(e)
	}

	// A released address has nothing after it. "→ cleared" would look as if
	// the user emptied a field.
	if e.Kind == dbtype.EventAddressReleased {
		return e.OldValue
	}

	// A class is stored as its identifier. The log shows the name from the
	// device page.
	if e.Kind == dbtype.EventDeviceClassified {
		return (&Event{
			OldValue: classify.Class(e.OldValue).Label(),
			NewValue: classify.Class(e.NewValue).Label(),
		}).Change()
	}

	// The same for a type that the user set, under the name of the field.
	if e.Kind == dbtype.EventDeviceEdited && e.Detail == "type" {
		return "type: " + (&Event{OldValue: classLabel(e.OldValue), NewValue: classLabel(e.NewValue)}).Change()
	}

	// An edit tells its field, because the user owns several. The event of a
	// scan is about the one thing that its kind can change.
	var field string
	if e.Kind == dbtype.EventDeviceEdited && e.Detail != "" {
		field = e.Detail + ": "
	}

	switch {
	case e.OldValue != "" && e.NewValue != "":
		return field + e.OldValue + " → " + e.NewValue
	case e.NewValue != "":
		return field + e.NewValue

	// To empty a field is a change. If not, the log would show the removed
	// value as if it was just set.
	case e.OldValue != "":
		return field + e.OldValue + " → cleared"

	case e.Detail != "":
		return e.Detail
	}

	return ""
}

// classLabel is the name of a class identifier. It is empty for an empty
// identifier.
func classLabel(id string) string {
	if id == "" {
		return ""
	}

	return classify.Class(id).Label()
}
