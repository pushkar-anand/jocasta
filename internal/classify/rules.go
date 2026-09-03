package classify

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Cond is the set of facts a rule needs to see. A rule fires when every field
// that is set here holds; an unset field is not tested. HostNot is the
// exception: it is a veto, not a condition, and does not count towards how
// specific the rule is.
//
// The number of set condition fields is a rule's specificity, which is how
// [Device] breaks the tie when several rules match: the pickier rule wins.
type Cond struct {
	Vendor  string // prefix of the normalised OUI name (letters and digits, lowercased)
	Host    string // regexp, matched against the lowercased elected name
	HostNot string // regexp veto: the rule is skipped if this matches the name
	Network string // substring of the lowercased segment name

	Port    uint16   // this port is open
	AnyPort []uint16 // at least one of these is open
	AllPort []uint16 // all of these are open

	Randomised *bool // the address is (or is not) locally administered

	// MinServer fires when at least this many service ports (see serverPorts)
	// are open at once -- a host running services rather than using them.
	MinServer int

	When func(Facts) bool // escape hatch for the rare rule the fields cannot express
}

// Rule maps a set of conditions to a class.
type Rule struct {
	Cond

	Class  Class
	Reason string // the phrase shown to a person; ReasonFn wins if set

	// ReasonFn builds the phrase from the facts, for a rule whose reason names
	// something it matched (which ports, how many).
	ReasonFn func(Facts) string

	// Weak marks a loose fallback -- a broad vendor, a port a dozen classes
	// share. A weak rule that fires alone yields a Low-confidence guess.
	Weak bool
}

func (r Rule) reason(f Facts) string {
	if r.ReasonFn != nil {
		return r.ReasonFn(f)
	}

	return r.Reason
}

func ptr[T any](v T) *T { return &v }

// serverPorts are ports that say little alone but, several at once, describe a
// host running services rather than a device using them.
var serverPorts = map[uint16]string{
	22:    "ssh",
	3306:  "mysql",
	5432:  "postgresql",
	6379:  "redis",
	27017: "mongodb",
	9200:  "elasticsearch",
	5984:  "couchdb",
	11211: "memcached",
	2375:  "docker",
	2376:  "docker",
	6443:  "kubernetes",
	9000:  "portainer",
	9090:  "prometheus",
	3000:  "grafana",
	19999: "netdata",
	8086:  "influxdb",
	873:   "rsync",
}

// serviceReason names the service ports a host has open, for the MinServer rule.
func serviceReason(f Facts) string {
	var names []string

	for _, p := range f.Ports {
		if n, ok := serverPorts[p]; ok && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}

	return fmt.Sprintf("%d service ports open at once (%s)", len(names), strings.Join(names, ", "))
}

// compiled caches every Host / HostNot pattern in the ruleset, compiled once.
var compiled = map[string]*regexp.Regexp{}

func init() {
	for i := range ruleset {
		for _, pat := range []string{ruleset[i].Host, ruleset[i].HostNot} {
			if pat == "" || compiled[pat] != nil {
				continue
			}

			compiled[pat] = regexp.MustCompile(pat)
		}
	}
}

// match reports whether r holds for f, and how many condition fields it tested
// (its specificity). A Cond with no positive condition never matches.
func match(c Cond, f Facts) (conds int, ok bool) {
	if c.HostNot != "" && compiled[c.HostNot].MatchString(f.Hostname) {
		return 0, false
	}

	n := 0
	test := func(set, pass bool) bool {
		if !set {
			return true
		}

		n++

		return pass
	}

	all := test(c.Vendor != "", f.Vendor != "" && strings.HasPrefix(f.Vendor, c.Vendor)) &&
		test(c.Host != "", c.Host != "" && compiled[c.Host].MatchString(f.Hostname)) &&
		test(c.Network != "", c.Network != "" && strings.Contains(f.Network, c.Network)) &&
		test(c.Port != 0, c.Port != 0 && f.hasPort(c.Port)) &&
		test(len(c.AnyPort) > 0, anyPort(f, c.AnyPort)) &&
		test(len(c.AllPort) > 0, allPort(f, c.AllPort)) &&
		test(c.Randomised != nil, c.Randomised != nil && *c.Randomised == f.Randomised) &&
		test(c.MinServer > 0, countServer(f) >= c.MinServer) &&
		test(c.When != nil, c.When != nil && c.When(f))

	if n == 0 || !all {
		return n, false
	}

	return n, true
}

func anyPort(f Facts, ports []uint16) bool {
	return slices.ContainsFunc(ports, f.hasPort)
}

func allPort(f Facts, ports []uint16) bool {
	for _, p := range ports {
		if !f.hasPort(p) {
			return false
		}
	}

	return len(ports) > 0
}

func countServer(f Facts) int {
	seen := map[string]bool{}

	for _, p := range f.Ports {
		if n, ok := serverPorts[p]; ok {
			seen[n] = true
		}
	}

	return len(seen)
}

// normVendor reduces a vendor name to lowercase letters and digits, so a prefix
// match lands the same way on the registered name and on the truncated form the
// OUI table often carries ("HewlettPacka", "BrotherIndus").
func normVendor(s string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// vend is a shorthand for a vendor rule.
func vend(prefix, label string, c Class, weak bool) Rule {
	return Rule{Cond: Cond{Vendor: prefix}, Class: c, Reason: "vendor " + label, Weak: weak}
}

// host is a shorthand for a hostname rule.
func host(re, reason string, c Class, weak bool) Rule {
	return Rule{Cond: Cond{Host: re}, Class: c, Reason: reason, Weak: weak}
}

// port is a shorthand for a single-port rule.
func port(p uint16, reason string, c Class, weak bool) Rule {
	return Rule{Cond: Cond{Port: p}, Class: c, Reason: reason, Weak: weak}
}

// net is a shorthand for a network-name rule. Always weak: a VLAN name is a
// hint about what belongs there, never proof of any one device.
func net(sub, reason string, c Class) Rule {
	return Rule{Cond: Cond{Network: sub}, Class: c, Reason: reason, Weak: true}
}

// ruleset is the whole classifier, ordered from the most telling rules to the
// weakest. That order is the tie-breaker when several equally specific rules
// match, so a rule's place in the list is part of what it means.
var ruleset = []Rule{
	// ---- combinations: pickier than any single-fact rule, so they win by
	// specificity wherever they apply ----

	// Home Assistant on a general-purpose box: the port says what it is, the
	// vendor only says it is a VM.
	{Cond: Cond{Port: 8123}, Class: IoTHub, Reason: "port 8123 (Home Assistant)"},

	// A Chromecast port on something that is clearly a computer is a cast
	// receiver running on a desktop, not a streaming stick.
	{
		Cond:   Cond{AnyPort: []uint16{8008, 8009}, HostNot: `nest|chromecast|shield|fire-?tv`, MinServer: 2},
		Class:  Server,
		Reason: "cast port beside a stack of service ports",
	},

	// ---- definitive: a manufacturer that makes one kind of thing, or a name
	// that spells the product out ----

	host(`iphone`, "the name says iPhone", Phone, false),
	host(`ipad`, "the name says iPad", Tablet, false),
	host(`macbook`, "the name says MacBook", Laptop, false),
	host(`(?:\b|_)imac(?:\b|_)`, "the name says iMac", Desktop, false),
	host(`mac-?mini`, "the name says Mac mini", Desktop, false),
	host(`apple-?tv|appletv`, "the name says Apple TV", Streaming, false),
	host(`home-?pod`, "the name says HomePod", Speaker, false),
	host(`(?:\b|_)(?:echo|alexa|echodot)(?:\b|_)`, "the name says Echo/Alexa", VoiceAssistant, false),
	host(`fire-?tv|firetv|nvidia-?shield|(?:\b|_)shield(?:\b|_)`, "the name is a streaming box", Streaming, false),
	host(`(?:\b|_)xbox(?:\b|_)`, "the name says Xbox", GameConsole, false),
	host(`playstation|(?:\b|_)ps[45](?:\b|_)|ps-?vita`, "the name says PlayStation", GameConsole, false),
	host(`nas(?:\b|_)|synology|diskstation|(?:\b|_)ds[0-9]{3,4}(?:\b|_)|qnap|truenas|freenas|unraid`, "the name says NAS", NAS, false),
	host(`printer|officejet|deskjet|laserjet|(?:\b|_)envy(?:\b|_)|pixma|imageclass|workforce|ecotank|(?:\b|_)mfp(?:\b|_)|mfc-|(?:\b|_)hl-|scanner`, "the name is a printer model", Printer, false),
	host(`esp-?[0-9a-f]{6}|esp32|esp8266|nodemcu|d1-?mini|(?:\b|_)wemos(?:\b|_)`, "the name is an ESP microcontroller", SmartHome, false),
	host(`shelly|tasmota|sonoff|(?:\b|_)tuya(?:\b|_)|smartlife|athom|localbytes`, "the name is a smart-home firmware", SmartHome, false),
	host(`(?:\b|_)hue(?:\b|_)|(?:\b|_)lifx(?:\b|_)|nanoleaf|(?:\b|_)wiz(?:\b|_)|govee|yeelight|lumiman`, "the name is a smart bulb brand", SmartHome, false),
	host(`camera|ipcam|ip-cam|(?:\b|_)cam-?[0-9]|doorbell|reolink|wyzecam|(?:\b|_)nvr(?:\b|_)`, "the name says camera", Camera, false),
	host(`proxmox|^pve(?:\b|_)|(?:\b|_)esxi(?:\b|_)|vsphere|hyper-?v`, "the name is a hypervisor", Hypervisor, false),
	host(`(?:\b|_)voip(?:\b|_)|grandstream|yealink|polycom|(?:\b|_)snom(?:\b|_)|cisco-?spa|(?:\b|_)sip-`, "the name says VoIP", VoIP, false),
	host(`fitbit|garmin|apple-?watch|-watch(?:\b|_)|mi-?band|amazfit`, "the name is a wearable", Wearable, false),

	vend("mikrotik", "MikroTik", Router, false),
	vend("fortinet", "Fortinet", Firewall, false),
	vend("paloalto", "Palo Alto", Firewall, false),
	vend("synology", "Synology", NAS, false),
	vend("qnap", "QNAP", NAS, false),
	vend("asustor", "Asustor", NAS, false),
	vend("brother", "Brother", Printer, false),
	vend("lexmark", "Lexmark", Printer, false),
	vend("xerox", "Xerox", Printer, false),
	vend("kyocera", "Kyocera", Printer, false),
	vend("zebratech", "Zebra", Printer, false),
	vend("hikvision", "Hikvision", Camera, false),
	vend("dahua", "Dahua", Camera, false),
	vend("zhejiangdahua", "Dahua", Camera, false),
	vend("axiscommunic", "Axis", Camera, false),
	vend("reolink", "Reolink", Camera, false),
	vend("amcrest", "Amcrest", Camera, false),
	vend("roku", "Roku", Streaming, false),
	vend("sonos", "Sonos", Speaker, false),
	vend("ecobee", "ecobee", SmartHome, false),
	vend("tuya", "Tuya", SmartHome, false),
	vend("itead", "Sonoff", SmartHome, false),
	vend("broadlink", "BroadLink", SmartHome, false),
	vend("philipslight", "Philips Hue", SmartHome, false),
	vend("lifilabs", "LIFX", SmartHome, false),
	vend("nanoleaf", "Nanoleaf", SmartHome, false),
	vend("sonyinteract", "PlayStation", GameConsole, false),
	vend("sonycomputer", "PlayStation", GameConsole, false),
	vend("nintendo", "Nintendo", GameConsole, false),
	vend("fitbit", "Fitbit", Wearable, false),
	vend("garmin", "Garmin", Wearable, false),
	vend("grandstream", "Grandstream", VoIP, false),
	vend("yealink", "Yealink", VoIP, false),
	vend("polycom", "Polycom", VoIP, false),

	port(515, "port 515 (LPD print service)", Printer, false),
	port(8009, "port 8009 (Chromecast)", Streaming, false),
	port(8581, "port 8581 (Homebridge)", IoTHub, false),
	port(8006, "port 8006 (Proxmox VE)", Hypervisor, false),
	port(32400, "port 32400 (Plex media server)", Server, false),

	// ---- characteristic: points somewhere, with honest alternatives ----

	host(`ipod`, "the name says iPod", Phone, false),
	host(`(?:\b|_)(?:pixel|galaxy|oneplus|nexus|redmi|poco|moto-?g)(?:\b|_)`, "the name is a phone model", Phone, false),
	host(`android-[0-9a-f]{12,}`, "an Android device default name", Phone, false),
	host(`chromecast|nest-?hub|nest-?mini|google-?home`, "the name is a Google cast device", Streaming, false),
	host(`bravia|aquos|(?:\b|_)regza(?:\b|_)|vizio|hisense|(?:\b|_)webos(?:\b|_)|(?:\b|_)tizen(?:\b|_)|-tv(?:\b|_)|(?:\b|_)tv-`, "the name says TV", TV, false),
	host(`thermostat|ecobee|(?:\b|_)tado(?:\b|_)|(?:\b|_)nest(?:\b|_)`, "the name says thermostat", SmartHome, false),
	host(`nintendo|switch-?[0-9]`, "the name says Nintendo", GameConsole, false),
	host(`pi-?hole|pihole|raspberry-?pi|(?:\b|_)rpi[0-9-]`, "the name is a Raspberry Pi", Server, false),
	host(`router|gateway|openwrt|edgerouter|(?:\b|_)udm(?:\b|_)|dream-?machine|pfsense|opnsense|-gw(?:\b|_)|(?:\b|_)gw-`, "the name says router/gateway", Router, false),
	host(`unifi|^ap-|-ap(?:\b|_)|access-?point`, "the name says access point", AccessPoint, false),
	host(`^sw-|-sw(?:\b|_)|switch-?rack|catalyst`, "the name says switch", Switch, false),
	host(`sonos|beoplay|soundbar|(?:\b|_)denon(?:\b|_)|(?:\b|_)heos(?:\b|_)|bluesound|(?:\b|_)naim(?:\b|_)`, "the name is a speaker brand", Speaker, false),
	host(`laptop|notebook|thinkpad|latitude|elitebook|zenbook|(?:\b|_)xps(?:\b|_)|-lt(?:\b|_)|-nb(?:\b|_)`, "the name says laptop", Laptop, false),
	host(`desktop|workstation|-pc(?:\b|_)|-ws(?:\b|_)|optiplex|precision`, "the name says desktop", Desktop, false),

	vend("arris", "Arris", Router, false),
	vend("technicolor", "Technicolor", Router, false),
	vend("juniper", "Juniper", Switch, false),
	vend("aruba", "Aruba", AccessPoint, false),
	vend("ruckus", "Ruckus", AccessPoint, false),
	vend("vmware", "VMware", Hypervisor, false),
	vend("canon", "Canon", Printer, false),
	vend("seikoepson", "Epson", Printer, false),
	vend("ricoh", "Ricoh", Printer, false),
	vend("wyze", "Wyze", Camera, false),
	vend("ring", "Ring", Camera, false),
	vend("bose", "Bose", Speaker, false),
	vend("nestlabs", "Nest", SmartHome, false),
	vend("espressif", "Espressif", SmartHome, false),

	{Cond: Cond{MinServer: 2}, Class: Server, ReasonFn: serviceReason},

	port(631, "port 631 (IPP print service)", Printer, false),
	port(8008, "port 8008 (Chromecast)", Streaming, false),
	port(554, "port 554 (RTSP video stream)", Camera, false),
	port(8096, "port 8096 (Jellyfin media server)", Server, false),
	port(1883, "port 1883 (MQTT broker)", IoTHub, false),
	port(5001, "port 5001 (Synology DSM)", NAS, false),
	port(2049, "port 2049 (NFS export)", NAS, false),
	port(548, "port 548 (AFP file sharing)", NAS, false),
	port(3389, "port 3389 (Windows remote desktop)", Desktop, false),
	port(6789, "port 6789 (UniFi controller)", AccessPoint, false),
	port(902, "port 902 (VMware host agent)", Hypervisor, false),
	port(5060, "port 5060 (SIP)", VoIP, false),
	port(5061, "port 5061 (SIP over TLS)", VoIP, false),

	net("iot", `on an "IoT" segment`, SmartHome),
	net("smart-home", `on a smart-home segment`, SmartHome),
	net("camera", `on a camera segment`, Camera),
	net("cctv", `on a CCTV segment`, Camera),
	net("surveillance", `on a surveillance segment`, Camera),
	net("voip", `on a VoIP segment`, VoIP),

	// ---- weak: a hint that needs corroboration to mean much ----

	host(`(?:\b|_)tv(?:\b|_)`, `the name contains "tv"`, TV, true),
	host(`server|(?:\b|_)srv-|-srv(?:\b|_)|ubuntu|debian|centos|fedora|(?:\b|_)docker(?:\b|_)|portainer|(?:\b|_)k8s(?:\b|_)|(?:\b|_)kube`, "the name suggests a server", Server, true),

	vend("ubiquiti", "Ubiquiti", AccessPoint, true),
	vend("tplink", "TP-Link", Router, true),
	vend("netgear", "Netgear", Router, true),
	vend("dlink", "D-Link", Router, true),
	vend("zyxel", "Zyxel", Router, true),
	vend("cisco", "Cisco", Switch, true),
	vend("westerndigit", "Western Digital", NAS, true),
	vend("buffalo", "Buffalo", NAS, true),
	vend("hewlett", "HP", Printer, true),
	vend("hpinc", "HP", Printer, true),
	vend("apple", "Apple", Phone, true),
	vend("samsung", "Samsung", Phone, true),
	vend("lgelectron", "LG", TV, true),
	vend("sony", "Sony", TV, true),
	vend("dell", "Dell", Desktop, true),
	vend("lenovo", "Lenovo", Laptop, true),
	vend("asustek", "ASUS", Desktop, true),
	vend("microsoft", "Microsoft", Desktop, true),
	vend("amazon", "Amazon", VoiceAssistant, true),
	vend("google", "Google", VoiceAssistant, true),
	vend("raspberry", "Raspberry Pi", Server, true),

	port(9100, "port 9100 (raw print, or a node-exporter)", Printer, true),
	port(445, "port 445 (SMB file sharing)", Server, true),
	port(5900, "port 5900 (VNC)", Desktop, true),
	port(53, "port 53 (DNS resolver)", Router, true),
	port(51820, "port 51820 (WireGuard)", Router, true),
	port(1194, "port 1194 (OpenVPN)", Router, true),
	port(1723, "port 1723 (PPTP)", Router, true),
	port(7000, "port 7000 (AirPlay)", Streaming, true),
	{Cond: Cond{Port: 7000}, Class: Speaker, Reason: "port 7000 (AirPlay)", Weak: true},
	port(5555, "port 5555 (Android debug bridge)", Streaming, true),
	{Cond: Cond{Port: 5555}, Class: Phone, Reason: "port 5555 (Android debug bridge)", Weak: true},

	net("voice", `on a voice segment`, VoIP),
	net("server", `on a server segment`, Server),
	net("dmz", `on the DMZ`, Server),
	net("lab", `on a lab segment`, Server),

	// A self-assigned address is a modern phone or laptop OS; on a home network
	// the phone is the safer guess, and any real evidence of a laptop, listed
	// above, outweighs it.
	{Cond: Cond{Randomised: ptr(true)}, Class: Phone, Reason: "randomised hardware address, typical of a phone or laptop", Weak: true},
}
