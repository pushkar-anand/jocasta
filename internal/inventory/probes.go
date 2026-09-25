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

// outsideAddrTTL is how long an address the router translated to is kept as
// its outside address after the last flow that named it. An ISP that hands
// out a new address leaves the old one behind once nothing uses it.
const outsideAddrTTL = 24 * time.Hour

// outsideSeen is who last named an outside address, and when.
type outsideSeen struct {
	source string
	kind   dbtype.SourceKind
	at     time.Time
}

// learnOutside remembers the addresses the routers translated flows to on the
// way out (their outside addresses) and the routers themselves. The caller
// holds r.mu.
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

// toRouter files a flow to or from a router's outside address under the
// router that exported it. Nothing on the network holds the outside address,
// so without this a scan of it, or a VPN ended on the router, would have no
// device to be counted on. A flow the router forwarded carries the device's
// own address, and is left alone. The caller holds r.mu.
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

// forgetOutside drops the outside addresses no flow has named for a while.
// The caller holds r.mu.
func (r *TrafficRecorder) forgetOutside(now time.Time) {
	for a, seen := range r.outside {
		if now.Sub(seen.at) > outsideAddrTTL {
			delete(r.outside, a)
		}
	}
}

// recordOutside writes the outside addresses the routers named, so a view can
// tell a router with a public address from one behind another NAT.
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
// outside says the router's outside address was what it tried, and the device
// is the router.
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

// probeTarget is the device an attempt was made on when it counts as a probe
// from the internet: made by a public address, on a device. Zero otherwise.
func probeTarget(k attemptKey, holders map[netip.Addr]int64) int64 {
	if holders[k.src] != 0 || !asn.IsPublic(k.src) {
		return 0
	}

	return holders[k.dst]
}

// OutsideAddresses returns the outside addresses any router named since a
// moment.
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

// Probed is a device the internet tried over a window without the connections
// carrying data.
type Probed struct {
	DeviceID   int64  `json:"device_id"`
	DeviceName string `json:"device_name"`

	// Outside says the router's outside address was what was tried, and the
	// device is the router.
	Outside bool `json:"outside,omitempty"`

	// Peers is how many addresses tried, Orgs how many organisations they
	// belong to.
	Peers int64 `json:"peers"`
	Orgs  int64 `json:"orgs"`

	Attempts int64 `json:"attempts"`
	Answered int64 `json:"answered"`

	// MaxPorts is the most ports one address tried in an hour, and Ports
	// the lowest of those tried.
	MaxPorts int64    `json:"max_ports"`
	Ports    []uint16 `json:"ports,omitempty"`

	LastHour time.Time `json:"last_hour"`
}

// ProbedDevices returns the devices the internet probed since the start of the
// hour containing since, most probes first. A device tried both on its own
// and through the router's outside address is listed once for each. A
// non-empty group keeps only the devices in it.
func (s *Store) ProbedDevices(ctx context.Context, since time.Time, group string) ([]*Probed, error) {
	rows, err := s.q.ProbedDevices(ctx, models.ProbedDevicesParams{
		Since:     dbtype.NewTime(since.UTC().Truncate(time.Hour)),
		GroupName: nullString(group),
	})
	if err != nil {
		return nil, fmt.Errorf("probed devices: %w", err)
	}

	out := make([]*Probed, 0, len(rows))

	for _, r := range rows {
		last, err := time.Parse(dbtype.Layout, r.LastHour)
		if err != nil {
			return nil, fmt.Errorf("probe hour %q: %w", r.LastHour, err)
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
