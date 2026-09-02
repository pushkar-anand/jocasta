// Package inventory processes network scan results, merging data into corresponding
// device records identified primarily by hardware address (or by the responding
// address if unknown). Any detected changes are written to the event log.
package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// DefaultOnlineWindow is how recently a device must have answered to count as
// online when no window is configured.
const DefaultOnlineWindow = 15 * time.Minute

// Store reads the inventory and writes scan results into it.
type Store struct {
	conn *sql.DB
	q    *models.Queries
	log  *slog.Logger

	// now is a field so tests can pin the timestamps they assert on.
	now func() time.Time

	// onlineWindow is how long after a device was last seen it still counts as
	// online. Nothing reports a device leaving, so presence is a question about
	// how stale the last sighting is, and how stale is too stale depends on how
	// often the sweeps run.
	onlineWindow time.Duration
}

// Option configures a Store.
type Option func(*Store)

// WithOnlineWindow sets how recently a device must have been seen to count as
// online. A window of zero or less leaves the default in place.
func WithOnlineWindow(d time.Duration) Option {
	return func(s *Store) {
		if d > 0 {
			s.onlineWindow = d
		}
	}
}

// WithClock replaces the clock the store stamps writes and judges presence by.
func WithClock(now func() time.Time) Option {
	return func(s *Store) {
		if now != nil {
			s.now = now
		}
	}
}

// New returns a store over conn that can be configured through opts.
func New(conn *sql.DB, log *slog.Logger, opts ...Option) *Store {
	s := &Store{
		conn:         conn,
		q:            models.New(conn),
		log:          log,
		now:          time.Now,
		onlineWindow: DefaultOnlineWindow,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Result counts what a single reading changed. Seen counts the facts that were
// recorded rather than the addresses that answered: for a sweep the two are the
// same, since everything a sweep returns answered a probe.
type Result struct {
	ScanID     int64
	Discovered int
	Identified int
	Merged     int
	Seen       int

	// Dropped counts facts there was nothing to record against: no hardware
	// address and no address any device holds, so believing them would invent a
	// device with nothing to tell it apart.
	Dropped int
}

// reading is what one source read: who read it, what they claim, and the
// network the reading covered when it covered exactly one.
type reading struct {
	source string
	kind   dbtype.SourceKind

	// network is the prefix a sweep covered. A source that reads every network
	// at once bounds its reading by none, and leaves this nil.
	network *netip.Prefix

	facts []plugin.Fact
}

// pass carries what every write in one ingest shares, including the single
// timestamp they are all stamped with.
type pass struct {
	q      *models.Queries
	scanID int64

	// sourceID files every claim this reading writes under the source that made
	// it.
	sourceID int64

	networks networks
	at       dbtype.Time
	res      Result
}

// networks matches an address to the recorded network containing it. A sweep
// knows the prefix it swept, but a source that reads every VLAN at once knows
// only addresses, so the network is looked up per address rather than carried
// for the whole reading.
type networks []recordedNetwork

type recordedNetwork struct {
	id     int64
	prefix netip.Prefix
}

// find returns the network holding addr, and false when no recorded network
// does. The most specific match wins: a supernet and a subnet of it can both be
// recorded, and the narrower one is the better answer.
func (ns networks) find(addr netip.Addr) (int64, bool) {
	var (
		id   int64
		bits = -1
	)

	for _, n := range ns {
		if n.prefix.Contains(addr) && n.prefix.Bits() > bits {
			id, bits = n.id, n.prefix.Bits()
		}
	}

	return id, bits >= 0
}

// networkID renders what find returns as the nullable column it is written to.
func (ns networks) networkID(addr netip.Addr) sql.NullInt64 {
	id, ok := ns.find(addr)

	return sql.NullInt64{Int64: id, Valid: ok}
}

func loadNetworks(ctx context.Context, q *models.Queries) (networks, error) {
	rows, err := q.AllNetworks(ctx)
	if err != nil {
		return nil, fmt.Errorf("load networks: %w", err)
	}

	ns := make(networks, 0, len(rows))
	for _, r := range rows {
		ns = append(ns, recordedNetwork{id: r.ID, prefix: r.Cidr.Prefix})
	}

	return ns, nil
}

// RecordSweep stores the results of one sweep of prefix, attributing them to
// the named source.
//
// A sweep is one source among several. What makes it particular is only that
// everything it returns answered a probe, and that any name it carries came
// from the reverse lookup it performed -- so it states those two things and
// hands the facts to the same path every source uses.
func (s *Store) RecordSweep(ctx context.Context, source string, prefix netip.Prefix, hosts []scanner.Host) (*Result, error) {
	return s.report(ctx, reading{
		source:  source,
		kind:    dbtype.SourceSweep,
		network: &prefix,
		facts:   sweptFacts(hosts),
	})
}

// sweptFacts says what a sweep result claims: the address answered, so the
// device is here now, and a name it carries was resolved over DNS.
func sweptFacts(hosts []scanner.Host) []plugin.Fact {
	facts := make([]plugin.Fact, len(hosts))

	for i, h := range hosts {
		f := plugin.Fact{Host: h.Host, Present: true, SeenAt: h.SeenAt}

		// The standing travels with the name: a fact carrying a source for a
		// name it does not have is a standing for nothing.
		if h.Hostname() != "" {
			f.HostnameSource = dbtype.HostnameFromDNS
		}

		facts[i] = f
	}

	return facts
}

// RecordFacts stores what one source claims about the devices it knows of.
//
// A source is asked what it knows rather than probing a prefix, so the reading
// is bounded by no network and each address is matched to the network
// containing it. Its facts need not assert presence: a lease names a device
// that is configured rather than answering.
//
// The kind is a parameter here and fixed inside RecordSweep, so a sweep's
// callers cannot pass the wrong one.
func (s *Store) RecordFacts(
	ctx context.Context,
	source string,
	kind dbtype.SourceKind,
	facts []plugin.Fact,
) (*Result, error) {
	return s.report(ctx, reading{source: source, kind: kind, facts: facts})
}

// report records one source's reading: it opens a scan row, ingests the facts
// as one transaction, and closes the scan with whatever happened.
func (s *Store) report(ctx context.Context, r reading) (*Result, error) {
	scanID, sourceID, err := s.open(ctx, r)
	if err != nil {
		return nil, err
	}

	res, ingestErr := s.ingest(ctx, scanID, sourceID, r.facts)

	// res is nil unless the ingest committed, so the count a failed scan
	// records is the only one true of the table: none.
	found := 0
	if ingestErr == nil {
		found = res.Seen
	}

	// The scan row is closed in its own transaction, so a failure is recorded
	// rather than rolled back along with the work it was describing.
	closeErr := s.close(ctx, scanID, found, ingestErr)

	// The scan id goes in the error rather than in a half-filled Result, so a
	// caller never has to know which fields survive a failure.
	if err := errors.Join(ingestErr, closeErr); err != nil {
		return nil, fmt.Errorf("scan %d: %w", scanID, err)
	}

	res.ScanID = scanID

	return res, nil
}

// open registers the source and opens a running scan row, recording the network
// when the reading covered exactly one.
func (s *Store) open(ctx context.Context, r reading) (scanID, sourceID int64, err error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin scan: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	at := s.stamp()

	src, err := q.UpsertSource(ctx, models.UpsertSourceParams{Kind: r.kind, Name: r.source, CreatedAt: at})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert source %q: %w", r.source, err)
	}

	// Recording the network here is also what lets the addresses in this
	// reading be matched to it: a prefix nothing has swept before is not in the
	// table until the scan that covers it opens.
	var networkID sql.NullInt64

	if r.network != nil {
		nw, err := q.UpsertNetwork(ctx, models.UpsertNetworkParams{Cidr: dbtype.NewPrefix(*r.network), CreatedAt: at})
		if err != nil {
			return 0, 0, fmt.Errorf("upsert network %s: %w", r.network, err)
		}

		networkID = sql.NullInt64{Int64: nw.ID, Valid: true}
	}

	sc, err := q.CreateScan(ctx, models.CreateScanParams{
		SourceID:  src.ID,
		Kind:      dbtype.ScanDiscovery,
		NetworkID: networkID,
		StartedAt: at,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("create scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit scan: %w", err)
	}

	return sc.ID, src.ID, nil
}

// ingest applies every fact as one transaction: a reading either lands whole
// or not at all, so a partial run cannot leave a device holding an address it
// was about to lose.
func (s *Store) ingest(ctx context.Context, scanID, sourceID int64, facts []plugin.Fact) (*Result, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin ingest: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	nets, err := loadNetworks(ctx, q)
	if err != nil {
		return nil, err
	}

	// One timestamp for the whole reading. Every row it writes describes the
	// same observation, and taking the clock per row would stamp a device as
	// last seen before it was first seen.
	p := &pass{
		q:        q,
		scanID:   scanID,
		sourceID: sourceID,
		networks: nets,
		at:       s.stamp(),
	}

	for _, f := range facts {
		if err := s.record(ctx, p, f); err != nil {
			return nil, fmt.Errorf("record %s: %w", f.Host.Address(), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit ingest: %w", err)
	}

	return &p.res, nil
}

// close marks the scan finished, carrying the ingest failure if there was one.
func (s *Store) close(ctx context.Context, scanID int64, found int, cause error) error {
	p := models.FinishScanParams{
		Status:     dbtype.StatusOK,
		FoundCount: int64(found),
		FinishedAt: dbtype.NullTime{Time: s.stamp(), Valid: true},
		ID:         scanID,
	}

	if cause != nil {
		p.Status = dbtype.StatusFailed
		p.Error = nullString(cause.Error())
	}

	if err := s.q.FinishScan(ctx, p); err != nil {
		return fmt.Errorf("finish scan %d: %w", scanID, err)
	}

	return nil
}

func (s *Store) record(ctx context.Context, p *pass, f plugin.Fact) error {
	ip := dbtype.NewAddr(f.Host.Address())
	mac := s.hardware(ctx, f)

	// A fact that does not assert presence says nothing about who holds the
	// address now, so it never reaches for the current holder. A static lease
	// for something unplugged must not identify, fold or take an address from
	// whatever is answering on that address today.
	var (
		holder *models.Device
		err    error
	)

	if f.Present {
		holder, err = currentHolder(ctx, p.q, ip)
		if err != nil {
			return err
		}
	}

	target, err := s.resolve(ctx, p, mac, f, holder)
	if err != nil {
		return err
	}

	if target == nil {
		s.log.DebugContext(ctx, "dropping a fact that identifies nothing",
			"addr", f.Host.Address(), "mac", f.Host.MAC, "present", f.Present)

		p.res.Dropped++

		return nil
	}

	// A row that only ever stood for this address is the same device under a
	// weaker name once a hardware address claims it.
	if holder != nil && holder.ID != target.ID && holder.IdentitySource == dbtype.IdentityIP {
		if err := s.fold(ctx, p, holder, target); err != nil {
			return err
		}

		p.res.Merged++
	}

	// Holding an address and having been seen are both presence claims, and a
	// source that is not making one may still say what a device is called.
	if f.Present {
		if err := s.claim(ctx, p, target.ID, ip); err != nil {
			return err
		}
	}

	if err := s.applyClaim(ctx, p, target, f); err != nil {
		return err
	}

	if f.Present {
		if err := p.q.TouchDevice(ctx, models.TouchDeviceParams{LastSeen: p.at, ID: target.ID}); err != nil {
			return fmt.Errorf("touch device %d: %w", target.ID, err)
		}
	}

	p.res.Seen++

	return nil
}

// hardware parses the hardware address a fact carries. Anything that is not a
// 6-byte address identifies nothing, so the fact keeps everything else it says
// and the device stays known by the address it answered on.
func (s *Store) hardware(ctx context.Context, f plugin.Fact) dbtype.MAC {
	if f.Host.MAC == "" {
		return dbtype.MAC{}
	}

	mac, err := dbtype.ParseMAC(f.Host.MAC)
	if err != nil {
		s.log.DebugContext(ctx, "ignoring unusable hardware address",
			"addr", f.Host.Address(), "mac", f.Host.MAC, "err", err)

		return dbtype.MAC{}
	}

	return mac
}

// resolve finds the device a fact belongs to, creating or identifying one where
// none is known yet. A nil holder means nothing currently claims the address,
// which is a state to act on rather than one to report.
//
// It returns a nil device and no error for a fact there is nothing to record
// against. That is a fact which neither identifies a device nor says the
// address is answering: an incomplete neighbour entry names nothing, and a
// source reporting a device it has not seen may enrich a device already known
// but may not conjure one. Until claims have a table of their own, "configured
// but never seen" has nowhere to live that does not put a device nothing has
// ever met into the inventory, counted among the online.
func (s *Store) resolve(
	ctx context.Context,
	p *pass,
	mac dbtype.MAC,
	f plugin.Fact,
	holder *models.Device,
) (*models.Device, error) {
	if !mac.Valid() {
		if holder != nil {
			return holder, nil
		}

		if !f.Present {
			return nil, nil
		}

		return s.create(ctx, p, mac, f)
	}

	d, err := p.q.GetDeviceByMAC(ctx, mac)

	switch {
	case err == nil:
		return d, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("device by mac %s: %w", mac, err)
	}

	// The address answered before its hardware was visible, so the row already
	// standing for it becomes the identified device rather than a second one.
	if holder != nil && holder.IdentitySource == dbtype.IdentityIP {
		params := models.IdentifyDeviceParams{
			MAC:          mac,
			IsRandomised: f.Host.Randomised(),
			Vendor:       nullString(f.Host.ShortName()),
			ID:           holder.ID,
		}

		if err := p.q.IdentifyDevice(ctx, params); err != nil {
			return nil, fmt.Errorf("identify device %d: %w", holder.ID, err)
		}

		if err := s.event(ctx, p, holder.ID, dbtype.EventDeviceIdentified, "", mac.String(), ""); err != nil {
			return nil, err
		}

		holder.MAC = params.MAC
		holder.IdentitySource = dbtype.IdentityMAC
		holder.IsRandomised = params.IsRandomised
		holder.Vendor = params.Vendor
		p.res.Identified++

		return holder, nil
	}

	if !f.Present {
		return nil, nil
	}

	return s.create(ctx, p, mac, f)
}

func (s *Store) create(ctx context.Context, p *pass, mac dbtype.MAC, f plugin.Fact) (*models.Device, error) {
	source := dbtype.IdentityIP
	if mac.Valid() {
		source = dbtype.IdentityMAC
	}

	d, err := p.q.CreateDevice(ctx, models.CreateDeviceParams{
		MAC:            mac,
		IdentitySource: source,
		IsRandomised:   f.Host.Randomised(),
		Vendor:         nullString(f.Host.ShortName()),
		Hostname:       nullString(f.Host.Hostname()),
		HostnameSource: f.HostnameSource,
		FirstSeen:      p.at,
		LastSeen:       p.at,
	})
	if err != nil {
		return nil, fmt.Errorf("create device for %s: %w", f.Host.Address(), err)
	}

	if err := s.event(ctx, p, d.ID, dbtype.EventDeviceDiscovered, "", f.Host.Address().String(), ""); err != nil {
		return nil, err
	}

	p.res.Discovered++

	return d, nil
}

// fold merges a device only ever known by its address into the one
// its hardware address identifies. Curation the user applied to the weaker row
// is carried over, since they had no way to know it was a duplicate.
func (s *Store) fold(ctx context.Context, p *pass, ghost, into *models.Device) error {
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

	// A source that filed against both rows would otherwise lose its claim to
	// the CASCADE when the ghost is deleted.
	err = p.q.MoveDeviceSources(ctx, models.MoveDeviceSourcesParams{IntoID: into.ID, FromID: ghost.ID})
	if err != nil {
		return fmt.Errorf("move claims about device %d: %w", ghost.ID, err)
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
	if err := s.event(ctx, p, into.ID, dbtype.EventDevicesMerged, "", "", detail); err != nil {
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
	if err := p.q.ReleaseAddress(ctx, models.ReleaseAddressParams{IP: ip, DeviceID: deviceID}); err != nil {
		return fmt.Errorf("release %s: %w", ip, err)
	}

	a, err := p.q.GetAddress(ctx, models.GetAddressParams{DeviceID: deviceID, IP: ip})

	switch {
	case errors.Is(err, sql.ErrNoRows):
		params := models.InsertAddressParams{
			DeviceID:  deviceID,
			NetworkID: p.networks.networkID(ip.Addr),
			IP:        ip,
			FirstSeen: p.at,
			LastSeen:  p.at,
		}
		if _, err := p.q.InsertAddress(ctx, params); err != nil {
			return fmt.Errorf("insert %s: %w", ip, err)
		}

		return s.event(ctx, p, deviceID, dbtype.EventAddressAdded, "", ip.String(), "")
	case err != nil:
		return fmt.Errorf("address %s of device %d: %w", ip, deviceID, err)
	}

	err = p.q.RefreshAddress(ctx, models.RefreshAddressParams{
		NetworkID: p.networks.networkID(ip.Addr),
		LastSeen:  p.at,
		ID:        a.ID,
	})
	if err != nil {
		return fmt.Errorf("refresh %s: %w", ip, err)
	}

	return nil
}

// applyClaim files this source's reading against the device, then re-elects the
// name the device row carries from every source's claim.
//
// The row holds one name because the device list shows one per row and searches
// one column; the claims behind it are kept so the device page can show a source
// that was outranked.
//
// Re-deriving from every claim on each pass is what makes retraction work: a
// source that stops reporting a name writes an empty claim, and the runner-up
// wins the next pass that touches the device.
func (s *Store) applyClaim(ctx context.Context, p *pass, d *models.Device, f plugin.Fact) error {
	if err := s.recordClaim(ctx, p, d.ID, f); err != nil {
		return err
	}

	rows, err := p.q.ListDeviceSources(ctx, d.ID)
	if err != nil {
		return fmt.Errorf("claims about device %d: %w", d.ID, err)
	}

	claims := make([]nameClaim, 0, len(rows))
	for _, r := range rows {
		claims = append(claims, nameClaim{
			name:     r.DeviceSource.Hostname.String,
			standing: r.DeviceSource.HostnameSource,
			at:       r.DeviceSource.LastSeen,
		})
	}

	won := resolveHostname(claims)
	if won.name == d.Hostname.String && won.standing == d.HostnameSource {
		return nil
	}

	params := models.SetDeviceHostnameParams{
		Hostname:       nullString(won.name),
		HostnameSource: won.standing,
		ID:             d.ID,
	}
	if err := p.q.SetDeviceHostname(ctx, params); err != nil {
		return fmt.Errorf("set hostname of device %d: %w", d.ID, err)
	}

	// A first name is part of discovering the device, not a change to it.
	if !d.Hostname.Valid {
		return nil
	}

	// Two spellings of one name are not a rename, or a device whose PTR and
	// lease label differ by a domain would log one every cycle.
	if sameName(won.name, d.Hostname.String) {
		return nil
	}

	return s.event(ctx, p, d.ID, dbtype.EventHostnameChanged, d.Hostname.String, won.name, "")
}

// recordClaim files this source's reading, replacing what the same source said
// before.
//
// last_seen advances whenever the source still reports the device, presence or
// not: a router still holds a static lease with nothing plugged in. Whether
// anything answered is devices.last_seen's question.
func (s *Store) recordClaim(ctx context.Context, p *pass, deviceID int64, f plugin.Fact) error {
	detail, err := claimDetail(f.Detail)
	if err != nil {
		return fmt.Errorf("detail of %s: %w", f.Host.Address(), err)
	}

	err = p.q.UpsertDeviceSource(ctx, models.UpsertDeviceSourceParams{
		DeviceID:       deviceID,
		SourceID:       p.sourceID,
		Hostname:       nullString(f.Host.Hostname()),
		HostnameSource: f.HostnameSource,
		Detail:         detail,
		FirstSeen:      p.at,
		LastSeen:       p.at,
	})
	if err != nil {
		return fmt.Errorf("record claim about device %d: %w", deviceID, err)
	}

	return nil
}

// claimDetail renders what only this source knows as the column's JSON, null
// when it knows nothing extra. encoding/json sorts map keys, so an unchanged
// reading re-marshals identically and the row does not churn.
func claimDetail(detail map[string]string) (sql.NullString, error) {
	if len(detail) == 0 {
		return sql.NullString{}, nil
	}

	raw, err := json.Marshal(detail)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("marshal detail: %w", err)
	}

	return sql.NullString{String: string(raw), Valid: true}, nil
}

func (s *Store) event(ctx context.Context, p *pass, deviceID int64, kind dbtype.EventKind, from, to, detail string) error {
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

// currentHolder returns the device holding ip right now, and nil when the
// address is free. Nothing holding an address is an answer, not a failure, so
// it is reported as a nil device rather than as an error to match on.
func currentHolder(ctx context.Context, q *models.Queries, ip dbtype.Addr) (*models.Device, error) {
	row, err := q.GetDeviceByCurrentIP(ctx, ip)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("device holding %s: %w", ip, err)
	}

	return &row.Device, nil
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
