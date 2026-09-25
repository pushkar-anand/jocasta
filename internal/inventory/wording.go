package inventory

import (
	"cmp"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// wording is how each kind of event reads to a person, kept in one table so
// a new kind is worded for the log and for notification settings together.
var wording = map[dbtype.EventKind]struct {
	// phrase is what the event says a device did.
	phrase string

	// label names the kind as a thing to be told about.
	label string
}{
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

// Phrase returns what an event of kind k says a device did, such as "was
// discovered". A line of the log reads as the device's name followed by it.
// A kind with no wording of its own is returned in lower case with spaces.
func Phrase(k dbtype.EventKind) string {
	if w, ok := wording[k]; ok {
		return w.phrase
	}

	// A kind added in Go and not yet worded here still has to render as
	// something, and its own name is the most truthful fallback.
	return strings.ToLower(strings.ReplaceAll(string(k), "_", " "))
}

// Label returns the name of kind k as a thing to be told about, such as "New
// device". A kind with no wording of its own falls back to its [Phrase].
func Label(k dbtype.EventKind) string {
	if w, ok := wording[k]; ok {
		return w.label
	}

	return Phrase(k)
}

// Change describes what an event changed, where it changed a value. An event
// that changed nothing, such as a discovery, has nothing to show here.
func (e *Event) Change() string {
	if e == nil {
		return ""
	}

	// A port event carries the number in whichever value changed and the
	// service name, where the port has a familiar one, in the detail. Neither
	// reads as a before and after, so it is worded here.
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

	// A released address has nothing after it, and "→ cleared" would read as
	// though the user emptied a field.
	if e.Kind == dbtype.EventAddressReleased {
		return e.OldValue
	}

	// A class is stored as its identifier; the log shows the name the device
	// page uses for it.
	if e.Kind == dbtype.EventDeviceClassified {
		return (&Event{
			OldValue: classify.Class(e.OldValue).Label(),
			NewValue: classify.Class(e.NewValue).Label(),
		}).Change()
	}

	// An edit says which field it was about, since the user owns several. A
	// scan's event is about the one thing that kind of event can change.
	var field string
	if e.Kind == dbtype.EventDeviceEdited && e.Detail != "" {
		field = e.Detail + ": "
	}

	switch {
	case e.OldValue != "" && e.NewValue != "":
		return field + e.OldValue + " → " + e.NewValue
	case e.NewValue != "":
		return field + e.NewValue

	// Emptying a field is a change, and the log would otherwise show the value
	// that went away as though it had just been set.
	case e.OldValue != "":
		return field + e.OldValue + " → cleared"

	case e.Detail != "":
		return e.Detail
	}

	return ""
}
