package inventory

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/pkg/asn"
)

// A device probing the network is one that, within a single hour, tried at
// least ProbeMinPeers local addresses or ProbeMinPorts ports on one of them
// without carrying data. Ordinary devices reach a handful of neighbours
// (the router, a printer, a speaker) and a handful of ports on each.
const (
	ProbeMinPeers = 20
	ProbeMinPorts = 20
)

// deviceAttemptsLimit caps how many peer rows a device's attempts view reads.
const deviceAttemptsLimit = 200

// Attempt is what one device tried to reach at one peer over a window
// without the connection ever carrying data.
type Attempt struct {
	// PeerDeviceID and PeerDeviceName name the peer when it was a known
	// device; zero otherwise.
	PeerDeviceID   int64  `json:"peer_device_id,omitempty"`
	PeerDeviceName string `json:"peer_device_name,omitempty"`

	IP netip.Addr `json:"ip"`

	// Internet is set for a public address, with the organisation announcing
	// it when known.
	Internet bool   `json:"internet,omitempty"`
	ASN      uint32 `json:"asn,omitempty"`
	OrgShort string `json:"org,omitempty"`

	Protocol uint8 `json:"protocol"`

	// Attempts is how many were made, Answered how many the peer answered:
	// a port that accepted the handshake, a ping that came back.
	Attempts int64 `json:"attempts"`
	Answered int64 `json:"answered"`

	// PortCount is how many distinct ports were tried, and Ports the lowest
	// of them. Both empty for ICMP.
	PortCount int64    `json:"port_count,omitempty"`
	Ports     []uint16 `json:"ports,omitempty"`

	LastHour time.Time `json:"last_hour"`
}

// DeviceAttempts returns what device id tried to reach since the start of
// the hour containing since, most attempts first.
func (s *Store) DeviceAttempts(ctx context.Context, id int64, since time.Time) ([]*Attempt, error) {
	rows, err := s.q.DeviceAttempts(ctx, models.DeviceAttemptsParams{
		DeviceID: id,
		Hour:     hourOf(since),
		Limit:    deviceAttemptsLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("attempts for device %d: %w", id, err)
	}

	out := make([]*Attempt, 0, len(rows))

	for _, r := range rows {
		ip, err := netip.ParseAddr(r.PeerIP)
		if err != nil {
			return nil, fmt.Errorf("attempt peer %q: %w", r.PeerIP, err)
		}

		last, err := parseHour("attempt", r.LastHour)
		if err != nil {
			return nil, err
		}

		a := &Attempt{
			PeerDeviceID: r.PeerDeviceID,
			IP:           ip,
			Internet:     r.PeerDeviceID == 0 && asn.IsPublic(ip),
			Protocol:     uint8(r.Protocol), //nolint:gosec // written from a uint8.
			Attempts:     r.Attempts,
			Answered:     r.Answered,
			PortCount:    r.PortCount,
			Ports:        mergePorts(nil, parsePortList(r.Ports)),
			LastHour:     last,
		}

		if a.PeerDeviceID != 0 {
			a.PeerDeviceName = displayName(r.PeerLabel, r.PeerHostname, r.PeerMAC, ip.String(), a.PeerDeviceID)
		}

		if r.PeerASN != 0 {
			a.ASN = uint32(r.PeerASN) //nolint:gosec // an ASN is 32 bits.
			_, a.OrgShort = orgName(a.ASN, ip)
		}

		out = append(out, a)
	}

	return out, nil
}

// Prober is a device that probed the local network within a window.
type Prober struct {
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`

	// LastHour is the most recent hour it probed, Hours how many hours of
	// the window it did.
	LastHour time.Time `json:"last_hour"`
	Hours    int       `json:"hours"`

	// Peers is the most local addresses it tried in any one hour, and
	// MaxPorts the most ports it tried on any one of them.
	Peers    int64 `json:"peers"`
	MaxPorts int64 `json:"max_ports"`

	// Attempts and Answered are summed over the hours it probed.
	Attempts int64 `json:"attempts"`
	Answered int64 `json:"answered"`

	// Ports is the lowest ports it tried, across those hours.
	Ports []uint16 `json:"ports,omitempty"`
}

// SweptAddresses reports whether the device tried many addresses in one hour.
func (p *Prober) SweptAddresses() bool { return p.Peers >= ProbeMinPeers }

// SweptPorts reports whether it tried many ports on one address in one hour.
func (p *Prober) SweptPorts() bool { return p.MaxPorts >= ProbeMinPorts }

// ProbingDevices returns the devices that probed the local network since the
// start of the hour containing since, most recent first. A non-empty group
// keeps only the devices in it.
func (s *Store) ProbingDevices(ctx context.Context, since time.Time, group string) ([]*Prober, error) {
	rows, err := s.q.ProbingHours(ctx, models.ProbingHoursParams{
		Since:     hourOf(since),
		GroupName: nullString(group),
		MinPeers:  ProbeMinPeers,
		MinPorts:  ProbeMinPorts,
	})
	if err != nil {
		return nil, fmt.Errorf("probing devices: %w", err)
	}

	byDevice := make(map[int64]*Prober)

	var out []*Prober

	// Rows come newest hour first, so a device's first row is its latest.
	for _, r := range rows {
		hour, err := parseHour("probing", r.Hour)
		if err != nil {
			return nil, err
		}

		p, ok := byDevice[r.DeviceID]
		if !ok {
			p = &Prober{
				DeviceID:   r.DeviceID,
				DeviceName: displayName(r.Label, r.Hostname, r.MAC, "", r.DeviceID),
				LastHour:   hour,
			}
			byDevice[r.DeviceID] = p
			out = append(out, p)
		}

		p.Hours++
		p.Peers = max(p.Peers, r.Peers)
		p.MaxPorts = max(p.MaxPorts, r.MaxPorts)
		p.Attempts += r.Attempts
		p.Answered += r.Answered
		p.Ports = mergePorts(p.Ports, parsePortList(r.Ports))
	}

	return out, nil
}
