package inventory

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/pushkar-anand/jocasta/pkg/geo"
)

// countryTop is how many devices and organisations a country lists; the rest
// are counted.
const countryTop = 5

// CountryTraffic is what the network exchanged with one country over a window.
type CountryTraffic struct {
	Code, Name string
	Bytes      int64

	// Active is set when anything in it was exchanged in the recent window.
	Active bool

	// Devices and Orgs are the busiest that talked to it, and MoreDevices,
	// MoreOrgs how many others did.
	Devices               []*CountryPart
	Orgs                  []*CountryPart
	MoreDevices, MoreOrgs int
}

// CountryPart is one device or organisation's share of a country's traffic.
// ID is the device's id, zero for an organisation.
type CountryPart struct {
	ID    int64
	Name  string
	Bytes int64
}

// TrafficByCountry returns what the network exchanged with each country since
// the start of the hour holding since, busiest first, with what recent covers
// marked active. A country is where an address is registered, as package geo
// has it; addresses it cannot place are left out.
func (s *Store) TrafficByCountry(ctx context.Context, since time.Time, recent []RecentEdge) ([]*CountryTraffic, error) {
	hour := hourOf(since)

	rows, err := s.q.TrafficWorldPeers(ctx, hour)
	if err != nil {
		return nil, fmt.Errorf("traffic by country: %w", err)
	}

	devRows, err := s.q.TrafficMapDevices(ctx, hour)
	if err != nil {
		return nil, fmt.Errorf("traffic by country devices: %w", err)
	}

	names := make(map[int64]string, len(devRows))
	for _, r := range devRows {
		names[r.ID] = displayName(r.Label, r.Hostname, r.MAC, "", r.ID)
	}

	type tally struct {
		c       *CountryTraffic
		devices map[int64]int64
		orgs    map[string]int64
	}

	byCode := make(map[string]*tally)

	for _, r := range rows {
		code, ok := geo.Lookup(r.PeerIP.Addr)
		if !ok {
			continue
		}

		t, ok := byCode[code]
		if !ok {
			c := &CountryTraffic{Code: code, Name: code}
			if country, ok := geo.CountryOf(code); ok {
				c.Name = country.Name
			}

			t = &tally{c: c, devices: make(map[int64]int64), orgs: make(map[string]int64)}
			byCode[code] = t
		}

		t.c.Bytes += r.Bytes
		t.devices[r.DeviceID] += r.Bytes

		_, short := orgName(uint32(r.PeerASN), r.PeerIP.Addr) //nolint:gosec // an ASN is 32 bits.
		t.orgs[short] += r.Bytes
	}

	out := make([]*CountryTraffic, 0, len(byCode))

	for _, t := range byCode {
		for id, n := range t.devices {
			t.c.Devices = append(t.c.Devices, &CountryPart{ID: id, Name: cmp.Or(names[id], fmt.Sprintf("device %d", id)), Bytes: n})
		}

		for name, n := range t.orgs {
			t.c.Orgs = append(t.c.Orgs, &CountryPart{Name: name, Bytes: n})
		}

		t.c.Devices, t.c.MoreDevices = topParts(t.c.Devices)
		t.c.Orgs, t.c.MoreOrgs = topParts(t.c.Orgs)
		out = append(out, t.c)
	}

	markCountries(out, recent)

	slices.SortFunc(out, func(a, b *CountryTraffic) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.Code, b.Code))
	})

	return out, nil
}

// markCountries sets Active on each country an address in recent is placed
// in. Only the ends on the public internet can be.
func markCountries(countries []*CountryTraffic, recent []RecentEdge) {
	byCode := make(map[string]*CountryTraffic, len(countries))
	for _, c := range countries {
		byCode[c.Code] = c
	}

	for _, e := range recent {
		for _, a := range [...]netip.Addr{e.A, e.B} {
			if code, ok := geo.Lookup(a); ok {
				if c := byCode[code]; c != nil {
					c.Active = true
				}
			}
		}
	}
}

// topParts keeps the busiest countryTop parts and counts the rest.
func topParts(parts []*CountryPart) ([]*CountryPart, int) {
	slices.SortFunc(parts, func(a, b *CountryPart) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.Name, b.Name))
	})

	if len(parts) <= countryTop {
		return parts, 0
	}

	return parts[:countryTop], len(parts) - countryTop
}
