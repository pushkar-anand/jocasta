package inventory

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// DeviceTotal is how much one device moved over a window, both ways.
type DeviceTotal struct {
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`
	Sent       int64  `json:"sent"`
	Received   int64  `json:"received"`
}

// OrgTotal is how much the whole network exchanged with one organisation over
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

// FirstContact is a device exchanging data with an organisation for the first
// time on record.
type FirstContact struct {
	DeviceID   int64     `json:"device_id"`
	DeviceName string    `json:"device_name"`
	ASN        uint32    `json:"asn"`
	Name       string    `json:"name"`
	Short      string    `json:"short"`
	First      time.Time `json:"first"`
	Bytes      int64     `json:"bytes"`
}

// FirstContacts is every first contact since a moment.
type FirstContacts struct {
	Since    time.Time       `json:"since"`
	Contacts []*FirstContact `json:"contacts"`

	// Partial is set when traffic was first recorded after Since. Everything
	// is a first contact in the first days of collecting, so a reader needs to
	// know the list covers less than the period asked for.
	Partial bool `json:"partial"`

	// Started is when traffic was first recorded, zero when it never has.
	Started time.Time `json:"started,omitzero"`
}

// BusiestDevices returns the devices that moved the most data since the start
// of the hour containing since, busiest first. A non-empty group keeps only
// the devices in it.
func (s *Store) BusiestDevices(ctx context.Context, since time.Time, group string, limit int) ([]*DeviceTotal, error) {
	rows, err := s.q.BusiestDevices(ctx, models.BusiestDevicesParams{
		Since:     dbtype.NewTime(since.UTC().Truncate(time.Hour)),
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

// TopOrganisations returns the organisations the network exchanged the most
// with since the start of the hour containing since. A non-empty group counts
// only the devices in it.
func (s *Store) TopOrganisations(ctx context.Context, since time.Time, group string, limit int) ([]*OrgTotal, error) {
	rows, err := s.q.TopOrganisations(ctx, models.TopOrganisationsParams{
		Since:     dbtype.NewTime(since.UTC().Truncate(time.Hour)),
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

// FirstContacts returns the organisations devices exchanged data with for the
// first time since the start of the hour containing since, newest first. A
// non-empty group keeps only the devices in it.
func (s *Store) FirstContacts(ctx context.Context, since time.Time, group string, limit int) (*FirstContacts, error) {
	since = since.UTC().Truncate(time.Hour)

	out := &FirstContacts{Since: since, Contacts: []*FirstContact{}}

	earliest, err := s.q.EarliestTraffic(ctx)
	if err != nil {
		return nil, fmt.Errorf("earliest traffic: %w", err)
	}

	if earliest != "" {
		started, err := time.Parse(dbtype.Layout, earliest)
		if err != nil {
			return nil, fmt.Errorf("earliest traffic %q: %w", earliest, err)
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
		first, err := time.Parse(dbtype.Layout, r.FirstHour)
		if err != nil {
			return nil, fmt.Errorf("first contact hour %q: %w", r.FirstHour, err)
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

// OrganisationDevices returns, for each organisation, what each device
// exchanged with it since the start of the hour containing since, busiest
// device first. A non-empty group keeps only the devices in it.
func (s *Store) OrganisationDevices(ctx context.Context, since time.Time, group string) (map[uint32][]*DeviceTotal, error) {
	rows, err := s.q.OrganisationDevices(ctx, models.OrganisationDevicesParams{
		Since:     dbtype.NewTime(since.UTC().Truncate(time.Hour)),
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

// parseAddr reads an address the database wrote, and the zero address for
// anything else, which names no organisation.
func parseAddr(s string) netip.Addr {
	a, _ := netip.ParseAddr(s)

	return a
}
