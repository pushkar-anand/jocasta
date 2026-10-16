// Package classify guesses the kind of device that a record describes, such as
// a phone, a printer or a camera. It uses what a scan already knows: the
// vendor, the name, the TCP ports that answer, and the advertised services.
//
// The guess is only advice. A device can defeat every rule here. So a
// [Result] has its confidence and its reasons, and a person must be able to
// override it. This package does not use the database or the network. It is a
// pure function over facts from elsewhere. So it is cheap to run again after
// every scan, and easy to test.
//
// The rules are in [ruleset], as one ordered list. Every rule names a class
// and the conditions for it. [Device] picks the matching rule with the most
// conditions. If two rules have the same number, the rule listed first wins.
// So the list goes from the strongest rules to the weakest. This order is the
// only tie-breaker. There are no weights.
package classify

import (
	"net/netip"
	"slices"
	"strings"
)

// Input is the data of the classifier. A caller fills it from the inventory.
// An empty field is one signal less.
type Input struct {
	// Vendor is the OUI short name. It is "" when the address is random, or
	// when the table knows no owner for it.
	Vendor string

	// Hostname is the elected name from any source. A match ignores case and
	// finds substrings. So a fully-qualified name works as well as a label.
	Hostname string

	// Randomised reports a locally administered hardware address. Such a
	// device made up its own address. On a home network, this almost always
	// means a phone or a laptop.
	Randomised bool

	// OpenPorts is the set of TCP ports that answer a scan now. Order and
	// duplicates are not important. [Device] sorts a copy and removes
	// duplicates.
	OpenPorts []uint16

	// NetworkName is the name of the segment of the device, if it has one. An
	// "IoT" or "cameras" VLAN is itself a weak signal.
	NetworkName string

	// Services are the DNS-SD service types that the device advertises, such
	// as "_googlecast._tcp". Order, case and duplicates are not important.
	Services []string

	// Models are the models that the device gives in its service records,
	// such as "Google Nest Mini". Only Google Cast gives a model.
	Models []string

	// Addresses are the IP addresses of the device now. The classifier reads
	// one fact from them: whether one is the .1 of its subnet. A home gateway
	// almost always answers on this address.
	Addresses []netip.Addr
}

// Class is a device category. The set is closed. The UI gives each value an
// icon and a filter. So a class outside this list cannot render.
type Class string

// Class values that [Device] can return. Unknown is the zero value. [Device]
// returns it when no rule matched.
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

// classes is every real class, in a stable order, for a caller that builds an
// icon map or a filter. It has no effect on the classification. The rule order
// decides that.
var classes = []Class{
	Router, Switch, AccessPoint, Firewall, Server, NAS, Hypervisor,
	Desktop, Laptop, Phone, Tablet, Printer, Camera, TV, Streaming,
	Speaker, VoiceAssistant, GameConsole, IoTHub, SmartHome, Wearable, VoIP,
}

// Classes returns the closed set of real classes, in a stable order, for a
// caller that builds an icon map or a filter. Unknown is not in the set.
func Classes() []Class { return slices.Clone(classes) }

// Valid reports whether c is Unknown or one of the known classes.
func (c Class) Valid() bool { return c == Unknown || c.Known() }

// Known reports whether c is one of the real classes. Unknown is not.
func (c Class) Known() bool { return slices.Contains(classes, c) }

// Confidence is how strong the case of the winning rule is.
type Confidence string

// Confidence bands. NoConfidence goes with an Unknown class.
const (
	NoConfidence Confidence = ""
	Low          Confidence = "low"
	Medium       Confidence = "medium"
	High         Confidence = "high"
)

// Result is a guess, with the case for it.
type Result struct {
	Class      Class
	Confidence Confidence

	// Reasons starts with the reason of the winning rule. Then it has the
	// reason of every other rule for the winning class. It is empty when
	// Class is Unknown.
	Reasons []string
}

// Facts is [Input], normalised for the rules. The vendor has only letters and
// digits. The strings are lowercase and trimmed. The ports are sorted, with no
// duplicates.
type Facts struct {
	Vendor     string // letters and digits only, lowercased
	Hostname   string // lowercased, trimmed
	Network    string // lowercased, trimmed
	Randomised bool
	Ports      []uint16
	Services   []string // lowercased, sorted, deduped
	Models     []string // lowercased, trimmed
	FirstHost  bool     // holds an address ending in .1
}

// facts normalises text and ports for the rules. It does not change in.
func facts(in Input) Facts {
	ports := slices.Clone(in.OpenPorts)
	slices.Sort(ports)

	firstHost := false

	for _, a := range in.Addresses {
		if a.Is4() && a.As4()[3] == 1 {
			firstHost = true
		}
	}

	services := make([]string, len(in.Services))
	for i, s := range in.Services {
		services[i] = strings.ToLower(s)
	}

	slices.Sort(services)

	models := make([]string, len(in.Models))
	for i, m := range in.Models {
		models[i] = strings.ToLower(strings.TrimSpace(m))
	}

	return Facts{
		Vendor:     normVendor(in.Vendor),
		Hostname:   strings.ToLower(strings.TrimSpace(in.Hostname)),
		Network:    strings.ToLower(strings.TrimSpace(in.NetworkName)),
		Randomised: in.Randomised,
		Ports:      slices.Compact(ports),
		Services:   slices.Compact(services),
		Models:     models,
		FirstHost:  firstHost,
	}
}

func (f Facts) hasPort(p uint16) bool { _, ok := slices.BinarySearch(f.Ports, p); return ok }

func (f Facts) hasService(s string) bool { _, ok := slices.BinarySearch(f.Services, s); return ok }

// Device guesses the kind of thing that in describes.
//
// Of the rules that match, the rule with the most conditions wins. If two
// rules have the same number, the rule listed first wins. So the same input
// always gives the same answer.
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

// confidence gives a guess a band, without weights. A rule that needed more
// than one fact, or a class that more than one rule agreed on, is High. A
// single weak fallback with no other support is Low. All other cases are
// Medium.
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
