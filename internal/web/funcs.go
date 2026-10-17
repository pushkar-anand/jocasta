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

// Decay buckets. An operator reads a list for how long ago a source last heard
// from a device. So the shade of presence shows the age. The two greens end at
// the online window. There, the label stops saying "Seen recently", and a
// watched device goes quiet. So the colour and the words change together. The
// buckets on each side are fixed.
const (
	decayFresh = 5 * time.Minute
	decayStale = 24 * time.Hour
)

// em is the character for an absent value. A blank cell looks like a rendering
// fault. This means "nothing known".
const em = "—"

// funcs are the template helpers. Everything here is presentation. A template
// must not do arithmetic or read the clock. window is the online window of
// the store. It decides when the label of a dot says "Quiet".
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

// rowPorts is how many open ports the row of a device in the list shows. The
// rest only count, with a link to the Ports section of the device.
const rowPorts = 6

// portsHead is the ports that the row of a device shows.
func portsHead(p []uint16) []uint16 { return p[:min(rowPorts, len(p))] }

// portsMore is how many ports the row of a device leaves out.
func portsMore(p []uint16) int { return max(0, len(p)-rowPorts) }

// segmentTones is how many colours the map uses in turn for its networks.
const segmentTones = 6

// segmentTone is the colour that the map gives the i-th network.
func segmentTone(i int) int { return i % segmentTones }

// humanCount is a count with separated thousands: "15,187".
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

// humanBytes is a byte count as a person reads it: "1.2 GB", "640 kB". Units
// are decimal, as network tools and ISPs count them.
func humanBytes(n int64) string {
	const unit = 1000

	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}

	v := float64(n)

	// An int64 ends in the exabytes. So EB is the last step.
	for _, suffix := range []string{"kB", "MB", "GB", "TB", "PB", "EB"} {
		v /= unit
		if v < unit || suffix == "EB" {
			// One decimal while it adds information. None when the number
			// has the precision by itself.
			if v < 10 {
				return strconv.FormatFloat(v, 'f', 1, 64) + " " + suffix
			}

			return strconv.FormatFloat(v, 'f', 0, 64) + " " + suffix
		}
	}

	return strconv.FormatInt(n, 10) + " B"
}

// protoName names an IP protocol, as it is usually written next to a port. A
// reader expects TCP by default. So TCP is not written.
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

// standing writes the origin of a claimed name in words, for a reader who does
// not know what DHCP_STATIC means.
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

	// A standing added in Go with no text here yet must still render. Its own
	// name is the most true fallback.
	return strings.ToLower(strings.ReplaceAll(string(s), "_", " "))
}

// sourceKeys gives words for the detail keys of a claim, for a reader who does
// not know the column names of a router.
var sourceKeys = map[string]string{
	"interface":    "Interface",
	"arp_status":   "ARP status",
	"arp_dynamic":  "Dynamic ARP",
	"neigh_state":  "Neighbour state",
	"dhcp_server":  "DHCP server",
	"dhcp_status":  "Lease status",
	"dhcp_dynamic": "Dynamic lease",
	"dhcp_comment": "Lease note",
	"reverse_dns":  "Reverse DNS names",
}

// sourceKey gives words for one detail key. A key with no words here must still
// render. So its own name, without underscores, is the fallback, as for
// standing and phrase.
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
// operator reads "4m ago" as recently. 4m12s would add nothing.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	d := now.Sub(t)

	switch {
	// A negative d is a clock difference between hosts. So it reads as "just
	// now", as any other gap below a minute.
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

// stamp renders t as a <time> element. It shows the coarse relative label from
// ago. The title has the exact local time and zone, for a reader who needs the
// exact time. A zero time has no time to show, so it stays a bare word. class,
// when given, is a literal from the caller for the few timestamps that need a
// style.
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

	// Fixed element shape, timestamps directly from time.Format, and a class
	// that is always a template literal. Nothing here is text from the
	// caller.
	return template.HTML(b.String()) //nolint:gosec // G203: no user input in the parts
}

// presenceLabel is the spoken status of a dot. It uses the words of the
// legends. So the dot and the legend agree for a reader who hears only one.
// "Seen recently" ends at the online window. The counts, the filter, the
// notification of a watched device, and the greens of decay all use the same
// limit.
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

// dot is a presence indicator: the shade of decay, with a text alternative. The
// colour alone tells nothing to a screen reader, or to a reader who cannot
// tell the two greens apart. The label pairs the coarse status with the age of
// the last sighting.
func dot(now, t time.Time, window time.Duration) template.HTML {
	label := presenceLabel(now, t, window)
	if !t.IsZero() {
		label += ": " + ago(now, t)
	}

	// Fixed element, class from decay, label from presenceLabel and ago.
	// Every part belongs to this package.
	return template.HTML(`<span class="dot ` + decay(now, t, window) + `" role="img" aria-label="` + //nolint:gosec // G203: no user input in the parts
		template.HTMLEscapeString(label) + `"></span>`)
}

// decay is the class that names the age of t.
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

// since renders t as a <time> element that names the time, for "since"
// phrases. There, "2d ago" from ago would not read well. Today, it is only the
// time. Within the week, it is the weekday and time. Before that, it is the
// date. The title has the exact local time and zone, as for stamp.
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

	// Fixed element shape and timestamps directly from time.Format. Nothing
	// here is text from the caller.
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
// divides by zero. So it reports zero, and the bar stays empty.
func pct(n, total int) string {
	if total <= 0 || n <= 0 {
		return "0"
	}

	if n >= total {
		return "100"
	}

	return strconv.FormatFloat(float64(n)/float64(total)*100, 'f', 2, 64)
}

// took renders how long a scan ran. A scan that still runs has no duration
// yet. It renders differently from a scan that finished at once.
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

// scanFound tells what the Found count of a scan counts. A discovery sweep and
// a port scan put numbers of different kinds in the same column.
func scanFound(s *inventory.Scan) string {
	unit := "device"
	if s.Kind == dbtype.ScanPorts {
		unit = "open port"
	}

	if s.Found != 1 {
		unit += "s"
	}

	return strconv.Itoa(s.Found) + " " + unit
}

// tone is the tint of the icon of a log line. Kinds share colours by group. The
// colour tells the kind of change: something arrived, something was learned,
// or someone edited it. Six colours in a list would tell nothing.
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

	// Everything that the user did, and anything with no text yet.
	return "act--edit"
}

// glyphs are the log icons, drawn on a 24px grid and stroked in currentColor.
// So the tone class gives them colour. This package owns their markup. So it
// is safe to return them as HTML.
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

// eventIcon is the glyph for a kind. A kind with no glyph gets the edit mark. A
// kind with no text is most likely an edit.
func eventIcon(k dbtype.EventKind) template.HTML {
	if g, ok := glyphs[k]; ok {
		return g
	}

	return glyphs[dbtype.EventDeviceEdited]
}

// health is the class that gives colour to the status dot of a network. A
// prefix with nothing quiet on it answers. A prefix where more than a third is
// quiet needs a second look. A prefix where nothing was ever found is
// neither.
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
// status dot of the network.
func healthLabel(n *inventory.Network) string {
	switch {
	case n == nil || n.Total == 0:
		return "Nothing recorded yet"
	case n.Offline*3 > n.Total:
		return "A third or more quiet"
	}

	return "Mostly answering"
}

// windowWords writes a duration as a caption would. The online window is a Go
// duration in config, and "15m0s" is not a sentence.
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

// statusClass is the chip of a scan status. Cancelled is not a failure. But for
// a reader, it is the same: the sweep has no result.
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

// addrs lists the addresses of a device. They arrive already in order, because
// SQL cannot order them in a TEXT column.
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

// build is the version of the running binary for the footer, with the page of
// its release notes. URL is empty for a build that is not a release.
type build struct {
	Version string
	URL     string
}

var releaseVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

func currentBuild() build {
	return buildOf(version.Get().Version)
}

// buildOf links the version of a release to its notes. A release from
// GoReleaser has "1.4.0". A release from go install has "v1.4.0". Any other
// value, such as "dev", has no notes.
func buildOf(v string) build {
	if !releaseVersion.MatchString(v) {
		return build{Version: v}
	}

	v = strings.TrimPrefix(v, "v")

	return build{Version: v, URL: "https://github.com/pushkar-anand/jocasta/releases/tag/v" + v}
}
