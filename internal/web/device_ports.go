package web

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// portRow is one line of the Ports table of the device page: a port, what a
// port scan found on it, and what the device advertises on it. A port that the
// scan never recorded is there because the device advertised it. A port that
// the device never advertised is there because the scan found it open.
type portRow struct {
	// Number is the port. It is zero for a service that the device advertised
	// with no port. UDP marks a port that a UDP service advertised. Every port
	// that a scan records is TCP.
	Number uint16
	UDP    bool

	// Scanned reports whether a port scan recorded the port. Service, Open and
	// ChangedAt are what it recorded.
	Scanned   bool
	Service   string
	Open      bool
	ChangedAt time.Time

	// Advertised are the services that the device advertised on the port.
	Advertised []advertised

	// FirstSeen is the earliest time when the port was open or advertised.
	FirstSeen time.Time
}

// advertised is one service as the Ports table shows it.
type advertised struct {
	// Name is the friendly name of the service type. It is empty when the type
	// has none. Then the table shows Type.
	Name string
	Type string

	// Instance is the name that the device gives the service. For a Google
	// Cast device, it is the name from its owner, if it has one. Model is the
	// model that a Google Cast device gives.
	Instance string
	Model    string
}

// portKey is the match key of a port row.
type portKey struct {
	number uint16
	udp    bool
}

// portRows merges the ports that a scan recorded with the services that the
// device advertised, into one row for each port. Rows that are open or
// advertised come first. Then come rows that only a scan recorded, and that
// closed since. Each part is by number, with TCP before UDP, and a service
// without a port last.
func portRows(ports []*inventory.Port, services []*inventory.Service) []*portRow {
	rows := make(map[portKey]*portRow, len(ports)+len(services))

	for _, p := range ports {
		rows[portKey{number: p.Number}] = &portRow{
			Number:    p.Number,
			Scanned:   true,
			Service:   p.Service,
			Open:      p.State == dbtype.PortOpen,
			ChangedAt: p.ChangedAt,
			FirstSeen: p.FirstSeen,
		}
	}

	for _, sv := range services {
		k := portKey{number: sv.Port, udp: strings.HasSuffix(sv.Type, "._udp")}

		r, ok := rows[k]
		if !ok {
			r = &portRow{Number: k.number, UDP: k.udp, FirstSeen: sv.FirstSeen}
			rows[k] = r
		}

		if sv.FirstSeen.Before(r.FirstSeen) {
			r.FirstSeen = sv.FirstSeen
		}

		r.Advertised = append(r.Advertised, advertised{
			Name:     serviceTypeNames[sv.Type],
			Type:     sv.Type,
			Instance: cmp.Or(sv.Label, sv.Instance),
			Model:    sv.Model,
		})
	}

	out := make([]*portRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}

	slices.SortFunc(out, func(a, b *portRow) int {
		return cmp.Or(
			cmp.Compare(a.closedOnly(), b.closedOnly()),
			compareFalseFirst(a.Number == 0, b.Number == 0),
			cmp.Compare(a.Number, b.Number),
			compareFalseFirst(a.UDP, b.UDP),
		)
	})

	return out
}

// compareFalseFirst orders false before true.
func compareFalseFirst(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

// closedOnly is 1 for a row that a scan found closed, and that the device does
// not advertise. This sorts it after the rest.
func (r *portRow) closedOnly() int {
	if r.Scanned && !r.Open && len(r.Advertised) == 0 {
		return 1
	}

	return 0
}

// serviceTypeNames gives names to the DNS-SD service types that devices on a
// home network often advertise. The page shows a missing type as it is.
var serviceTypeNames = map[string]string{
	"_adisk._tcp":            "Time Machine",
	"_afpovertcp._tcp":       "File sharing (AFP)",
	"_airplay._tcp":          "AirPlay",
	"_androidtvremote2._tcp": "Android TV remote",
	"_companion-link._tcp":   "Apple device link",
	"_daap._tcp":             "Music library (DAAP)",
	"_dacp._tcp":             "Remote control (DACP)",
	"_device-info._tcp":      "Device information",
	"_esphomelib._tcp":       "ESPHome",
	"_googlecast._tcp":       "Google Cast",
	"_googlezone._tcp":       "Google speaker group",
	"_hap._tcp":              "HomeKit",
	"_hap._udp":              "HomeKit over Thread",
	"_home-assistant._tcp":   "Home Assistant",
	"_http._tcp":             "Web page",
	"_https._tcp":            "Web page (HTTPS)",
	"_ipp._tcp":              "Printing (IPP)",
	"_ipps._tcp":             "Printing (IPPS)",
	"_matter._tcp":           "Matter",
	"_matterc._udp":          "Matter setup",
	"_mqtt._tcp":             "MQTT",
	"_nfs._tcp":              "File sharing (NFS)",
	"_nut._tcp":              "UPS monitor (NUT)",
	"_pdl-datastream._tcp":   "Printing (raw)",
	"_printer._tcp":          "Printing (LPD)",
	"_raop._tcp":             "AirPlay audio",
	"_rfb._tcp":              "Screen sharing (VNC)",
	"_scanner._tcp":          "Scanning",
	"_sftp-ssh._tcp":         "File transfer (SFTP)",
	"_smb._tcp":              "File sharing (SMB)",
	"_sonos._tcp":            "Sonos",
	"_spotify-connect._tcp":  "Spotify Connect",
	"_ssh._tcp":              "SSH",
	"_uscan._tcp":            "Scanning",
	"_workstation._tcp":      "Workstation",
}
