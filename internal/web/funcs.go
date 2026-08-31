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
		"event":       eventLabel,
		"statusClass": statusClass,
		"change":      change,
		"addrs":       addrs,
	}
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
func took(s inventory.Scan) string {
	d := s.Took()
	if d == 0 {
		return em
	}

	if d < time.Second {
		return d.Truncate(time.Millisecond).String()
	}

	return d.Truncate(100 * time.Millisecond).String()
}

// eventLabel renders a stored event kind as a phrase. The kinds are written for
// the log, in the schema's upper case; this is the same fact worded for a
// reader.
func eventLabel(k dbtype.EventKind) string {
	switch k {
	case dbtype.EventDeviceDiscovered:
		return "discovered"
	case dbtype.EventDeviceIdentified:
		return "identified"
	case dbtype.EventDevicesMerged:
		return "merged"
	case dbtype.EventAddressAdded:
		return "address"
	case dbtype.EventHostnameChanged:
		return "renamed"
	}

	// A kind added in Go and not yet worded here still has to render as
	// something, and its own name is the most truthful fallback.
	return strings.ToLower(strings.ReplaceAll(string(k), "_", " "))
}

// statusClass is the class that colours a scan status. Only failure is
// coloured; a scan that worked needs no emphasis.
func statusClass(s dbtype.ScanStatus) string {
	switch s {
	case dbtype.StatusFailed, dbtype.StatusCancelled:
		return "status status--failed"
	case dbtype.StatusRunning:
		return "status status--running"
	}

	return "status"
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
func change(e inventory.Event) string {
	switch {
	case e.OldValue != "" && e.NewValue != "":
		return e.OldValue + " → " + e.NewValue
	case e.NewValue != "":
		return e.NewValue
	case e.Detail != "":
		return e.Detail
	}

	return ""
}
