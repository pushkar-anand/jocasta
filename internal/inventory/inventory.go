// Package inventory records what a scan found. Each result is folded into the
// device it belongs to -- identified by hardware address where one is known,
// and by the address it answered on where one is not -- and anything that
// changed is written to the event log.
package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// How a device came to be identified, mirroring the CHECK on the column.
const (
	identityMAC = "mac"
	identityIP  = "ip"
)

// Event kinds. They live here rather than in a schema constraint so adding one
// is a Go change instead of a migration.
const (
	eventDiscovered      = "device_discovered"
	eventIdentified      = "device_identified"
	eventMerged          = "devices_merged"
	eventAddressAdded    = "address_added"
	eventHostnameChanged = "hostname_changed"
)

// hostnameSourceDNS names where a swept hostname came from. A sweep resolves
// names one way; other sources bring their own and have to be told apart.
const hostnameSourceDNS = "dns"

// Store writes scan results into the inventory.
type Store struct {
	conn *sql.DB
	q    *models.Queries
	log  *slog.Logger

	// now is a field so tests can pin the timestamps they assert on.
	now func() time.Time
}

func New(conn *sql.DB, log *slog.Logger) *Store {
	return &Store{conn: conn, q: models.New(conn), log: log, now: time.Now}
}

// Result counts what a single sweep changed.
type Result struct {
	ScanID     int64
	Discovered int
	Identified int
	Merged     int
	Seen       int
}

// pass carries what every write in one ingest shares, including the single
// timestamp they are all stamped with.
type pass struct {
	q         *models.Queries
	scanID    int64
	networkID int64
	at        dbtype.Time
	res       Result
}

// RecordSweep stores the results of one sweep of prefix, attributing them to
// the named source.
func (s *Store) RecordSweep(ctx context.Context, source string, prefix netip.Prefix, hosts []scanner.Host) (Result, error) {
	scanID, networkID, err := s.open(ctx, source, prefix)
	if err != nil {
		return Result{}, err
	}

	res, ingestErr := s.ingest(ctx, scanID, networkID, hosts)
	res.ScanID = scanID

	// The scan row is closed in its own transaction so a failure is recorded
	// rather than rolled back along with the work it was describing.
	if err := s.close(ctx, scanID, res.Seen, ingestErr); err != nil {
		return res, errors.Join(ingestErr, err)
	}

	return res, ingestErr
}

// open registers the source and network and opens a running scan row.
func (s *Store) open(ctx context.Context, source string, prefix netip.Prefix) (scanID, networkID int64, err error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin scan: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	at := s.stamp()

	src, err := q.UpsertSource(ctx, models.UpsertSourceParams{Kind: "sweep", Name: source, CreatedAt: at})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert source %q: %w", source, err)
	}

	nw, err := q.UpsertNetwork(ctx, models.UpsertNetworkParams{Cidr: dbtype.NewPrefix(prefix), CreatedAt: at})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert network %s: %w", prefix, err)
	}

	sc, err := q.CreateScan(ctx, models.CreateScanParams{
		SourceID:  src.ID,
		Kind:      "discovery",
		NetworkID: sql.NullInt64{Int64: nw.ID, Valid: true},
		StartedAt: at,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("create scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit scan: %w", err)
	}

	return sc.ID, nw.ID, nil
}

// ingest applies every result as one transaction: a sweep either lands whole
// or not at all, so a partial run cannot leave a device holding an address it
// was about to lose.
func (s *Store) ingest(ctx context.Context, scanID, networkID int64, hosts []scanner.Host) (Result, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin ingest: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	// One timestamp for the whole sweep. Every row it writes describes the
	// same observation, and taking the clock per row would stamp a device as
	// last seen before it was first seen.
	p := &pass{
		q:         s.q.WithTx(tx),
		scanID:    scanID,
		networkID: networkID,
		at:        s.stamp(),
	}

	for _, h := range hosts {
		if err := s.record(ctx, p, h); err != nil {
			return Result{}, fmt.Errorf("record %s: %w", h.Addr, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit ingest: %w", err)
	}

	return p.res, nil
}

// close marks the scan finished, carrying the ingest failure if there was one.
func (s *Store) close(ctx context.Context, scanID int64, found int, cause error) error {
	p := models.FinishScanParams{
		Status:     "ok",
		FoundCount: int64(found),
		FinishedAt: dbtype.NullTime{Time: s.stamp(), Valid: true},
		ID:         scanID,
	}

	if cause != nil {
		p.Status = "failed"
		p.Error = nullString(cause.Error())
	}

	if err := s.q.FinishScan(ctx, p); err != nil {
		return fmt.Errorf("finish scan %d: %w", scanID, err)
	}

	return nil
}

func (s *Store) record(ctx context.Context, p *pass, h scanner.Host) error {
	ip := dbtype.NewAddr(h.Addr)

	// A sweep reports whatever the neighbour table held for the address, and
	// anything that is not a 6-byte hardware address identifies nothing, so the
	// device stays known by the address it answered on.
	var mac dbtype.MAC

	if h.MAC != "" {
		parsed, err := dbtype.ParseMAC(h.MAC)
		if err != nil {
			s.log.DebugContext(ctx, "ignoring unusable hardware address", "addr", h.Addr, "mac", h.MAC, "err", err)
		} else {
			mac = parsed
		}
	}

	holder, hasHolder, err := currentHolder(ctx, p.q, ip)
	if err != nil {
		return err
	}

	target, err := s.resolve(ctx, p, mac, h, holder, hasHolder)
	if err != nil {
		return err
	}

	// A row that only ever stood for this address is the same device under a
	// weaker name once a hardware address claims it.
	if hasHolder && holder.ID != target.ID && holder.IdentitySource == identityIP {
		if err := s.fold(ctx, p, holder, target); err != nil {
			return err
		}

		p.res.Merged++
	}

	if err := s.claim(ctx, p, target.ID, ip); err != nil {
		return err
	}

	if err := s.applyHostname(ctx, p, target, h.Hostname); err != nil {
		return err
	}

	if err := p.q.TouchDevice(ctx, models.TouchDeviceParams{LastSeen: p.at, ID: target.ID}); err != nil {
		return fmt.Errorf("touch device %d: %w", target.ID, err)
	}

	p.res.Seen++

	return nil
}

// resolve finds the device a result belongs to, creating or identifying one
// where none is known yet.
func (s *Store) resolve(
	ctx context.Context,
	p *pass,
	mac dbtype.MAC,
	h scanner.Host,
	holder models.Device,
	hasHolder bool,
) (models.Device, error) {
	if !mac.Valid() {
		if hasHolder {
			return holder, nil
		}

		return s.create(ctx, p, mac, h)
	}

	d, err := p.q.GetDeviceByMAC(ctx, mac)

	switch {
	case err == nil:
		return d, nil
	case !errors.Is(err, sql.ErrNoRows):
		return models.Device{}, fmt.Errorf("device by mac %s: %w", mac, err)
	}

	// The address answered before its hardware was visible, so the row already
	// standing for it becomes the identified device rather than a second one.
	if hasHolder && holder.IdentitySource == identityIP {
		params := models.IdentifyDeviceParams{
			Mac:          mac,
			IsRandomised: h.Randomised,
			Vendor:       nullString(h.Vendor),
			ID:           holder.ID,
		}

		if err := p.q.IdentifyDevice(ctx, params); err != nil {
			return models.Device{}, fmt.Errorf("identify device %d: %w", holder.ID, err)
		}

		if err := s.event(ctx, p, holder.ID, eventIdentified, "", mac.String(), ""); err != nil {
			return models.Device{}, err
		}

		holder.Mac = params.Mac
		holder.IdentitySource = identityMAC
		holder.IsRandomised = params.IsRandomised
		holder.Vendor = params.Vendor
		p.res.Identified++

		return holder, nil
	}

	return s.create(ctx, p, mac, h)
}

func (s *Store) create(ctx context.Context, p *pass, mac dbtype.MAC, h scanner.Host) (models.Device, error) {
	source := identityIP
	if mac.Valid() {
		source = identityMAC
	}

	hostnameSource := ""
	if h.Hostname != "" {
		hostnameSource = hostnameSourceDNS
	}

	d, err := p.q.CreateDevice(ctx, models.CreateDeviceParams{
		Mac:            mac,
		IdentitySource: source,
		IsRandomised:   h.Randomised,
		Vendor:         nullString(h.Vendor),
		Hostname:       nullString(h.Hostname),
		HostnameSource: nullString(hostnameSource),
		FirstSeen:      p.at,
		LastSeen:       p.at,
	})
	if err != nil {
		return models.Device{}, fmt.Errorf("create device for %s: %w", h.Addr, err)
	}

	if err := s.event(ctx, p, d.ID, eventDiscovered, "", h.Addr.String(), ""); err != nil {
		return models.Device{}, err
	}

	p.res.Discovered++

	return d, nil
}

// fold merges a device that was only ever known by its address into the one
// its hardware address identifies. Curation the user applied to the weaker row
// is carried over, since they had no way to know it was a duplicate.
func (s *Store) fold(ctx context.Context, p *pass, ghost, into models.Device) error {
	err := p.q.AdoptCuration(ctx, models.AdoptCurationParams{
		FoldedLabel:     ghost.Label,
		FoldedNotes:     ghost.Notes,
		FoldedGroupName: ghost.GroupName,
		FirstSeen:       earlier(into.FirstSeen, ghost.FirstSeen),
		ID:              into.ID,
	})
	if err != nil {
		return fmt.Errorf("adopt curation of device %d: %w", ghost.ID, err)
	}

	if err := p.q.MoveAddresses(ctx, models.MoveAddressesParams{IntoID: into.ID, FromID: ghost.ID}); err != nil {
		return fmt.Errorf("move addresses of device %d: %w", ghost.ID, err)
	}

	err = p.q.MoveEvents(ctx, models.MoveEventsParams{
		IntoID: sql.NullInt64{Int64: into.ID, Valid: true},
		FromID: sql.NullInt64{Int64: ghost.ID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("move events of device %d: %w", ghost.ID, err)
	}

	s.log.InfoContext(ctx, "folded device into its identified twin", "from", ghost.ID, "into", into.ID)

	detail := fmt.Sprintf("device %d folded into %d", ghost.ID, into.ID)
	if err := s.event(ctx, p, into.ID, eventMerged, "", "", detail); err != nil {
		return err
	}

	if err := p.q.DeleteDevice(ctx, ghost.ID); err != nil {
		return fmt.Errorf("delete folded device %d: %w", ghost.ID, err)
	}

	return nil
}

// claim makes the address current for the device, taking it off whoever else
// was holding it.
func (s *Store) claim(ctx context.Context, p *pass, deviceID int64, ip dbtype.Addr) error {
	if err := p.q.ReleaseAddress(ctx, models.ReleaseAddressParams{Ip: ip, DeviceID: deviceID}); err != nil {
		return fmt.Errorf("release %s: %w", ip, err)
	}

	a, err := p.q.GetAddress(ctx, models.GetAddressParams{DeviceID: deviceID, Ip: ip})

	switch {
	case errors.Is(err, sql.ErrNoRows):
		params := models.InsertAddressParams{
			DeviceID:  deviceID,
			NetworkID: sql.NullInt64{Int64: p.networkID, Valid: true},
			Ip:        ip,
			FirstSeen: p.at,
			LastSeen:  p.at,
		}
		if _, err := p.q.InsertAddress(ctx, params); err != nil {
			return fmt.Errorf("insert %s: %w", ip, err)
		}

		return s.event(ctx, p, deviceID, eventAddressAdded, "", ip.String(), "")
	case err != nil:
		return fmt.Errorf("address %s of device %d: %w", ip, deviceID, err)
	}

	err = p.q.RefreshAddress(ctx, models.RefreshAddressParams{
		NetworkID: sql.NullInt64{Int64: p.networkID, Valid: true},
		LastSeen:  p.at,
		ID:        a.ID,
	})
	if err != nil {
		return fmt.Errorf("refresh %s: %w", ip, err)
	}

	return nil
}

func (s *Store) applyHostname(ctx context.Context, p *pass, d models.Device, hostname string) error {
	if hostname == "" || hostname == d.Hostname.String {
		return nil
	}

	params := models.SetDeviceHostnameParams{
		Hostname:       nullString(hostname),
		HostnameSource: nullString(hostnameSourceDNS),
		ID:             d.ID,
	}
	if err := p.q.SetDeviceHostname(ctx, params); err != nil {
		return fmt.Errorf("set hostname of device %d: %w", d.ID, err)
	}

	// A first name is part of discovering the device, not a change to it.
	if !d.Hostname.Valid {
		return nil
	}

	return s.event(ctx, p, d.ID, eventHostnameChanged, d.Hostname.String, hostname, "")
}

func (s *Store) event(ctx context.Context, p *pass, deviceID int64, kind, from, to, detail string) error {
	err := p.q.CreateEvent(ctx, models.CreateEventParams{
		DeviceID:   sql.NullInt64{Int64: deviceID, Valid: true},
		ScanID:     sql.NullInt64{Int64: p.scanID, Valid: true},
		Kind:       kind,
		OldValue:   nullString(from),
		NewValue:   nullString(to),
		Detail:     nullString(detail),
		OccurredAt: p.at,
	})
	if err != nil {
		return fmt.Errorf("record %s event: %w", kind, err)
	}

	return nil
}

// stamp reads the clock as the schema stores it.
func (s *Store) stamp() dbtype.Time {
	return dbtype.NewTime(s.now())
}

// currentHolder returns the device holding ip right now, if any.
func currentHolder(ctx context.Context, q *models.Queries, ip dbtype.Addr) (models.Device, bool, error) {
	row, err := q.GetDeviceByCurrentIP(ctx, ip)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return models.Device{}, false, nil
	case err != nil:
		return models.Device{}, false, fmt.Errorf("device holding %s: %w", ip, err)
	}

	return row.Device, true, nil
}

// earlier returns whichever of the two timestamps came first. A folded device
// was seen from the moment either of its rows was.
func earlier(a, b dbtype.Time) dbtype.Time {
	if b.Before(a.Time) {
		return b
	}

	return a
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
