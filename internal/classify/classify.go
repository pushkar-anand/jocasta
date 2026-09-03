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

// classes is every real class, in the order a tie on total score is finally
// broken. More specific categories, and ones a stray signal is less likely to
// land on, come first. This is also the order the winner is chosen in, so a
// result never depends on map iteration.
var classes = []Class{
	NAS, Hypervisor, Printer, Camera, GameConsole,
	VoiceAssistant, Speaker, Streaming, TV, Wearable, VoIP,
	IoTHub, SmartHome,
	Firewall, Router, Switch, AccessPoint, Server,
	Phone, Tablet, Laptop, Desktop,
}

// Classes returns the closed set of real classes, in a stable order, for a
// caller building an icon map or a filter. Unknown is not among them.
func Classes() []Class { return slices.Clone(classes) }

// Valid reports whether c is Unknown or one of the known classes.
func (c Class) Valid() bool { return c == Unknown || slices.Contains(classes, c) }

// Confidence is how much weight of evidence stood behind a class.
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

	// Reasons are the winning class's supporting signals, in the order the
	// rules produced them. Empty when Class is Unknown.
	Reasons []string
}

// signal is one rule's vote: a class, how strongly it points there, and a
// phrase a person can read.
type signal struct {
	class  Class
	weight int
	reason string
}

// rule inspects an Input and returns the signals it found, or none.
type rule func(Input) []signal

// rules run in this order. The order affects only how reasons are listed, not
// which class wins.
var rules = []rule{
	vendorRule,
	hostnameRule,
	portRule,
	serverPortsRule,
	networkRule,
	randomisedRule,
}

// Device guesses what kind of thing in describes.
//
// It sums every rule's signals per class and returns the class with the most
// weight behind it. A tie goes to whichever class carries the single strongest
// signal, and then to the order in classes, so the same input always gives the
// same answer.
func Device(in Input) Result {
	in.Vendor = strings.ToLower(strings.TrimSpace(in.Vendor))
	in.Hostname = strings.ToLower(strings.TrimSpace(in.Hostname))
	in.NetworkName = strings.ToLower(strings.TrimSpace(in.NetworkName))

	ports := slices.Clone(in.OpenPorts)
	slices.Sort(ports)
	in.OpenPorts = slices.Compact(ports)

	var sigs []signal
	for _, r := range rules {
		sigs = append(sigs, r(in)...)
	}

	total := map[Class]int{}
	strongest := map[Class]int{}

	for _, s := range sigs {
		if s.weight <= 0 || s.class == Unknown || !s.class.Valid() {
			continue
		}

		total[s.class] += s.weight
		strongest[s.class] = max(strongest[s.class], s.weight)
	}

	win := Unknown

	for _, c := range classes {
		switch {
		case total[c] == 0:
			continue
		case win == Unknown,
			total[c] > total[win],
			total[c] == total[win] && strongest[c] > strongest[win]:
			win = c
		}
	}

	if win == Unknown {
		return Result{}
	}

	var reasons []string

	for _, s := range sigs {
		if s.class == win && s.weight > 0 {
			reasons = append(reasons, s.reason)
		}
	}

	return Result{Class: win, Confidence: confidence(total[win]), Reasons: reasons}
}

// confidence bands a winning score. The thresholds are set so a single strong
// signal (weight 3) reads as Medium and needs corroboration to reach High.
func confidence(score int) Confidence {
	switch {
	case score >= 5:
		return High
	case score >= 3:
		return Medium
	default:
		return Low
	}
}
