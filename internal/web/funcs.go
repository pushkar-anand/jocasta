package web

import (
	"html/template"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// Decay buckets. A device is not simply present or absent: something last heard
// from it at some point, and how long ago that was is what an operator reads a
// list for. The thresholds are absolute rather than taken from the configured
// online window, so the shading means the same thing whatever the sweeps do.
const (
	decayFresh  = 5 * time.Minute
	decayRecent = time.Hour
	decayStale  = 24 * time.Hour
)

// em is the character shown where a value is absent. A blank cell reads as a
// rendering fault; this reads as "nothing known".
const em = "—"

// funcs are the template helpers. Everything here is presentation: a template
// should not be doing arithmetic or reaching for the clock.
func funcs(now func() time.Time) template.FuncMap {
	return template.FuncMap{
		"ago":         func(t time.Time) string { return ago(now(), t) },
		"decay":       func(t time.Time) string { return decay(now(), t) },
		"dash":        dash,
		"pct":         pct,
		"took":        took,
		"phrase":      phrase,
		"tone":        tone,
		"eventIcon":   eventIcon,
		"health":      health,
		"statusClass": statusClass,
		"change":      change,
		"addrs":       addrs,
		"standing":    standing,
	}
}

// standing words where a claimed name came from, for a reader who has no reason
// to know what DHCP_STATIC means.
func standing(s dbtype.HostnameSource) string {
	switch s {
	case dbtype.HostnameFromDNS:
		return "reverse DNS"
	case dbtype.HostnameFromDHCPStatic:
		return "static lease"
	case dbtype.HostnameFromDHCPLease:
		return "DHCP lease"
	}

	// A standing added in Go and not yet worded here still has to render as
	// something, and its own name is the most truthful fallback.
	return strings.ToLower(strings.ReplaceAll(string(s), "_", " "))
}

// ago renders how long before now t was, at the coarsest useful precision. An
// operator reads "4m ago" to mean recently, not to know it was 4m12s.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	d := now.Sub(t)

	switch {
	case d < 0:
		// A clock difference, not a sighting from the future.
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < decayStale:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	case d < 7*decayStale:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}

	return t.Format("2 Jan 2006")
}

// decay is the class naming how stale t is.
func decay(now, t time.Time) string {
	if t.IsZero() {
		return "decay--cold"
	}

	switch d := now.Sub(t); {
	case d < decayFresh:
		return "decay--fresh"
	case d < decayRecent:
		return "decay--recent"
	case d < decayStale:
		return "decay--stale"
	}

	return "decay--cold"
}

// dash renders an absent value as a dash.
func dash(s string) string {
	if s == "" {
		return em
	}

	return s
}

// pct is n as a percentage of total, for an SVG width. An empty inventory
// divides by nothing, so it reports zero rather than filling the bar.
func pct(n, total int) string {
	if total <= 0 || n <= 0 {
		return "0"
	}

	if n >= total {
		return "100"
	}

	return strconv.FormatFloat(float64(n)/float64(total)*100, 'f', 2, 64)
}

// took renders how long a scan ran. A scan still running has taken no time yet,
// which is not the same as having taken none.
func took(s *inventory.Scan) string {
	if s == nil {
		return em
	}

	d := s.Took()
	if d == 0 {
		return em
	}

	if d < time.Second {
		return d.Truncate(time.Millisecond).String()
	}

	return d.Truncate(100 * time.Millisecond).String()
}

// phrase renders a stored event kind as what it did, worded for a reader. The
// kinds are written for the log, in the schema's upper case; a log line reads
// as a sentence about the device that precedes it.
func phrase(k dbtype.EventKind) string {
	switch k {
	case dbtype.EventDeviceDiscovered:
		return "was discovered"
	case dbtype.EventDeviceIdentified:
		return "was identified"
	case dbtype.EventDevicesMerged:
		return "merged with a duplicate record"
	case dbtype.EventAddressAdded:
		return "picked up a new address"
	case dbtype.EventHostnameChanged:
		return "was relabelled"
	case dbtype.EventDeviceEdited:
		return "was edited"
	}

	// A kind added in Go and not yet worded here still has to render as
	// something, and its own name is the most truthful fallback.
	return strings.ToLower(strings.ReplaceAll(string(k), "_", " "))
}

// tone is the tint a log line's icon carries. Kinds are grouped rather than
// coloured one apiece: the colour says what sort of change it was -- something
// arrived, something was learned, someone edited it -- and six colours in a
// list would say nothing at all.
func tone(k dbtype.EventKind) string {
	switch k {
	case dbtype.EventDeviceDiscovered:
		return "act--arrival"
	case dbtype.EventDeviceIdentified, dbtype.EventAddressAdded:
		return "act--learned"
	case dbtype.EventDevicesMerged, dbtype.EventHostnameChanged:
		return "act--shape"
	}

	// Everything the user did themselves, and anything not worded yet.
	return "act--edit"
}

// glyphs are the log icons, drawn on a 24px grid and stroked in currentColor so
// the tone class colours them. They are markup this package owns, not anything
// a caller supplies, which is what makes returning them as HTML safe.
var glyphs = map[dbtype.EventKind]template.HTML{
	dbtype.EventDeviceDiscovered: `<circle cx="12" cy="12" r="9"/><path d="M12 8v8M8 12h8"/>`,
	dbtype.EventDeviceIdentified: `<circle cx="12" cy="12" r="9"/><path d="M8.5 12.5l2.5 2.5 4.5-5"/>`,
	dbtype.EventDevicesMerged:    `<path d="M7 4v4a5 5 0 005 5h6"/><path d="M15 10l3 3-3 3"/>`,
	dbtype.EventAddressAdded:     `<path d="M4 12h16M4 12l4-4M4 12l4 4M20 12l-4-4M20 12l-4 4"/>`,
	dbtype.EventHostnameChanged:  `<path d="M20.5 12.5l-8-8H4v8.5l8 8a1.5 1.5 0 002 0l6.5-6.5a1.5 1.5 0 000-2z"/><circle cx="8" cy="8" r="1"/>`,
	dbtype.EventDeviceEdited:     `<path d="M4 20h4l10-10a2.8 2.8 0 10-4-4L4 16v4z"/>`,
}

// eventIcon is the glyph for a kind. A kind with no glyph of its own gets the
// edit mark, which is what an unworded kind most likely is.
func eventIcon(k dbtype.EventKind) template.HTML {
	if g, ok := glyphs[k]; ok {
		return g
	}

	return glyphs[dbtype.EventDeviceEdited]
}

// health is the class colouring a network's status dot. A prefix with nothing
// quiet on it is answering; one where more than a third has gone quiet is worth
// a second look; one nothing has ever been found on is neither.
func health(n *inventory.Network) string {
	switch {
	case n == nil || n.Total == 0:
		return "dot--quiet"
	case n.Offline*3 > n.Total:
		return "dot--warn"
	}

	return "dot--ok"
}

// statusClass is the chip a scan status is drawn as. Cancelled is not failure,
// but it is the same thing to read for: the sweep has no result behind it.
func statusClass(s dbtype.ScanStatus) string {
	switch s {
	case dbtype.StatusFailed:
		return "chip chip--fail"
	case dbtype.StatusCancelled:
		return "chip chip--quiet"
	case dbtype.StatusRunning:
		return "chip chip--brand"
	}

	return "chip chip--ok"
}

// addrs lists the addresses a device holds. They arrive already ordered, since
// ordering them is something SQL cannot do over a TEXT column.
func addrs(list []netip.Addr) string {
	if len(list) == 0 {
		return em
	}

	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.String())
	}

	return strings.Join(out, ", ")
}

// change describes what an event changed, where it changed a value. An event
// that changed nothing -- a discovery -- has nothing to show here.
func change(e *inventory.Event) string {
	if e == nil {
		return ""
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
