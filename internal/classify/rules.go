package classify

import (
	"fmt"
	"regexp"
	"strings"
)

// Weights are on a 1--4 scale. 1 is a hint that needs corroboration, 2 points
// somewhere but has honest alternatives, 3 is characteristic of the class, and
// 4 is close to definitive.

// vendorSignals maps a normalised prefix of the OUI name to what it suggests. A
// manufacturer that makes one kind of thing scores high; one with a broad
// catalogue (Apple, Samsung, HP) scores low and leaves the hostname and ports
// to narrow it down.
//
// The match is a prefix of normVendor's output -- the vendor name lowercased
// with everything but letters and digits removed -- because the stored name is
// as often the truncated Wireshark form ("HewlettPacka", "BrotherIndus") as the
// registered one, and a prefix catches both without a substring's false hits
// ("dlink" inside "handlink").
var vendorSignals = []struct {
	prefix string
	label  string
	class  Class
	weight int
}{
	{"mikrotik", "MikroTik", Router, 3},
	{"ubiquiti", "Ubiquiti", AccessPoint, 1}, // also routers, switches, cameras
	{"tplink", "TP-Link", Router, 1},
	{"netgear", "Netgear", Router, 1},
	{"dlink", "D-Link", Router, 1},
	{"zyxel", "Zyxel", Router, 1},
	{"arris", "Arris", Router, 2},
	{"technicolor", "Technicolor", Router, 2},
	{"cisco", "Cisco", Switch, 1},
	{"juniper", "Juniper", Switch, 2},
	{"aruba", "Aruba", AccessPoint, 2},
	{"ruckus", "Ruckus", AccessPoint, 2},
	{"fortinet", "Fortinet", Firewall, 3},
	{"paloalto", "Palo Alto", Firewall, 3},
	{"synology", "Synology", NAS, 4},
	{"qnap", "QNAP", NAS, 4},
	{"asustor", "Asustor", NAS, 3},
	{"westerndigit", "Western Digital", NAS, 1},
	{"buffalo", "Buffalo", NAS, 1},
	{"vmware", "VMware", Hypervisor, 2},
	{"canon", "Canon", Printer, 2},
	{"seikoepson", "Epson", Printer, 2},
	{"brother", "Brother", Printer, 3},
	{"lexmark", "Lexmark", Printer, 4},
	{"xerox", "Xerox", Printer, 3},
	{"kyocera", "Kyocera", Printer, 3},
	{"ricoh", "Ricoh", Printer, 2},
	{"zebratech", "Zebra", Printer, 3},
	{"hewlett", "HP", Printer, 1}, // printers, but also servers and laptops
	{"hpinc", "HP", Printer, 1},
	{"apple", "Apple", Phone, 1},     // phone, laptop, tablet, TV -- the name and ports decide
	{"samsung", "Samsung", Phone, 1}, // phones and TVs, mostly
	{"lgelectron", "LG", TV, 1},
	{"sony", "Sony", TV, 1},
	{"dell", "Dell", Desktop, 1},
	{"lenovo", "Lenovo", Laptop, 1},
	{"asustek", "ASUS", Desktop, 1},
	{"microsoft", "Microsoft", Desktop, 1}, // Surface and Xbox both
	{"hikvision", "Hikvision", Camera, 4},
	{"dahua", "Dahua", Camera, 4},
	{"zhejiangdahua", "Dahua", Camera, 4},
	{"axiscommunic", "Axis", Camera, 4},
	{"reolink", "Reolink", Camera, 3},
	{"amcrest", "Amcrest", Camera, 3},
	{"wyze", "Wyze", Camera, 2},
	{"ring", "Ring", Camera, 2},
	{"roku", "Roku", Streaming, 4},
	{"sonos", "Sonos", Speaker, 4},
	{"bose", "Bose", Speaker, 2},
	{"amazon", "Amazon", VoiceAssistant, 1}, // Echo, but also Fire TV, Kindle
	{"google", "Google", VoiceAssistant, 1}, // Home/Nest, but also Chromecast
	{"nestlabs", "Nest", SmartHome, 2},
	{"ecobee", "ecobee", SmartHome, 4},
	{"espressif", "Espressif", SmartHome, 2}, // ESP32/ESP8266, the DIY IoT chip
	{"tuya", "Tuya", SmartHome, 3},
	{"itead", "Sonoff", SmartHome, 3},
	{"broadlink", "BroadLink", SmartHome, 3},
	{"philipslight", "Philips Hue", SmartHome, 3},
	{"lifilabs", "LIFX", SmartHome, 4},
	{"nanoleaf", "Nanoleaf", SmartHome, 4},
	{"sonyinteract", "PlayStation", GameConsole, 4},
	{"sonycomputer", "PlayStation", GameConsole, 4},
	{"nintendo", "Nintendo", GameConsole, 4},
	{"raspberry", "Raspberry Pi", Server, 1},
	{"fitbit", "Fitbit", Wearable, 4},
	{"garmin", "Garmin", Wearable, 3},
	{"grandstream", "Grandstream", VoIP, 3},
	{"yealink", "Yealink", VoIP, 4},
	{"polycom", "Polycom", VoIP, 3},
}

// hostnameSignals match the elected name. DHCP host-names and reverse-DNS
// records are where a device most often says outright what it is: "Johns-iPhone",
// "living-room-tv", "shellyplug-s-a41f".
var hostnameSignals = []struct {
	re     *regexp.Regexp
	reason string
	class  Class
	weight int
}{
	{regexp.MustCompile(`iphone`), `the name says iPhone`, Phone, 3},
	{regexp.MustCompile(`ipad`), `the name says iPad`, Tablet, 3},
	{regexp.MustCompile(`ipod`), `the name says iPod`, Phone, 2},
	{regexp.MustCompile(`macbook`), `the name says MacBook`, Laptop, 3},
	{regexp.MustCompile(`\bimac\b`), `the name says iMac`, Desktop, 3},
	{regexp.MustCompile(`mac-?mini`), `the name says Mac mini`, Desktop, 3},
	{regexp.MustCompile(`apple-?tv|appletv`), `the name says Apple TV`, Streaming, 3},
	{regexp.MustCompile(`home-?pod`), `the name says HomePod`, Speaker, 3},
	{regexp.MustCompile(`\b(pixel|galaxy|oneplus|nexus|redmi|poco|moto-?g)\b`), `the name is a phone model`, Phone, 2},
	{regexp.MustCompile(`android-[0-9a-f]{12,}`), `an Android device default name`, Phone, 2},
	{regexp.MustCompile(`chromecast|nest-?hub|nest-?mini|google-?home`), `the name is a Google cast device`, Streaming, 2},
	{regexp.MustCompile(`\b(echo|alexa|echodot)\b`), `the name says Echo/Alexa`, VoiceAssistant, 3},
	{regexp.MustCompile(`fire-?tv|firetv|nvidia-?shield|\bshield\b`), `the name is a streaming box`, Streaming, 3},
	{regexp.MustCompile(`bravia|aquos|\bregza\b|vizio|hisense|\bwebos\b|\btizen\b|-tv\b|\btv-`), `the name says TV`, TV, 2},
	{regexp.MustCompile(`\btv\b`), `the name contains "tv"`, TV, 1},
	{regexp.MustCompile(`nas\b|synology|diskstation|\bds[0-9]{3,4}\b|qnap|truenas|freenas|unraid`), `the name says NAS`, NAS, 3},
	{regexp.MustCompile(`printer|officejet|deskjet|laserjet|\benvy\b|pixma|imageclass|workforce|ecotank|\bmfp\b|mfc-|\bhl-|scanner`), `the name is a printer model`, Printer, 3},
	{regexp.MustCompile(`esp-?[0-9a-f]{6}|esp32|esp8266|nodemcu|d1-?mini|\bwemos\b`), `the name is an ESP microcontroller`, SmartHome, 3},
	{regexp.MustCompile(`shelly|tasmota|sonoff|\btuya\b|smartlife|athom|localbytes`), `the name is a smart-home firmware`, SmartHome, 3},
	{regexp.MustCompile(`\bhue\b|\blifx\b|nanoleaf|\bwiz\b|govee|yeelight|lumiman`), `the name is a smart bulb brand`, SmartHome, 3},
	{regexp.MustCompile(`thermostat|ecobee|\btado\b|\bnest\b`), `the name says thermostat`, SmartHome, 2},
	{regexp.MustCompile(`camera|ipcam|ip-cam|\bcam-?[0-9]|doorbell|reolink|wyzecam|\bnvr\b`), `the name says camera`, Camera, 3},
	{regexp.MustCompile(`\bxbox\b`), `the name says Xbox`, GameConsole, 3},
	{regexp.MustCompile(`playstation|\bps[45]\b|ps-?vita`), `the name says PlayStation`, GameConsole, 3},
	{regexp.MustCompile(`nintendo|switch-?[0-9]`), `the name says Nintendo`, GameConsole, 2},
	{regexp.MustCompile(`pi-?hole|pihole|raspberry-?pi|\brpi[0-9-]`), `the name is a Raspberry Pi`, Server, 2},
	{regexp.MustCompile(`proxmox|^pve\b|\besxi\b|vsphere|hyper-?v`), `the name is a hypervisor`, Hypervisor, 3},
	{regexp.MustCompile(`server|\bsrv-|-srv\b|ubuntu|debian|centos|fedora|\bdocker\b|portainer|\bk8s\b|\bkube`), `the name suggests a server`, Server, 1},
	{regexp.MustCompile(`router|gateway|openwrt|edgerouter|\budm\b|dream-?machine|pfsense|opnsense|-gw\b|\bgw-`), `the name says router/gateway`, Router, 2},
	{regexp.MustCompile(`unifi|^ap-|-ap\b|access-?point`), `the name says access point`, AccessPoint, 2},
	{regexp.MustCompile(`^sw-|-sw\b|switch-?rack|catalyst`), `the name says switch`, Switch, 2},
	{regexp.MustCompile(`fitbit|garmin|apple-?watch|-watch\b|mi-?band|amazfit`), `the name is a wearable`, Wearable, 3},
	{regexp.MustCompile(`sonos|beoplay|soundbar|\bdenon\b|\bheos\b|bluesound|\bnaim\b`), `the name is a speaker brand`, Speaker, 2},
	{regexp.MustCompile(`\bvoip\b|grandstream|yealink|polycom|\bsnom\b|cisco-?spa|\bsip-`), `the name says VoIP`, VoIP, 3},
	{regexp.MustCompile(`laptop|notebook|thinkpad|latitude|elitebook|zenbook|\bxps\b|-lt\b|-nb\b`), `the name says laptop`, Laptop, 2},
	{regexp.MustCompile(`desktop|workstation|-pc\b|-ws\b|optiplex|precision`), `the name says desktop`, Desktop, 2},
}

// portSignals maps a listening TCP port to what answers there. A port is a
// weaker signal than it looks: services move, and a device runs software its
// class does not imply. Only ports a class is genuinely characterised by score
// above 1.
var portSignals = map[uint16][]signal{
	9100:  {{Printer, 1, "port 9100 (raw print, or a node-exporter)"}}, // deliberate collision
	515:   {{Printer, 3, "port 515 (LPD print service)"}},
	631:   {{Printer, 2, "port 631 (IPP print service)"}},
	8009:  {{Streaming, 3, "port 8009 (Chromecast)"}},
	8008:  {{Streaming, 2, "port 8008 (Chromecast)"}},
	7000:  {{Streaming, 1, "port 7000 (AirPlay)"}, {Speaker, 1, "port 7000 (AirPlay)"}},
	5555:  {{Streaming, 1, "port 5555 (Android debug bridge)"}, {Phone, 1, "port 5555 (Android debug bridge)"}},
	554:   {{Camera, 2, "port 554 (RTSP video stream)"}},
	32400: {{Server, 3, "port 32400 (Plex media server)"}},
	8096:  {{Server, 2, "port 8096 (Jellyfin media server)"}},
	8123:  {{IoTHub, 3, "port 8123 (Home Assistant)"}},
	8581:  {{IoTHub, 3, "port 8581 (Homebridge)"}},
	1883:  {{IoTHub, 2, "port 1883 (MQTT broker)"}},
	5001:  {{NAS, 2, "port 5001 (Synology DSM)"}},
	2049:  {{NAS, 2, "port 2049 (NFS export)"}},
	548:   {{NAS, 2, "port 548 (AFP file sharing)"}},
	445:   {{Server, 1, "port 445 (SMB file sharing)"}},
	3389:  {{Desktop, 2, "port 3389 (Windows remote desktop)"}},
	5900:  {{Desktop, 1, "port 5900 (VNC)"}},
	6789:  {{AccessPoint, 2, "port 6789 (UniFi controller)"}},
	8006:  {{Hypervisor, 3, "port 8006 (Proxmox VE)"}},
	902:   {{Hypervisor, 2, "port 902 (VMware host agent)"}},
	53:    {{Router, 1, "port 53 (DNS resolver)"}},
	51820: {{Router, 1, "port 51820 (WireGuard)"}},
	1194:  {{Router, 1, "port 1194 (OpenVPN)"}},
	1723:  {{Router, 1, "port 1723 (PPTP)"}},
	5060:  {{VoIP, 2, "port 5060 (SIP)"}},
	5061:  {{VoIP, 2, "port 5061 (SIP over TLS)"}},
}

// serverPorts are ports that are unremarkable alone but, several at once,
// describe a host running services rather than using them.
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

// vendorRule reads the OUI name.
func vendorRule(in Input) []signal {
	nv := normVendor(in.Vendor)
	if nv == "" {
		return nil
	}

	var out []signal

	for _, v := range vendorSignals {
		if strings.HasPrefix(nv, v.prefix) {
			out = append(out, signal{v.class, v.weight, "vendor " + v.label})
		}
	}

	return out
}

// normVendor reduces a vendor name to letters and digits, lowercased, so a
// prefix match lands the same way on the registered name and on the truncated
// form the OUI table often carries instead.
func normVendor(s string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// hostnameRule reads the elected name.
func hostnameRule(in Input) []signal {
	if in.Hostname == "" {
		return nil
	}

	var out []signal

	for _, h := range hostnameSignals {
		if h.re.MatchString(in.Hostname) {
			out = append(out, signal{h.class, h.weight, h.reason})
		}
	}

	return out
}

// portRule reads individual characteristic ports.
func portRule(in Input) []signal {
	var out []signal

	for _, p := range in.OpenPorts {
		out = append(out, portSignals[p]...)
	}

	return out
}

// serverPortsRule fires when enough service ports are open at once to describe a
// server, whatever any single one of them is. Two is a coincidence a lot of
// devices manage; three or more is a pattern.
func serverPortsRule(in Input) []signal {
	var found []string

	for _, p := range in.OpenPorts {
		if name, ok := serverPorts[p]; ok {
			found = append(found, name)
		}
	}

	if len(found) < 2 {
		return nil
	}

	weight := len(found)
	if weight > 3 {
		weight = 3
	}

	return []signal{{
		Server, weight,
		fmt.Sprintf("%d service ports open at once (%s)", len(found), strings.Join(found, ", ")),
	}}
}

// networkRule reads the name of the segment the device sits on. A named VLAN is
// a decision someone made about what belongs there.
func networkRule(in Input) []signal {
	if in.NetworkName == "" {
		return nil
	}

	patterns := []struct {
		sub    string
		class  Class
		weight int
	}{
		{"iot", SmartHome, 2},
		{"smart-home", SmartHome, 2},
		{"camera", Camera, 2},
		{"cctv", Camera, 2},
		{"surveillance", Camera, 2},
		{"voip", VoIP, 2},
		{"voice", VoIP, 1},
		{"server", Server, 1},
		{"dmz", Server, 1},
		{"lab", Server, 1},
	}

	var out []signal

	for _, p := range patterns {
		if strings.Contains(in.NetworkName, p.sub) {
			out = append(out, signal{p.class, p.weight, fmt.Sprintf("on the %q network", in.NetworkName)})
		}
	}

	return out
}

// randomisedRule notes a self-assigned hardware address. A device that
// randomises its MAC is running a modern phone or laptop OS; on a home network
// the phone is the safer guess, so it gets the weak vote and any real evidence
// of a laptop outweighs it.
func randomisedRule(in Input) []signal {
	if !in.Randomised {
		return nil
	}

	return []signal{{Phone, 1, "randomised hardware address, typical of a phone or laptop"}}
}
