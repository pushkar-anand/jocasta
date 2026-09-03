package inventory

import (
	"cmp"
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
// hold. Callers match on it rather than on [sql.ErrNoRows], which would tie
// every surface to the storage layer.
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
		NetworkID:      sql.NullInt64{Int64: f.Network, Valid: f.Network != 0},
		Q:              nullString(f.Query),
	})
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}

	// The prefixes each device's current addresses sit on, so a row can name
	// where the device lives. The table is a handful of rows, so it is read
	// whole and indexed rather than joined -- the same choice Device makes.
	networks, err := s.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	byID := make(map[int64]*Network, len(networks))
	for _, n := range networks {
		byID[n.ID] = n
	}

	cutoff := s.onlineCutoff()
	devices := make([]*Device, 0, len(rows))

	for _, r := range rows {
		d := newDevice(&r.Device, cutoff)
		d.Current = parseAddrs(r.CurrentIps)
		d.OpenPorts = parsePorts(r.OpenPorts)
		d.Networks = resolveNetworks(parseIDs(r.NetworkIds), byID)

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

// resolveNetworks turns a device's network ids into the prefixes they name, in
// CIDR order, dropping an id the networks list no longer holds.
func resolveNetworks(ids []int64, byID map[int64]*Network) []*Network {
	if len(ids) == 0 {
		return nil
	}

	out := make([]*Network, 0, len(ids))
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
		}
	}

	slices.SortFunc(out, func(a, b *Network) int { return cmp.Compare(a.CIDR, b.CIDR) })

	return out
}

// Device returns one device with its full address history.
func (s *Store) Device(ctx context.Context, id int64) (*Device, error) {
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

	// The prefix each address sits on, so the page can name it. The table is a
	// handful of rows, so it is read whole and indexed rather than joined.
	networks, err := s.ListNetworks(ctx)
	if err != nil {
		return nil, fmt.Errorf("networks for device %d: %w", id, err)
	}

	byID := make(map[int64]*Network, len(networks))
	for _, n := range networks {
		byID[n.ID] = n
	}

	d.Addresses = make([]*Address, 0, len(addrs))

	for _, a := range addrs {
		addr := newAddress(a)
		if a.NetworkID.Valid {
			addr.Network = byID[a.NetworkID.Int64]
		}

		d.Addresses = append(d.Addresses, addr)

		if a.IsCurrent {
			d.Current = append(d.Current, a.IP.Addr)
		}
	}

	slices.SortFunc(d.Current, netip.Addr.Compare)

	ports, err := s.q.ListDevicePorts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("ports of device %d: %w", id, err)
	}

	d.Ports = make([]*Port, 0, len(ports))
	for _, p := range ports {
		d.Ports = append(d.Ports, newPort(p))
	}

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

// DeviceSources returns what every source claims about one device, the most
// recently heard from first.
func (s *Store) DeviceSources(ctx context.Context, id int64) ([]*Claim, error) {
	rows, err := s.q.ListDeviceSources(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("claims about device %d: %w", id, err)
	}

	claims := make([]*Claim, 0, len(rows))
	for _, r := range rows {
		claims = append(claims, newClaim(r))
	}

	return claims, nil
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

// LastSuccessfulScanAt reports when a scan of kind k last finished with
// something to show for it, and ErrNotFound when none ever has.
//
// It answers "is this due?" for anything that runs on a schedule, so it is the
// finish rather than the start: a poller waits an interval after the work ends,
// and measuring from the start would make the wait mean something else across a
// restart than it does between runs. A scan that failed, or that was cut off
// before it finished, gathered nothing and so does not answer -- which is what
// makes an interrupted or failed sweep due again rather than credited as done.
func (s *Store) LastSuccessfulScanAt(ctx context.Context, k dbtype.ScanKind) (time.Time, error) {
	at, err := s.q.LatestSuccessfulScanFinishedAt(ctx, k)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, fmt.Errorf("scan of kind %s: %w", k, ErrNotFound)
	case err != nil:
		return time.Time{}, fmt.Errorf("latest %s scan: %w", k, err)
	}

	// The query selects finished scans only, so this holds unless the column
	// override and the WHERE ever disagree. Kept because the failure it guards
	// against -- a zero time read as a real one -- is silent.
	if !at.Valid {
		return time.Time{}, fmt.Errorf("scan of kind %s: %w", k, ErrNotFound)
	}

	return at.Time.Time, nil
}

// PortScanTargets returns every address a port scan should probe: the current
// address of every device the user has not ignored. The scan works from what
// discovery has already found rather than sweeping, so this is its whole target
// list.
func (s *Store) PortScanTargets(ctx context.Context) ([]netip.Addr, error) {
	rows, err := s.q.AllCurrentAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("port scan targets: %w", err)
	}

	addrs := make([]netip.Addr, 0, len(rows))
	for _, r := range rows {
		addrs = append(addrs, r.IP.Addr)
	}

	return addrs, nil
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

// ListNetworks returns every recorded network with how many devices are on it
// now. The overview leads with these, so a network the sweeps have never found
// anything on is included at zero rather than dropped.
func (s *Store) ListNetworks(ctx context.Context) ([]*Network, error) {
	rows, err := s.q.ListNetworks(ctx, dbtype.NewTime(s.onlineCutoff()))
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}

	networks := make([]*Network, 0, len(rows))

	for _, row := range rows {
		networks = append(networks, &Network{
			ID:   row.ID,
			CIDR: row.Cidr.String(),
			Name: row.Name.String,
			VLAN: int(row.VlanID.Int64),

			Total:   int(row.Total),
			Online:  int(row.Online),
			Offline: int(row.Total - row.Online),
		})
	}

	return networks, nil
}

// Network returns one recorded network with its device counts, or ErrNotFound
// when no network has that id.
//
// It filters the full list rather than asking for one row: the table is a
// handful of prefixes, and the counts are the same aggregate ListNetworks
// already computes.
func (s *Store) Network(ctx context.Context, id int64) (*Network, error) {
	networks, err := s.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	for _, n := range networks {
		if n.ID == id {
			return n, nil
		}
	}

	return nil, fmt.Errorf("network %d: %w", id, ErrNotFound)
}

// Groups returns the group names the user has assigned, in order.
func (s *Store) Groups(ctx context.Context) ([]string, error) {
	groups, err := s.q.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}

	return groups, nil
}

// onlineCutoff is the instant a device must have been seen at or after to count
// as online. It is read once per query so a list judges every device alike.
func (s *Store) onlineCutoff() time.Time {
	return s.now().Add(-s.onlineWindow)
}
