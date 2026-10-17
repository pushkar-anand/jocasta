package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/pkg/asn"
)

// outsideAddrTTL is how long the recorder keeps an address from the router's
// translation as its outside address, after the last flow that named it. An
// ISP that gives a new address leaves the old address behind when nothing uses
// it.
const outsideAddrTTL = 24 * time.Hour

// outsideSeen is who last named an outside address, and when.
type outsideSeen struct {
	source string
	kind   dbtype.SourceKind
	at     time.Time
}

// learnOutside remembers the addresses to which the routers translated flows on
// the way out (their outside addresses). It also remembers the routers
// themselves. The caller holds r.mu.
func (r *TrafficRecorder) learnOutside(src plugin.Plugin, flows []plugin.Flow) {
	for _, f := range flows {
		if f.NATSrc.IsValid() {
			r.outside[f.NATSrc] = outsideSeen{source: src.Name(), kind: src.Kind(), at: f.End}
		}

		if f.Exporter.IsValid() {
			r.routers[f.Exporter] = true
		}
	}
}

// toRouter puts a flow to or from the outside address of a router under the
// router that exported it. Nothing on the network has the outside address. So
// without this, a scan of it, or a VPN that ends on the router, would have no
// device for its count. A flow that the router forwarded has the address of
// the device, and does not change. The caller holds r.mu.
func (r *TrafficRecorder) toRouter(f plugin.Flow) plugin.Flow {
	if !f.Exporter.IsValid() {
		return f
	}

	if _, ok := r.outside[f.Dst]; ok {
		f.Dst = f.Exporter
	}

	if _, ok := r.outside[f.Src]; ok {
		f.Src = f.Exporter
	}

	return f
}

// forgetOutside drops the outside addresses that no flow named for some time.
// The caller holds r.mu.
func (r *TrafficRecorder) forgetOutside(now time.Time) {
	for a, seen := range r.outside {
		if now.Sub(seen.at) > outsideAddrTTL {
			delete(r.outside, a)
		}
	}
}

// recordOutside writes the outside addresses that the routers named. So a view
// can tell a router with a public address from a router behind a different
// NAT.
func (s *Store) recordOutside(ctx context.Context, outside map[netip.Addr]outsideSeen) error {
	if len(outside) == 0 {
		return nil
	}

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin outside addresses: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	stamp := s.stamp()

	for a, seen := range outside {
		src, err := q.UpsertSource(ctx, models.UpsertSourceParams{Kind: seen.kind, Name: seen.source, CreatedAt: stamp})
		if err != nil {
			return fmt.Errorf("source %s: %w", seen.source, err)
		}

		at := dbtype.NewTime(seen.at)

		err = q.UpsertOutsideAddress(ctx, models.UpsertOutsideAddressParams{
			SourceID: src.ID, Address: dbtype.NewAddr(a), FirstSeen: at, LastSeen: at,
		})
		if err != nil {
			return fmt.Errorf("outside address: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outside addresses: %w", err)
	}

	return nil
}

// writeProbe writes what an internet peer tried on a device in one flush.
// outside tells that the peer tried the outside address of the router. Then
// the device is the router.
func (s *Store) writeProbe(
	ctx context.Context,
	q *models.Queries,
	k attemptKey,
	a *attemptSample,
	device, srcID int64,
	outside bool,
) error {
	hour := dbtype.NewTime(k.hour)
	peer := dbtype.NewAddr(k.src)

	if a.attempts > 0 {
		p := models.UpsertProbesParams{
			SourceID:  srcID,
			DeviceID:  device,
			Hour:      hour,
			PeerIP:    peer,
			Protocol:  int64(k.protocol),
			Attempts:  clampInt64(a.attempts),
			Answered:  clampInt64(a.answered),
			PortCount: int64(a.portCount),
		}

		if outside {
			p.Outside = 1
		}

		if org, ok := asn.Lookup(k.src); ok {
			p.PeerASN = sql.NullInt64{Int64: int64(org.ASN), Valid: true}
		}

		ports := a.ports

		prev, err := q.ProbePorts(ctx, models.ProbePortsParams{
			DeviceID: device, Hour: hour, SourceID: srcID, PeerIP: peer, Protocol: p.Protocol,
		})

		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("probe ports for device %d: %w", device, err)
		default:
			ports = mergePorts(ports, parsePortList(prev.Ports))
			p.PortCount = max(p.PortCount, prev.PortCount, int64(len(ports)))
		}

		p.Ports = formatPorts(ports)

		if err := q.UpsertProbes(ctx, p); err != nil {
			return fmt.Errorf("probes of device %d: %w", device, err)
		}
	}

	if a.lateAnswered > 0 {
		err := q.AnswerProbes(ctx, models.AnswerProbesParams{
			Late: clampInt64(a.lateAnswered), DeviceID: device, Hour: hour,
			SourceID: srcID, PeerIP: peer, Protocol: int64(k.protocol),
		})
		if err != nil {
			return fmt.Errorf("late answers to probes of device %d: %w", device, err)
		}
	}

	return nil
}

// probeTarget is the target device of an attempt that counts as a probe from
// the internet. Such an attempt is from a public address, on a device. Else,
// it is zero.
func probeTarget(k attemptKey, holders map[netip.Addr]int64) int64 {
	if holders[k.src] != 0 || !asn.IsPublic(k.src) {
		return 0
	}

	return holders[k.dst]
}

// OutsideAddresses returns the outside addresses that any router named since a
// time.
func (s *Store) OutsideAddresses(ctx context.Context, since time.Time) ([]netip.Addr, error) {
	rows, err := s.q.OutsideAddresses(ctx, dbtype.NewTime(since))
	if err != nil {
		return nil, fmt.Errorf("outside addresses: %w", err)
	}

	out := make([]netip.Addr, 0, len(rows))

	for _, r := range rows {
		a, err := netip.ParseAddr(r)
		if err != nil {
			return nil, fmt.Errorf("outside address %q: %w", r, err)
		}

		out = append(out, a)
	}

	return out, nil
}

// Probed is a device that the internet tried over a window, with no data on
// the connections.
type Probed struct {
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`

	// Outside tells that the target was the outside address of the router.
	// Then the device is the router.
	Outside bool `json:"outside,omitempty"`

	// Peers is how many addresses tried. Orgs is how many organisations they
	// belong to.
	Peers int64 `json:"peers"`
	Orgs  int64 `json:"orgs"`

	Attempts int64 `json:"attempts"`
	Answered int64 `json:"answered"`

	// MaxPorts is the most ports that one address tried in an hour. Ports is
	// the lowest of the tried ports.
	MaxPorts int64    `json:"max_ports"`
	Ports    []uint16 `json:"ports,omitempty"`

	LastHour time.Time `json:"last_hour"`
}

// ProbedDevices returns the devices that the internet probed since the start of
// the hour of since, most probes first. A device tried on its own address and
// through the outside address of the router is listed once for each. A
// non-empty group keeps only its devices.
func (s *Store) ProbedDevices(ctx context.Context, since time.Time, group string) ([]*Probed, error) {
	return s.probed(ctx, since, group, 0)
}

// DeviceProbed is ProbedDevices for one device. It has a row for its own
// address, and one for the outside address of the router. Each row is there
// only when the address was tried.
func (s *Store) DeviceProbed(ctx context.Context, id int64, since time.Time) ([]*Probed, error) {
	return s.probed(ctx, since, "", id)
}

// probed reads the probed devices. It limits them to a group or a device, if
// either is set.
func (s *Store) probed(ctx context.Context, since time.Time, group string, device int64) ([]*Probed, error) {
	rows, err := s.q.ProbedDevices(ctx, models.ProbedDevicesParams{
		Since:     hourOf(since),
		GroupName: nullString(group),
		DeviceID:  nullInt64(device),
	})
	if err != nil {
		return nil, fmt.Errorf("probed devices: %w", err)
	}

	out := make([]*Probed, 0, len(rows))

	for _, r := range rows {
		last, err := parseHour("probe", r.LastHour)
		if err != nil {
			return nil, err
		}

		out = append(out, &Probed{
			DeviceID:   r.ID,
			DeviceName: displayName(r.Label, r.Hostname, r.MAC, "", r.ID),
			Outside:    r.Outside != 0,
			Peers:      r.Peers,
			Orgs:       r.Orgs,
			Attempts:   r.Attempts,
			Answered:   r.Answered,
			MaxPorts:   r.MaxPorts,
			Ports:      mergePorts(nil, parsePortList(r.Ports)),
			LastHour:   last,
		})
	}

	return out, nil
}
