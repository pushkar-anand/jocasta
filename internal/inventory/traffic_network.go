package inventory

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// DeviceTotal is how much data one device moved over a window, in both
// directions.
type DeviceTotal struct {
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`
	Sent       int64  `json:"sent"`
	Received   int64  `json:"received"`
}

// OrgTotal is how much the whole network exchanged with one organisation, over
// a window.
type OrgTotal struct {
	ASN      uint32 `json:"asn"`
	Name     string `json:"name"`
	Short    string `json:"short"`
	Sent     int64  `json:"sent"`
	Received int64  `json:"received"`

	// Devices counts the devices that reached the organisation.
	Devices int64 `json:"devices"`
}

// FirstContact is the first recorded data exchange of a device with an
// organisation.
type FirstContact struct {
	DeviceID   int64     `json:"device_id"`
	DeviceName string    `json:"device_name"`
	ASN        uint32    `json:"asn"`
	Name       string    `json:"name"`
	Short      string    `json:"short"`
	First      time.Time `json:"first"`
	Bytes      int64     `json:"bytes"`
}

// FirstContacts is every first contact since a time.
type FirstContacts struct {
	Since    time.Time       `json:"since"`
	Contacts []*FirstContact `json:"contacts"`

	// Partial is set when the first recorded traffic is after Since. In the
	// first days of collection, everything is a first contact. So a reader
	// needs to know that the list covers less than the asked period.
	Partial bool `json:"partial"`

	// Started is the time of the first recorded traffic. It is zero when
	// there is none.
	Started time.Time `json:"started,omitzero"`
}

// BusiestDevices returns the devices that moved the most data since the start
// of the hour of since, busiest first. A non-empty group keeps only its
// devices.
func (s *Store) BusiestDevices(ctx context.Context, since time.Time, group string, limit int) ([]*DeviceTotal, error) {
	rows, err := s.q.BusiestDevices(ctx, models.BusiestDevicesParams{
		Since:     hourOf(since),
		GroupName: nullString(group),
		LimitRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("busiest devices: %w", err)
	}

	out := make([]*DeviceTotal, 0, len(rows))
	for _, r := range rows {
		out = append(out, &DeviceTotal{
			DeviceID:   r.ID,
			DeviceName: displayName(r.Label, r.Hostname, r.MAC, "", r.ID),
			Sent:       r.BytesOut,
			Received:   r.BytesIn,
		})
	}

	return out, nil
}

// TopOrganisations returns the organisations with which the network exchanged
// the most, since the start of the hour of since. A non-empty group counts
// only its devices.
func (s *Store) TopOrganisations(ctx context.Context, since time.Time, group string, limit int) ([]*OrgTotal, error) {
	rows, err := s.q.TopOrganisations(ctx, models.TopOrganisationsParams{
		Since:     hourOf(since),
		GroupName: nullString(group),
		LimitRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("top organisations: %w", err)
	}

	out := make([]*OrgTotal, 0, len(rows))

	for _, r := range rows {
		number := uint32(r.PeerASN) //nolint:gosec // an ASN is 32 bits.
		name, short := orgName(number, parseAddr(r.PeerIP))

		out = append(out, &OrgTotal{
			ASN: number, Name: name, Short: short,
			Sent: r.BytesOut, Received: r.BytesIn, Devices: r.Devices,
		})
	}

	return out, nil
}

// FirstContacts returns the organisations with which devices exchanged data for
// the first time, since the start of the hour of since, newest first. A
// non-empty group keeps only its devices.
func (s *Store) FirstContacts(ctx context.Context, since time.Time, group string, limit int) (*FirstContacts, error) {
	since = since.UTC().Truncate(time.Hour)

	out := &FirstContacts{Since: since, Contacts: []*FirstContact{}}

	earliest, err := s.q.EarliestTraffic(ctx)
	if err != nil {
		return nil, fmt.Errorf("earliest traffic: %w", err)
	}

	if earliest != "" {
		started, err := parseHour("earliest traffic", earliest)
		if err != nil {
			return nil, err
		}

		out.Started = started
		out.Partial = started.After(since)
	}

	rows, err := s.q.FirstContacts(ctx, models.FirstContactsParams{
		Since:     dbtype.NewTime(since),
		GroupName: nullString(group),
		LimitRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("first contacts: %w", err)
	}

	for _, r := range rows {
		first, err := parseHour("first contact", r.FirstHour)
		if err != nil {
			return nil, err
		}

		number := uint32(r.PeerASN) //nolint:gosec // an ASN is 32 bits.
		name, short := orgName(number, parseAddr(r.PeerIP))

		out.Contacts = append(out.Contacts, &FirstContact{
			DeviceID:   r.ID,
			DeviceName: displayName(r.Label, r.Hostname, r.MAC, "", r.ID),
			ASN:        number, Name: name, Short: short,
			First: first, Bytes: r.Bytes,
		})
	}

	return out, nil
}

// OrganisationDevices returns what each device exchanged with each
// organisation, since the start of the hour of since. The busiest device comes
// first. A non-empty group keeps only its devices.
func (s *Store) OrganisationDevices(ctx context.Context, since time.Time, group string) (map[uint32][]*DeviceTotal, error) {
	rows, err := s.q.OrganisationDevices(ctx, models.OrganisationDevicesParams{
		Since:     hourOf(since),
		GroupName: nullString(group),
	})
	if err != nil {
		return nil, fmt.Errorf("organisation devices: %w", err)
	}

	out := make(map[uint32][]*DeviceTotal)

	for _, r := range rows {
		number := uint32(r.PeerASN) //nolint:gosec // an ASN is 32 bits.
		out[number] = append(out[number], &DeviceTotal{
			DeviceID:   r.ID,
			DeviceName: displayName(r.Label, r.Hostname, r.MAC, "", r.ID),
			Sent:       r.BytesOut,
			Received:   r.BytesIn,
		})
	}

	return out, nil
}

// parseAddr reads an address that the database wrote. For any other value, it
// returns the zero address, which names no organisation.
func parseAddr(s string) netip.Addr {
	a, _ := netip.ParseAddr(s)

	return a
}

// Incoming is one service of a device to which peers on the internet opened
// connections, over a window.
type Incoming struct {
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`

	Protocol uint8  `json:"protocol"`
	Port     uint16 `json:"port"`

	// Service is the usual service on Port. Nothing detects it.
	Service string `json:"service,omitempty"`

	// Connections is how many connections the internet opened. Peers is how
	// many addresses they came from. Orgs is how many organisations they came
	// from.
	Connections int64 `json:"connections"`
	Peers       int64 `json:"peers"`
	Orgs        int64 `json:"orgs"`

	// Sent and Received are from the side of the device.
	Sent     int64     `json:"sent"`
	Received int64     `json:"received"`
	LastHour time.Time `json:"last_hour"`
}

// IncomingFromInternet returns the device services to which internet peers
// opened connections, since the start of the hour of since. The most
// connections come first. A non-empty group keeps only its devices.
func (s *Store) IncomingFromInternet(ctx context.Context, since time.Time, group string, limit int) ([]*Incoming, error) {
	rows, err := s.q.IncomingFromInternet(ctx, models.IncomingFromInternetParams{
		Since:     hourOf(since),
		GroupName: nullString(group),
		LimitRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("incoming from the internet: %w", err)
	}

	out := make([]*Incoming, 0, len(rows))

	for _, r := range rows {
		last, err := parseHour("incoming", r.LastHour)
		if err != nil {
			return nil, err
		}

		in := &Incoming{
			DeviceID:    r.ID,
			DeviceName:  displayName(r.Label, r.Hostname, r.MAC, "", r.ID),
			Protocol:    uint8(r.Protocol),     //nolint:gosec // written from a uint8.
			Port:        uint16(r.ServicePort), //nolint:gosec // range enforced by the column CHECK.
			Connections: r.Connections,
			Peers:       r.Peers,
			Orgs:        r.Orgs,
			Sent:        r.BytesOut,
			Received:    r.BytesIn,
			LastHour:    last,
		}
		in.Service = scanner.ServiceName(in.Port)

		out = append(out, in)
	}

	return out, nil
}
