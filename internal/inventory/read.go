package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/pkg/cursor"
)

// ErrNotFound is returned when a read names something the inventory does not
// hold. Callers match on it rather than on sql.ErrNoRows, which would tie every
// surface to the storage layer.
var ErrNotFound = errors.New("not found")

// DiscoveryWindow is how far back Stats counts a device as newly discovered.
const DiscoveryWindow = 24 * time.Hour

// ListDevices returns the devices matching f.
//
// The whole matching set is read: a homelab holds tens to low hundreds of
// devices, and ordering by address or by name has to happen in Go, which can
// only be right if it sees every row rather than one page of them.
func (s *Store) ListDevices(ctx context.Context, f DeviceFilter) ([]*Device, error) {
	rows, err := s.q.ListDevices(ctx, models.ListDevicesParams{
		IncludeIgnored: f.IncludeIgnored,
		GroupName:      nullString(f.Group),
		Q:              nullString(f.Query),
	})
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}

	cutoff := s.onlineCutoff()
	devices := make([]*Device, 0, len(rows))

	for _, r := range rows {
		d := newDevice(&r.Device, cutoff)
		d.Current = parseAddrs(r.CurrentIps)

		// Online is decided against the clock, not the query, so the status
		// filter is applied here rather than as one more SQL clause.
		if !f.Status.admits(d.Online) {
			continue
		}

		devices = append(devices, d)
	}

	sortDevices(devices, f.Sort)

	return devices, nil
}

// GetDevice returns one device with its full address history.
func (s *Store) GetDevice(ctx context.Context, id int64) (*Device, error) {
	row, err := s.q.GetDevice(ctx, id)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("device %d: %w", id, ErrNotFound)
	case err != nil:
		return nil, fmt.Errorf("device %d: %w", id, err)
	}

	d := newDevice(row, s.onlineCutoff())

	addrs, err := s.q.ListDeviceAddresses(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("addresses of device %d: %w", id, err)
	}

	d.Addresses = make([]*Address, 0, len(addrs))

	for _, a := range addrs {
		d.Addresses = append(d.Addresses, newAddress(a))

		if a.IsCurrent {
			d.Current = append(d.Current, a.IP.Addr)
		}
	}

	slices.SortFunc(d.Current, func(a, b netip.Addr) int { return a.Compare(b) })

	return d, nil
}

// DeviceEvents returns the most recent events recorded against one device.
//
// The events carry no DeviceName: the caller named the device it asked about.
func (s *Store) DeviceEvents(ctx context.Context, id int64, limit int) ([]*Event, error) {
	rows, err := s.q.ListDeviceEvents(ctx, models.ListDeviceEventsParams{
		DeviceID: sql.NullInt64{Int64: id, Valid: true},
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("events of device %d: %w", id, err)
	}

	events := make([]*Event, 0, len(rows))
	for _, r := range rows {
		events = append(events, newEvent(r))
	}

	return events, nil
}

// ListEvents returns one page of the change log, most recent first.
func (s *Store) ListEvents(ctx context.Context, p Page) (*EventPage, error) {
	rows, err := s.q.ListEvents(ctx, models.PageParams{Cursor: p.Cursor, Limit: p.seek()})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	events := make([]*Event, 0, len(rows))

	for _, r := range rows {
		e := newEvent(&r.Event)
		e.DeviceName = displayName(
			r.DeviceLabel.String, r.DeviceHostname.String, macString(r.DeviceMAC), "", e.DeviceID,
		)
		events = append(events, e)
	}

	events, next := trim(events, p.Limit, func(e *Event) Cursor {
		return Cursor{Value: e.At, ID: e.ID, Order: cursor.Desc}
	})

	return &EventPage{Events: events, Next: next}, nil
}

// ListScans returns one page of the scan history, most recent first.
func (s *Store) ListScans(ctx context.Context, p Page) (*ScanPage, error) {
	rows, err := s.q.ListScans(ctx, models.PageParams{Cursor: p.Cursor, Limit: p.seek()})
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}

	scans := make([]*Scan, 0, len(rows))
	for _, r := range rows {
		scans = append(scans, newScan(&r.Scan, r.SourceName, r.NetworkCidr))
	}

	scans, next := trim(scans, p.Limit, func(sc *Scan) Cursor {
		return Cursor{Value: sc.StartedAt, ID: sc.ID, Order: cursor.Desc}
	})

	return &ScanPage{Scans: scans, Next: next}, nil
}

// LatestScan returns the most recent scan, or ErrNotFound when none has run.
func (s *Store) LatestScan(ctx context.Context) (*Scan, error) {
	page, err := s.ListScans(ctx, Page{Limit: 1})
	if err != nil {
		return nil, err
	}

	if len(page.Scans) == 0 {
		return nil, fmt.Errorf("scan: %w", ErrNotFound)
	}

	return page.Scans[0], nil
}

// Stats counts the inventory as a whole.
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	now := s.now()

	row, err := s.q.DeviceStats(ctx, models.DeviceStatsParams{
		OnlineSince: dbtype.NewTime(now.Add(-s.onlineWindow)),
		NewSince:    dbtype.NewTime(now.Add(-DiscoveryWindow)),
	})
	if err != nil {
		return nil, fmt.Errorf("device stats: %w", err)
	}

	return &Stats{
		Total:      int(row.Total),
		Online:     int(row.Online),
		Offline:    int(row.Total - row.Online),
		Ignored:    int(row.Ignored),
		Discovered: int(row.Discovered),
	}, nil
}

// Groups returns the group names the user has assigned, in order.
func (s *Store) Groups(ctx context.Context) ([]string, error) {
	groups, err := s.q.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}

	return groups, nil
}

func (s *Store) LastSuccessfulDeviceScan(ctx context.Context) (time.Time, error) {
	t, err := s.q.LastSuccessfulDeviceScanTimestamp(ctx)
	if err != nil {
		return time.Time{}, err
	}

	if t.Valid {
		return time.Time{}, errors.New("unexpected null time value")
	}

	return t.Time.Time, nil
}

// onlineCutoff is the instant a device must have been seen at or after to count
// as online. It is read once per query so a list judges every device alike.
func (s *Store) onlineCutoff() time.Time {
	return s.now().Add(-s.onlineWindow)
}
