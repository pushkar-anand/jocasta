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

// deviceTrafficLimit caps how many peer-and-service rows a device's traffic
// view reads. The busiest come first, so what falls past it is the long tail
// a page would not show anyway.
const deviceTrafficLimit = 200

// TrafficPeer is what a device exchanged with one peer on one service over a
// window.
type TrafficPeer struct {
	// DeviceID and DeviceName name the peer when it was a known device. Zero
	// for anything else: an internet address, or a local one no device holds.
	DeviceID   int64  `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`

	IP netip.Addr `json:"ip"`

	// Name is the peer's reverse DNS name, when it had one. It is chosen by
	// whoever runs the peer's DNS, not by the user.
	Name string `json:"name,omitempty"`

	Protocol    uint8  `json:"protocol"`
	ServicePort uint16 `json:"service_port"`

	// Service is the service usually found on ServicePort, not one that was
	// detected.
	Service string `json:"service,omitempty"`

	// Sent and Received are bytes from the device's side of the conversation.
	Sent     int64 `json:"sent"`
	Received int64 `json:"received"`

	// Connections counts the conversations the device started to this peer
	// and service.
	Connections int64 `json:"connections"`

	// LastHour is the start of the most recent hour the two exchanged
	// anything.
	LastHour time.Time `json:"last_hour"`
}

// TrafficOrg is every internet peer one organisation announces, added up.
type TrafficOrg struct {
	// ASN is zero for addresses no organisation announces, each of which is
	// its own entry named by its address.
	ASN   uint32 `json:"asn,omitempty"`
	Name  string `json:"name"`
	Short string `json:"short"`

	Sent     int64     `json:"sent"`
	Received int64     `json:"received"`
	LastHour time.Time `json:"last_hour"`

	Peers []*TrafficPeer `json:"peers"`
}

// DeviceTraffic is who a device talked to since a moment, split the way a
// person asks about it: what on the home network, and who out on the
// internet.
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
// start of the hour containing since.
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

	out := &DeviceTraffic{Since: since, Local: []*TrafficPeer{}, Internet: []*TrafficOrg{}}
	orgs := make(map[uint32]*TrafficOrg)

	for _, row := range rows {
		p, err := trafficPeer(row)
		if err != nil {
			return nil, err
		}

		if p.DeviceID != 0 || !asn.IsPublic(p.IP) {
			out.Local = append(out.Local, p)

			continue
		}

		// An address no organisation announces is its own entry; every other
		// peer joins the entry for whoever announces it.
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

	// Rows arrive busiest first, but an organisation's total is only known
	// once all its peers are in.
	slices.SortStableFunc(out.Internet, func(a, b *TrafficOrg) int {
		return cmp.Compare(b.Sent+b.Received, a.Sent+a.Received)
	})

	return out, nil
}

// TrafficRecorded reports whether any traffic has been recorded, which is how
// a view tells a device that exchanged nothing from a network nothing is
// collecting traffic on.
func (s *Store) TrafficRecorded(ctx context.Context) (bool, error) {
	n, err := s.q.AnyTraffic(ctx)
	if err != nil {
		return false, fmt.Errorf("any traffic: %w", err)
	}

	return n != 0, nil
}

// newTrafficOrg names the entry for the organisation announcing number, or
// for p alone when number is zero.
func newTrafficOrg(number uint32, p *TrafficPeer) *TrafficOrg {
	if number == 0 {
		label := cmp.Or(p.Name, p.IP.String())

		return &TrafficOrg{Name: label, Short: label}
	}

	// The table may have been refreshed since the row was written; the number
	// is what was recorded, so a name that no longer matches it is not used.
	if o, ok := asn.Lookup(p.IP); ok && o.ASN == number {
		return &TrafficOrg{ASN: number, Name: o.Name, Short: o.Short}
	}

	label := fmt.Sprintf("AS%d", number)

	return &TrafficOrg{ASN: number, Name: label, Short: label}
}

func trafficPeer(row *models.DeviceTrafficRow) (*TrafficPeer, error) {
	ip, err := netip.ParseAddr(row.PeerIP)
	if err != nil {
		return nil, fmt.Errorf("traffic peer %q: %w", row.PeerIP, err)
	}

	last, err := time.Parse(dbtype.Layout, row.LastHour)
	if err != nil {
		return nil, fmt.Errorf("traffic hour %q: %w", row.LastHour, err)
	}

	p := &TrafficPeer{
		DeviceID:    row.PeerDeviceID,
		IP:          ip,
		Name:        row.PeerName,
		Protocol:    uint8(row.Protocol),     //nolint:gosec // written from a uint8.
		ServicePort: uint16(row.ServicePort), //nolint:gosec // range enforced by the column CHECK.
		Sent:        row.BytesOut,
		Received:    row.BytesIn,
		Connections: row.Connections,
		LastHour:    last,
	}

	p.Service = scanner.ServiceName(p.ServicePort)

	if p.DeviceID != 0 {
		p.DeviceName = displayName(row.PeerLabel, row.PeerHostname, row.PeerMAC, ip.String(), p.DeviceID)
	}

	return p, nil
}
