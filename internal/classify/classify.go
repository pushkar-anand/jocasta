// Package classify guesses what kind of device a record describes -- a phone, a
// printer, a camera -- from what a scan already knows about it: its vendor, its
// name, and the TCP ports it answers on.
//
// The guess is advisory. Every rule here is a heuristic a determined device can
// defeat, so a [Result] carries the confidence behind it and the reasons that
// led to it, and the caller is expected to let a person override it. Nothing in
// this package touches the database or the network: it is a pure function over
// facts gathered elsewhere, which is what makes it cheap to re-run after every
// scan and simple to test.
//
// The rules live in [ruleset] as one ordered list. Every rule names a class and
// the conditions that point at it; [Device] picks the matching rule with the
// most conditions, and on a tie the one listed first. The list therefore runs
// from the most definitive rules to the weakest, and that order is the only
// tie-breaker -- there are no weights to balance.
package classify

import (
	"slices"
	"strings"
)

// Input is what the classifier reasons over. A caller fills it from the
// inventory; an empty field is simply one less signal, never an error.
type Input struct {
	// Vendor is the OUI short name, or "" when the address is randomised or
	// registered to nobody the table knows.
	Vendor string

	// Hostname is the elected name from any source. Matching is case-insensitive
	// and substring, so a fully-qualified name works as well as a bare label.
	Hostname string

	// Randomised reports a locally administered hardware address. Such a device
	// generated its own address, which on a home network almost always means a
	// phone or a laptop.
	Randomised bool

	// OpenPorts is the set of TCP ports a scan currently finds answering. Order
	// and duplicates do not matter; [Device] sorts and dedupes a copy.
	OpenPorts []uint16

	// NetworkName is what the segment the device sits on is called, when it is
	// called anything. An "IoT" or "cameras" VLAN is itself a weak classifier.
	NetworkName string
}

// Class is a device category. The set is closed: the UI keys an icon and a
// filter off each value, so a class outside this list has nowhere to render.
type Class string

// Class values [Device] can return. Unknown is the zero value, returned when no
// rule had anything to say.
const (
	Unknown        Class = ""
	Router         Class = "router"
	Switch         Class = "switch"
	AccessPoint    Class = "access_point"
	Firewall       Class = "firewall"
	Server         Class = "server"
	NAS            Class = "nas"
	Hypervisor     Class = "hypervisor"
	Desktop        Class = "desktop"
	Laptop         Class = "laptop"
	Phone          Class = "phone"
	Tablet         Class = "tablet"
	Printer        Class = "printer"
	Camera         Class = "camera"
	TV             Class = "tv"
	Streaming      Class = "streaming"
	Speaker        Class = "speaker"
	VoiceAssistant Class = "voice_assistant"
	GameConsole    Class = "game_console"
	IoTHub         Class = "iot_hub"
	SmartHome      Class = "smart_home"
	Wearable       Class = "wearable"
	VoIP           Class = "voip"
)

// classes is every real class, in a stable order for a caller building an icon
// map or a filter. It plays no part in classification -- the rule order does
// that.
var classes = []Class{
	Router, Switch, AccessPoint, Firewall, Server, NAS, Hypervisor,
	Desktop, Laptop, Phone, Tablet, Printer, Camera, TV, Streaming,
	Speaker, VoiceAssistant, GameConsole, IoTHub, SmartHome, Wearable, VoIP,
}

// Classes returns the closed set of real classes, in a stable order, for a
// caller building an icon map or a filter. Unknown is not among them.
func Classes() []Class { return slices.Clone(classes) }

// Valid reports whether c is Unknown or one of the known classes.
func (c Class) Valid() bool { return c == Unknown || slices.Contains(classes, c) }

// Confidence is how strong a case the winning rule made.
type Confidence string

// Confidence bands. NoConfidence accompanies an Unknown class.
const (
	NoConfidence Confidence = ""
	Low          Confidence = "low"
	Medium       Confidence = "medium"
	High         Confidence = "high"
)

// Result is a guess and the case for it.
type Result struct {
	Class      Class
	Confidence Confidence

	// Reasons are the winning rule's reason first, then the reason of every
	// other rule that also pointed at the winning class. Empty when Class is
	// Unknown.
	Reasons []string
}

// Facts is [Input] normalised for the rules: the vendor reduced to letters and
// digits, the strings lowercased and trimmed, the ports sorted and deduped.
type Facts struct {
	Vendor     string // letters and digits only, lowercased
	Hostname   string // lowercased, trimmed
	Network    string // lowercased, trimmed
	Randomised bool
	Ports      []uint16
}

func facts(in Input) Facts {
	ports := slices.Clone(in.OpenPorts)
	slices.Sort(ports)

	return Facts{
		Vendor:     normVendor(in.Vendor),
		Hostname:   strings.ToLower(strings.TrimSpace(in.Hostname)),
		Network:    strings.ToLower(strings.TrimSpace(in.NetworkName)),
		Randomised: in.Randomised,
		Ports:      slices.Compact(ports),
	}
}

func (f Facts) hasPort(p uint16) bool { _, ok := slices.BinarySearch(f.Ports, p); return ok }

// Device guesses what kind of thing in describes.
//
// It walks [ruleset] once. Among the rules that match, the winner is the one
// with the most conditions; a tie goes to whichever is listed first. The same
// input therefore always gives the same answer.
func Device(in Input) Result {
	f := facts(in)

	best := -1
	bestConds := 0

	for i := range ruleset {
		conds, ok := match(ruleset[i].Cond, f)
		if !ok {
			continue
		}

		if best < 0 || conds > bestConds {
			best, bestConds = i, conds
		}
	}

	if best < 0 {
		return Result{}
	}

	win := ruleset[best]

	reasons := []string{win.reason(f)}
	agree := 0

	for i := range ruleset {
		if i == best || ruleset[i].Class != win.Class {
			continue
		}

		_, ok := match(ruleset[i].Cond, f)
		if !ok {
			continue
		}

		agree++

		reasons = append(reasons, ruleset[i].reason(f))
	}

	return Result{
		Class:      win.Class,
		Confidence: confidence(win, bestConds, agree),
		Reasons:    reasons,
	}
}

// confidence bands a guess without any weights: a rule that needed more than
// one fact to fire, or a class more than one rule agreed on, is High; a lone
// weak fallback nothing corroborated is Low; everything else is Medium.
func confidence(win Rule, conds, agree int) Confidence {
	switch {
	case conds >= 2 || agree >= 2:
		return High
	case win.Weak && agree == 0:
		return Low
	default:
		return Medium
	}
}
