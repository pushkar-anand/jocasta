package web

import (
	"html/template"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/version"
)

// Decay buckets. Something last heard from a device at some point, and how long
// ago that was is what an operator reads a list for, so presence is shaded by
// age. The two greens end at the online window, where the label stops saying
// "Seen recently" and a watched device goes quiet, so the colour and the words
// change together; the buckets either side are fixed.
const (
	decayFresh = 5 * time.Minute
	decayStale = 24 * time.Hour
)

// em is the character shown where a value is absent. A blank cell reads as a
// rendering fault; this reads as "nothing known".
const em = "—"

// funcs are the template helpers. Everything here is presentation: a template
// should not be doing arithmetic or reaching for the clock. window is the
// store's online window, which decides when a dot's label says "Quiet".
func funcs(now func() time.Time, window time.Duration) template.FuncMap {
	return template.FuncMap{
		"stamp":        func(t time.Time, class string) template.HTML { return stamp(now(), t, class) },
		"since":        func(t time.Time) template.HTML { return since(now(), t) },
		"dot":          func(t time.Time) template.HTML { return dot(now(), t, window) },
		"healthLabel":  healthLabel,
		"dash":         dash,
		"pct":          pct,
		"took":         took,
		"found":        scanFound,
		"phrase":       inventory.Phrase,
		"tone":         tone,
		"eventIcon":    eventIcon,
		"health":       health,
		"statusClass":  statusClass,
		"change":       (*inventory.Event).Change,
		"addrs":        addrs,
		"standing":     standing,
		"sourcekey":    sourceKey,
		"classLabel":   classify.Class.Label,
		"classIcon":    classIcon,
		"classChoices": classChoices,
		"confidence":   confidence,
		"roledisplay":  roleDisplay,
		"scopedisplay": scopeDisplay,
		"permchoice":   permChoice,
		"scopechoice":  scopeChoice,
		"build":        currentBuild,
		"bytes":        humanBytes,
		"count":        humanCount,
		"proto":        protoName,
		"portList":     portList,
		"portsHead":    portsHead,
		"portsMore":    portsMore,
		"segtone":      segmentTone,
		"services":     mapServices,
	}
}

// rowPorts is how many open ports a device's row in the list shows. The rest
// are counted, with a link to the device's own Ports section.
const rowPorts = 6

// portsHead is the ports a device's row shows.
func portsHead(p []uint16) []uint16 { return p[:min(rowPorts, len(p))] }

// portsMore is how many ports a device's row leaves out.
func portsMore(p []uint16) int { return max(0, len(p)-rowPorts) }

// segmentTones is how many colours the map cycles through for its networks.
const segmentTones = 6

// segmentTone is the colour the map gives the i-th network.
func segmentTone(i int) int { return i % segmentTones }

// humanCount is a count with its thousands separated: "15,187".
func humanCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 0 {
		return "-" + humanCount(-n)
	}

	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}

	return s
}

// humanBytes is a byte count as a person reads one: "1.2 GB", "640 kB". Units
// are decimal, as network tools and ISPs count them.
func humanBytes(n int64) string {
	const unit = 1000

	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}

	v := float64(n)

	// An int64 runs out in the exabytes, so EB is the last step.
	for _, suffix := range []string{"kB", "MB", "GB", "TB", "PB", "EB"} {
		v /= unit
		if v < unit || suffix == "EB" {
			// One decimal while it says something, none once the number
			// carries the precision by itself.
			if v < 10 {
				return strconv.FormatFloat(v, 'f', 1, 64) + " " + suffix
			}

			return strconv.FormatFloat(v, 'f', 0, 64) + " " + suffix
		}
	}

	return strconv.FormatInt(n, 10) + " B"
}

// protoName names an IP protocol the way a port is usually written beside it.
// TCP is the default a reader assumes, so it is the one left unsaid.
func protoName(p uint8) string {
	switch p {
	case 6:
		return ""
	case 17:
		return "UDP"
	case 1, 58:
		return "ICMP"
	case 132:
		return "SCTP"
	default:
		return "protocol " + strconv.Itoa(int(p))
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
	case dbtype.HostnameFromMDNS:
		return "mDNS"
	case dbtype.HostnameFromNetBIOS:
		return "NetBIOS"
	case dbtype.HostnameFromDNSSD:
		return "DNS-SD"
	case dbtype.HostnameFromSSDP:
		return "UPnP"
	}

	// A standing added in Go and not yet worded here still has to render as
	// something, and its own name is the most truthful fallback.
	return strings.ToLower(strings.ReplaceAll(string(s), "_", " "))
}

// sourceKeys words the detail keys a source files a claim under, for a reader
// who has no reason to know a router's column names.
var sourceKeys = map[string]string{
	"interface":    "Interface",
	"arp_status":   "ARP status",
	"arp_dynamic":  "Dynamic ARP",
	"neigh_state":  "Neighbour state",
	"dhcp_server":  "DHCP server",
	"dhcp_status":  "Lease status",
	"dhcp_dynamic": "Dynamic lease",
	"dhcp_comment": "Lease note",
}

// sourceKey words one detail key. A key with no wording here still has to
// render as something, so its own name, de-underscored, is the fallback, as
// standing and phrase do.
func sourceKey(k string) string {
	if label, ok := sourceKeys[k]; ok {
		return label
	}

	s := strings.ReplaceAll(k, "_", " ")
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

// ago renders how long before now t was, at the coarsest useful precision. An
// operator reads "4m ago" to mean recently, and 4m12s would add nothing.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	d := now.Sub(t)

	switch {
	// A negative d is a clock difference between hosts, so it reads as "just
	// now" like any other sub-minute gap.
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

// stamp renders t as a <time> element: the coarse relative label ago gives, with
// the exact local time and zone on the title for anyone who needs the precise
// moment. A zero time has no moment to place, so it stays a bare word. class,
// when given, is a caller-set literal for the few timestamps that need styling.
func stamp(now, t time.Time, class string) template.HTML {
	var b strings.Builder

	b.WriteString("<time")

	if class != "" {
		b.WriteString(` class="`)
		b.WriteString(template.HTMLEscapeString(class))
		b.WriteString(`"`)
	}

	if t.IsZero() {
		b.WriteString(">never</time>")
	} else {
		local := t.Local()

		b.WriteString(` datetime="`)
		b.WriteString(local.Format(time.RFC3339))
		b.WriteString(`" title="`)
		b.WriteString(local.Format("Mon 2 Jan 2006, 15:04 MST"))
		b.WriteString(`">`)
		b.WriteString(ago(now, t))
		b.WriteString("</time>")
	}

	// Fixed element shape, timestamps straight from time.Format, and a class
	// that is always a template literal: nothing here is caller-supplied text.
	return template.HTML(b.String()) //nolint:gosec // G203: no user input in the parts
}

// presenceLabel is the spoken status behind a dot: the words the legends use, so
// the dot and the legend agree for a reader who only hears one of them.
// "Seen recently" ends at the online window, where the counts, the filter, a
// watched device's notification and decay's greens all draw the same line.
func presenceLabel(now, t time.Time, window time.Duration) string {
	if t.IsZero() {
		return "Not seen"
	}

	switch d := now.Sub(t); {
	case d < window:
		return "Seen recently"
	case d < decayStale:
		return "Quiet"
	}

	return "Long quiet"
}

// dot is a presence indicator: decay's shading with a text alternative, since
// the colour alone says nothing to a screen reader or a reader who cannot tell
// the two greens apart. The label pairs the coarse status with how long ago the
// last sighting was.
func dot(now, t time.Time, window time.Duration) template.HTML {
	label := presenceLabel(now, t, window)
	if !t.IsZero() {
		label += ": " + ago(now, t)
	}

	// Fixed element, class from decay, label from presenceLabel and ago:
	// every part is this package's own.
	return template.HTML(`<span class="dot ` + decay(now, t, window) + `" role="img" aria-label="` + //nolint:gosec // G203: no user input in the parts
		template.HTMLEscapeString(label) + `"></span>`)
}

// decay is the class naming how stale t is.
func decay(now, t time.Time, window time.Duration) string {
	if t.IsZero() {
		return "decay--cold"
	}

	switch d := now.Sub(t); {
	case d < min(decayFresh, window):
		return "decay--fresh"
	case d < window:
		return "decay--recent"
	case d < decayStale:
		return "decay--stale"
	}

	return "decay--cold"
}

// since renders t as a <time> element naming the moment, for "since" phrases
// where ago's "2d ago" would not read: the time alone today, the weekday and
// time within the week, the date before that. The title carries the exact
// local time and zone, as stamp's does.
func since(now, t time.Time) template.HTML {
	local, today := t.Local(), now.Local()

	var label string

	switch {
	case local.YearDay() == today.YearDay() && local.Year() == today.Year():
		label = local.Format("15:04")
	case today.Sub(local) < 6*24*time.Hour:
		label = local.Format("Mon 15:04")
	case local.Year() == today.Year():
		label = local.Format("2 Jan")
	default:
		label = local.Format("2 Jan 2006")
	}

	// Fixed element shape and timestamps straight from time.Format: nothing
	// here is caller-supplied text.
	return template.HTML(`<time datetime="` + local.Format(time.RFC3339) + //nolint:gosec // G203: no user input in the parts
		`" title="` + local.Format("Mon 2 Jan 2006, 15:04 MST") + `">` + label + `</time>`)
}

// dash renders an absent value as a dash.
func dash(s string) string {
	if s == "" {
		return em
	}

	return s
}

// pct is n as a percentage of total, for an SVG width. An empty inventory
// divides by nothing, so it reports zero and the bar stays empty.
func pct(n, total int) string {
	if total <= 0 || n <= 0 {
		return "0"
	}

	if n >= total {
		return "100"
	}

	return strconv.FormatFloat(float64(n)/float64(total)*100, 'f', 2, 64)
}

// took renders how long a scan ran. A scan still running has no duration yet,
// and renders differently from one that finished instantly.
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

// scanFound says what a scan's Found count counts, since a discovery sweep and
// a port scan put incommensurable numbers in the same column.
func scanFound(s *inventory.Scan) string {
	n := strconv.Itoa(s.Found)

	switch s.Kind {
	case dbtype.ScanPorts:
		return n + " ports"
	case dbtype.ScanImport:
		return n + " records"
	}

	return n + " hosts"
}

// tone is the tint a log line's icon carries. Kinds share colours by group:
// the colour says what sort of change it was (something arrived, something was
// learned, someone edited it), and six colours in a list would say nothing at
// all.
func tone(k dbtype.EventKind) string {
	switch k {
	case dbtype.EventDeviceQuiet:
		return "act--warn"
	case dbtype.EventDeviceDiscovered, dbtype.EventDeviceBack:
		return "act--arrival"
	case dbtype.EventDeviceIdentified, dbtype.EventAddressAdded, dbtype.EventPortOpened:
		return "act--learned"
	case dbtype.EventDevicesMerged, dbtype.EventHostnameChanged, dbtype.EventAddressReleased,
		dbtype.EventPortClosed, dbtype.EventDeviceClassified:
		return "act--shape"
	}

	// Everything the user did themselves, and anything not worded yet.
	return "act--edit"
}

// glyphs are the log icons, drawn on a 24px grid and stroked in currentColor so
// the tone class colours them. They are markup this package owns, which is what
// makes returning them as HTML safe.
var glyphs = map[dbtype.EventKind]template.HTML{
	dbtype.EventDeviceQuiet:      `<path d="M5 12.5a10 10 0 0114 0"/><path d="M8.5 16a5 5 0 017 0"/><path d="M12 19.5h.01"/><path d="M3 3l18 18"/>`,
	dbtype.EventDeviceBack:       `<path d="M2 8.8a15 15 0 0120 0"/><path d="M5 12.5a10 10 0 0114 0"/><path d="M8.5 16a5 5 0 017 0"/><path d="M12 19.5h.01"/>`,
	dbtype.EventDeviceDiscovered: `<circle cx="12" cy="12" r="9"/><path d="M12 8v8M8 12h8"/>`,
	dbtype.EventDeviceIdentified: `<circle cx="12" cy="12" r="9"/><path d="M8.5 12.5l2.5 2.5 4.5-5"/>`,
	dbtype.EventDevicesMerged:    `<path d="M7 4v4a5 5 0 005 5h6"/><path d="M15 10l3 3-3 3"/>`,
	dbtype.EventAddressAdded:     `<path d="M4 12h16M4 12l4-4M4 12l4 4M20 12l-4-4M20 12l-4 4"/>`,
	dbtype.EventAddressReleased:  `<path d="M14 5H5v14h9"/><path d="M19 12H9M19 12l-4-4M19 12l-4 4"/>`,
	dbtype.EventHostnameChanged:  `<path d="M20.5 12.5l-8-8H4v8.5l8 8a1.5 1.5 0 002 0l6.5-6.5a1.5 1.5 0 000-2z"/><circle cx="8" cy="8" r="1"/>`,
	dbtype.EventDeviceEdited:     `<path d="M4 20h4l10-10a2.8 2.8 0 10-4-4L4 16v4z"/>`,
	dbtype.EventPortOpened:       `<path d="M9 3v4M15 3v4"/><path d="M6 7h12v3a6 6 0 01-12 0z"/><path d="M12 16v5"/>`,
	dbtype.EventPortClosed:       `<path d="M6 7h12v3a6 6 0 01-12 0z"/><path d="M12 16v5"/><path d="M4 4l16 16"/>`,
	dbtype.EventDeviceClassified: `<path d="M4 4h7l9 9-7 7-9-9z"/><circle cx="8.5" cy="8.5" r="1.5"/>`,
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

// healthLabel is the spoken form of health, for a reader who only hears the
// network's status dot.
func healthLabel(n *inventory.Network) string {
	switch {
	case n == nil || n.Total == 0:
		return "Nothing recorded yet"
	case n.Offline*3 > n.Total:
		return "A third or more quiet"
	}

	return "Mostly answering"
}

// windowWords says a duration the way a caption would: the online window is
// configured as a Go duration, and "15m0s" is not a sentence.
func windowWords(d time.Duration) string {
	switch {
	case d <= 0:
		return "a few minutes"
	case d < time.Hour:
		if m := int(d.Minutes()); m != 1 {
			return strconv.Itoa(m) + " minutes"
		}

		return "1 minute"
	case d%time.Hour != 0:
		return strconv.Itoa(int(d.Minutes())) + " minutes"
	case d == time.Hour:
		return "1 hour"
	}

	return strconv.Itoa(int(d.Hours())) + " hours"
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

// build is the running binary's version for the footer, and the page of its
// release notes; URL is empty for a build that is not a release.
type build struct {
	Version string
	URL     string
}

var releaseVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

func currentBuild() build {
	return buildOf(version.Get().Version)
}

// buildOf links a release's version to its notes. A release built by
// GoReleaser carries "1.4.0", one built by go install "v1.4.0"; anything
// else, such as "dev", has no notes to link to.
func buildOf(v string) build {
	if !releaseVersion.MatchString(v) {
		return build{Version: v}
	}

	v = strings.TrimPrefix(v, "v")

	return build{Version: v, URL: "https://github.com/pushkar-anand/jocasta/releases/tag/v" + v}
}
