package inventory

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/pkg/asn"
)

// deviceTrafficLimit limits how many peer-and-service rows the traffic view of
// a device reads. The busiest come first. So the rows after the limit are the
// long tail, which a page would not show.
const deviceTrafficLimit = 200

// TrafficPeer is what a device exchanged with one peer on one service, over a
// window.
type TrafficPeer struct {
	// DeviceID and DeviceName name the peer when it was a known device. They
	// are zero for all other peers: an internet address, or a local address of
	// no device.
	DeviceID   int64  `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`

	IP netip.Addr `json:"ip"`

	// Name is the reverse DNS name of the peer, if it had one. The operator of
	// the DNS of the peer chooses it.
	Name string `json:"name,omitempty"`

	// LookedUp is the names that the device looked up, with the address of
	// the peer in the answer, over the same window. The most frequent name is
	// first. It is empty for a known device.
	LookedUp []string `json:"looked_up,omitempty"`

	Protocol    uint8  `json:"protocol"`
	ServicePort uint16 `json:"service_port"`

	// Service is the usual service on ServicePort. Nothing detects it.
	Service string `json:"service,omitempty"`

	// Sent and Received are bytes, from the side of the device.
	Sent     int64 `json:"sent"`
	Received int64 `json:"received"`

	// Connections counts the conversations that the device started to this
	// peer and service. ConnectionsIn counts the conversations that the peer
	// started to the service of the device.
	Connections   int64 `json:"connections"`
	ConnectionsIn int64 `json:"connections_in,omitempty"`

	// LastHour is the start of the most recent hour in which the two
	// exchanged something.
	LastHour time.Time `json:"last_hour"`
}

// TrafficOrg is the total of every internet peer that one organisation
// announces.
type TrafficOrg struct {
	// ASN is zero for addresses that no organisation announces. Each such
	// address is its own entry, with its address as name.
	ASN   uint32 `json:"asn,omitempty"`
	Name  string `json:"name"`
	Short string `json:"short"`

	Sent     int64     `json:"sent"`
	Received int64     `json:"received"`
	LastHour time.Time `json:"last_hour"`

	Peers []*TrafficPeer `json:"peers"`
}

// DeviceTraffic is who a device talked to since a time. It is split as a
// person asks: what on the home network, and who on the internet.
type DeviceTraffic struct {
	Since    time.Time      `json:"since"`
	Local    []*TrafficPeer `json:"local"`
	Internet []*TrafficOrg  `json:"internet"`
}

// Empty reports whether nothing was exchanged in the window.
func (t *DeviceTraffic) Empty() bool {
	return len(t.Local) == 0 && len(t.Internet) == 0
}

// DeviceTraffic returns what device id exchanged, and with whom, since the
// start of the hour of since.
func (s *Store) DeviceTraffic(ctx context.Context, id int64, since time.Time) (*DeviceTraffic, error) {
	since = since.UTC().Truncate(time.Hour)

	rows, err := s.q.DeviceTraffic(ctx, models.DeviceTrafficParams{
		DeviceID: id,
		Hour:     dbtype.NewTime(since),
		Limit:    deviceTrafficLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("traffic for device %d: %w", id, err)
	}

	answers, err := s.deviceAnswers(ctx, id, since)
	if err != nil {
		return nil, err
	}

	out := &DeviceTraffic{Since: since, Local: []*TrafficPeer{}, Internet: []*TrafficOrg{}}
	orgs := make(map[uint32]*TrafficOrg)

	for _, row := range rows {
		p, err := trafficPeer(row)
		if err != nil {
			return nil, err
		}

		if p.DeviceID == 0 {
			p.LookedUp = answers[p.IP]
		}

		if p.DeviceID != 0 || !asn.IsPublic(p.IP) {
			out.Local = append(out.Local, p)

			continue
		}

		// An address that no organisation announces is its own entry. Every
		// other peer joins the entry of the organisation that announces it.
		number := uint32(row.PeerASN) //nolint:gosec // an ASN is 32 bits.

		org, ok := orgs[number]
		if !ok || number == 0 {
			org = newTrafficOrg(number, p)
			out.Internet = append(out.Internet, org)

			if number != 0 {
				orgs[number] = org
			}
		}

		org.Peers = append(org.Peers, p)
		org.Sent += p.Sent
		org.Received += p.Received

		if p.LastHour.After(org.LastHour) {
			org.LastHour = p.LastHour
		}
	}

	// Rows arrive busiest first. But the total of an organisation is known
	// only after all its peers are in.
	slices.SortStableFunc(out.Internet, func(a, b *TrafficOrg) int {
		return cmp.Compare(b.Sent+b.Received, a.Sent+a.Received)
	})

	return out, nil
}

// TrafficRecorded reports whether Jocasta recorded any traffic. So a view can
// tell a device that exchanged nothing from a network where no source
// collects traffic.
func (s *Store) TrafficRecorded(ctx context.Context) (bool, error) {
	n, err := s.q.AnyTraffic(ctx)
	if err != nil {
		return false, fmt.Errorf("any traffic: %w", err)
	}

	return n != 0, nil
}

// newTrafficOrg names the entry for the organisation that announces number. If
// number is zero, the entry is for p alone.
func newTrafficOrg(number uint32, p *TrafficPeer) *TrafficOrg {
	if number == 0 {
		label := cmp.Or(p.Name, p.IP.String())
		if len(p.LookedUp) > 0 {
			label = p.LookedUp[0]
		}

		return &TrafficOrg{Name: label, Short: label}
	}

	name, short := orgName(number, p.IP)

	return &TrafficOrg{ASN: number, Name: name, Short: short}
}

// orgName names the organisation of ASN number. The lookup uses an address
// recorded for it. The table can have a refresh after the write of the row.
// The number is what was recorded. So if the address no longer maps to a name,
// orgName does not use that name. The number replaces it.
func orgName(number uint32, ip netip.Addr) (name, short string) {
	if o, ok := asn.Lookup(ip); ok && o.ASN == number {
		return o.Name, o.Short
	}

	label := fmt.Sprintf("AS%d", number)

	return label, label
}

// trafficPeer converts a stored aggregate to a peer for display. It refuses
// addresses and hour values that do not parse.
func trafficPeer(row *models.DeviceTrafficRow) (*TrafficPeer, error) {
	ip, err := netip.ParseAddr(row.PeerIP)
	if err != nil {
		return nil, fmt.Errorf("traffic peer %q: %w", row.PeerIP, err)
	}

	last, err := parseHour("traffic", row.LastHour)
	if err != nil {
		return nil, err
	}

	p := &TrafficPeer{
		DeviceID:      row.PeerDeviceID,
		IP:            ip,
		Name:          row.PeerName,
		Protocol:      uint8(row.Protocol),     //nolint:gosec // written from a uint8.
		ServicePort:   uint16(row.ServicePort), //nolint:gosec // range enforced by the column CHECK.
		Sent:          row.BytesOut,
		Received:      row.BytesIn,
		Connections:   row.Connections,
		ConnectionsIn: row.ConnectionsIn,
		LastHour:      last,
	}

	p.Service = scanner.ServiceName(p.ServicePort)

	if p.DeviceID != 0 {
		p.DeviceName = displayName(row.PeerLabel, row.PeerHostname, row.PeerMAC, ip.String(), p.DeviceID)
	}

	return p, nil
}
