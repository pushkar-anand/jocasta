package inventory

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/pkg/asn"
)

// The map shows the busiest devices and organisations; past these the rest are
// only counted, since a picture with hundreds of nodes shows nothing.
const (
	MapMaxDevices = 60
	MapMaxOrgs    = 12
)

// TrafficMap is who talked to whom over a window, as nodes and the lines
// between them.
type TrafficMap struct {
	Devices []*MapDevice
	Orgs    []*MapOrg
	Links   []*MapLink

	// MoreDevices and MoreOrgs count what had traffic but was left off.
	MoreDevices, MoreOrgs int
}

// MapDevice is one device on the map.
type MapDevice struct {
	ID   int64
	Name string
	Type string

	// NetworkID is the recorded network one of its current addresses is on,
	// zero when none is.
	NetworkID int64

	Bytes int64

	// Active is set when it exchanged anything in the recent window.
	Active bool
}

// MapOrg is one organisation on the internet side of the map.
type MapOrg struct {
	ASN         uint32
	Name, Short string
	Bytes       int64
	Active      bool
}

// MapLink is traffic between a device and another device or an organisation.
// Exactly one of PeerDevice and PeerASN is set.
type MapLink struct {
	Device     int64
	PeerDevice int64
	PeerASN    uint32
	Bytes      int64
	Active     bool

	// Services are what the pair used, by port.
	Services []MapService
}

// MapService is one service a link carried: its protocol and the port that
// names it, and the name usually found there, empty when there is none.
type MapService struct {
	Protocol uint8
	Port     uint16
	Name     string
}

// mapLinkKey names a link; a device pair is kept lower id first, as the query
// returns it.
type mapLinkKey struct {
	device, peerDevice int64
	peerASN            uint32
}

// TrafficMap returns the map of traffic since the start of the hour holding
// since, with what recent covers marked active. recent is what the recorder
// saw lately, by address; it is resolved to devices by the addresses they
// hold now.
func (s *Store) TrafficMap(ctx context.Context, since time.Time, recent []RecentEdge) (*TrafficMap, error) {
	hour := dbtype.NewTime(since.UTC().Truncate(time.Hour))

	devRows, err := s.q.TrafficMapDevices(ctx, hour)
	if err != nil {
		return nil, fmt.Errorf("traffic map devices: %w", err)
	}

	linkRows, err := s.q.TrafficMapLinks(ctx, hour)
	if err != nil {
		return nil, fmt.Errorf("traffic map links: %w", err)
	}

	m := &TrafficMap{}
	devices := make(map[int64]*MapDevice, len(devRows))

	for i, r := range devRows {
		if i >= MapMaxDevices {
			m.MoreDevices = len(devRows) - MapMaxDevices

			break
		}

		d := &MapDevice{
			ID: r.ID, Name: displayName(r.Label, r.Hostname, r.MAC, "", r.ID),
			Type: r.DeviceType, NetworkID: r.NetworkID, Bytes: r.Bytes,
		}
		devices[d.ID] = d
		m.Devices = append(m.Devices, d)
	}

	perASN := make(map[uint32]*MapOrg)

	for _, r := range linkRows {
		if r.PeerDeviceID != 0 || r.PeerASN == 0 {
			continue
		}

		number := uint32(r.PeerASN) //nolint:gosec // an ASN is 32 bits.

		o, ok := perASN[number]
		if !ok {
			name, short := orgName(number, parseAddr(r.PeerIP))
			o = &MapOrg{ASN: number, Name: name, Short: short}
			perASN[number] = o
		}

		o.Bytes += r.Bytes
	}

	var canon map[uint32]uint32

	m.Orgs, canon = mergeOrgs(perASN)

	orgs := make(map[uint32]*MapOrg, len(m.Orgs))

	if len(m.Orgs) > MapMaxOrgs {
		m.MoreOrgs = len(m.Orgs) - MapMaxOrgs
		m.Orgs = m.Orgs[:MapMaxOrgs]
	}

	for _, o := range m.Orgs {
		orgs[o.ASN] = o
	}

	links := make(map[mapLinkKey]*MapLink)

	for _, r := range linkRows {
		number := canon[uint32(r.PeerASN)] //nolint:gosec // an ASN is 32 bits.
		if devices[r.DeviceID] == nil ||
			(r.PeerDeviceID != 0 && devices[r.PeerDeviceID] == nil) ||
			(r.PeerDeviceID == 0 && orgs[number] == nil) {
			continue
		}

		k := mapLinkKey{device: r.DeviceID, peerDevice: r.PeerDeviceID}
		if r.PeerDeviceID == 0 {
			k.peerASN = number
		}

		l, ok := links[k]
		if !ok {
			l = &MapLink{Device: k.device, PeerDevice: k.peerDevice, PeerASN: k.peerASN}
			links[k] = l
			m.Links = append(m.Links, l)
		}

		l.Bytes += r.Bytes
		l.Services = mergeServices(l.Services, parseServices(r.Services))
	}

	if err := s.markActive(ctx, recent, devices, orgs, canon, links); err != nil {
		return nil, err
	}

	return m, nil
}

// markActive sets Active on what the recent edges cover: both devices of a
// pair and the line between them, or a device, the organisation announcing
// its peer and the line between those.
func (s *Store) markActive(
	ctx context.Context,
	recent []RecentEdge,
	devices map[int64]*MapDevice,
	orgs map[uint32]*MapOrg,
	canon map[uint32]uint32,
	links map[mapLinkKey]*MapLink,
) error {
	if len(recent) == 0 {
		return nil
	}

	rows, err := s.q.AllCurrentAddresses(ctx)
	if err != nil {
		return fmt.Errorf("current addresses: %w", err)
	}

	holders := make(map[netip.Addr]int64, len(rows))
	for _, r := range rows {
		holders[r.IP.Addr] = r.DeviceID
	}

	mark := func(device int64) {
		if d := devices[device]; d != nil {
			d.Active = true
		}
	}

	for _, e := range recent {
		a, b := holders[e.A], holders[e.B]

		switch {
		case a != 0 && b != 0:
			mark(a)
			mark(b)

			if l := links[mapLinkKey{device: min(a, b), peerDevice: max(a, b)}]; l != nil {
				l.Active = true
			}
		case a != 0 || b != 0:
			device, peer := a, e.B
			if device == 0 {
				device, peer = b, e.A
			}

			mark(device)

			o, ok := asn.Lookup(peer)
			if !ok {
				continue
			}

			number := canon[o.ASN]

			if org := orgs[number]; org != nil {
				org.Active = true
			}

			if l := links[mapLinkKey{device: device, peerASN: number}]; l != nil {
				l.Active = true
			}
		}
	}

	return nil
}

// mergeOrgs folds the organisations that go by one short name (a company
// announcing from several ASNs) into one, busiest first, as the Traffic page
// does. Each keeps the number of its busiest ASN; canon maps every ASN to the
// number it was folded under.
func mergeOrgs(perASN map[uint32]*MapOrg) (merged []*MapOrg, canon map[uint32]uint32) {
	all := make([]*MapOrg, 0, len(perASN))
	for _, o := range perASN {
		all = append(all, o)
	}

	slices.SortFunc(all, func(a, b *MapOrg) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.ASN, b.ASN))
	})

	canon = make(map[uint32]uint32, len(all))
	byShort := make(map[string]*MapOrg, len(all))

	for _, o := range all {
		if into, ok := byShort[o.Short]; ok {
			into.Bytes += o.Bytes
			canon[o.ASN] = into.ASN

			continue
		}

		cp := *o
		byShort[o.Short] = &cp
		canon[o.ASN] = o.ASN

		merged = append(merged, &cp)
	}

	slices.SortFunc(merged, func(a, b *MapOrg) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.ASN, b.ASN))
	})

	return merged, canon
}

// parseServices reads the "6/443,17/123" list the map query returns, skipping
// what does not parse.
func parseServices(list string) []MapService {
	var out []MapService

	for item := range strings.SplitSeq(list, ",") {
		proto, port, ok := strings.Cut(item, "/")
		if !ok {
			continue
		}

		pr, err := strconv.ParseUint(proto, 10, 8)
		if err != nil {
			continue
		}

		po, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			continue
		}

		out = append(out, MapService{Protocol: uint8(pr), Port: uint16(po)})
	}

	return out
}

// mergeServices adds more to have, once each, named and by port.
func mergeServices(have, more []MapService) []MapService {
	for _, m := range more {
		if slices.ContainsFunc(have, func(h MapService) bool { return h.Protocol == m.Protocol && h.Port == m.Port }) {
			continue
		}

		if m.Protocol == protoTCP {
			m.Name = scanner.ServiceName(m.Port)
		}

		have = append(have, m)
	}

	slices.SortFunc(have, func(a, b MapService) int {
		return cmp.Or(cmp.Compare(a.Port, b.Port), cmp.Compare(a.Protocol, b.Protocol))
	})

	return have
}
