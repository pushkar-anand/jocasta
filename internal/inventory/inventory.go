// Package inventory merges what a source reports into the device records that
// it belongs to.
//
// The hardware address identifies a device. If only the answering address is
// known, that address identifies it. Every change of the merge goes into the
// event log.
package inventory

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
)

// DefaultOnlineWindow is how recently a device must answer to count as online,
// when no window is configured.
const DefaultOnlineWindow = 15 * time.Minute

// DefaultAddressGrace is how long an address in a swept prefix can stay
// without an answer while its device answers elsewhere in that prefix. After
// it, the sweep decides that the lease is gone, and retires the address.
const DefaultAddressGrace = 6 * time.Hour

// DefaultRetention is how long the store keeps events and scans, when no
// retention is configured.
const DefaultRetention = 90 * 24 * time.Hour

// DefaultTrafficRetention is how long the store keeps hourly traffic totals,
// when no retention is configured. It is shorter than for the event log,
// because traffic grows with the activity of the network.
const DefaultTrafficRetention = 30 * 24 * time.Hour

// DefaultDNSRetention is how long the store keeps hourly lookup counts, when no
// retention is configured. As traffic, they grow with the activity of the
// network.
const DefaultDNSRetention = 30 * 24 * time.Hour

// DefaultDeviceRetention is how long the store keeps a device that its owner
// never curated, after it was last seen. It applies when no retention is
// configured.
const DefaultDeviceRetention = 90 * 24 * time.Hour

// Store reads the inventory and writes scan results into it.
type Store struct {
	conn *sql.DB
	q    *models.Queries
	log  *slog.Logger

	// now is a field, so tests can fix the timestamps that they assert on.
	now func() time.Time

	// onlineWindow is how long a device counts as online after it was last
	// seen. No source reports that a device left. So presence depends on the
	// age of the last sighting. The age that is too old depends on how often
	// the sweeps run.
	onlineWindow time.Duration

	// addressGrace is how long the store keeps an address on which a swept
	// device stopped answering. After it, the sweep retires the address. An
	// answer of the device elsewhere in the prefix proves that it moved. This
	// window only covers a second interface whose replies were lost for one
	// whole sweep.
	addressGrace time.Duration

	// onScan gets each finished scan. If it is nil, nobody gets them.
	onScan func(ctx context.Context, scanID int64)
}

// OnlineWindow is how recently a device must be seen to count as online. The
// web layer shows it. So "seen recently" is not a limit with no explanation.
func (s *Store) OnlineWindow() time.Duration {
	return s.onlineWindow
}

// Now reads the clock that the store uses for presence. So a page can tell how
// long ago something happened, from the same time as the queries of the
// store.
func (s *Store) Now() time.Time {
	return s.now()
}

// Option configures a Store.
type Option func(*Store)

// WithOnlineWindow sets how recently a device must be seen to count as online.
// A window of zero or less keeps the default.
func WithOnlineWindow(d time.Duration) Option {
	return func(s *Store) {
		if d > 0 {
			s.onlineWindow = d
		}
	}
}

// WithAddressGrace sets how long a swept device can stay without an answer on
// an address. After it, a later sweep that finds the device elsewhere in the
// prefix retires that address. A window of zero or less keeps the default.
func WithAddressGrace(d time.Duration) Option {
	return func(s *Store) {
		if d > 0 {
			s.addressGrace = d
		}
	}
}

// WithClock replaces the clock that the store uses for writes and presence.
func WithClock(now func() time.Time) Option {
	return func(s *Store) {
		if now != nil {
			s.now = now
		}
	}
}

// New returns a store over conn, with the options in opts.
func New(conn *sql.DB, log *slog.Logger, opts ...Option) *Store {
	s := &Store{
		conn:         conn,
		q:            models.New(conn),
		log:          log,
		now:          time.Now,
		onlineWindow: DefaultOnlineWindow,
		addressGrace: DefaultAddressGrace,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Result counts what a single reading changed. Seen counts the recorded facts.
// For a sweep, this is the number of addresses that answered, because
// everything that a sweep returns answered a probe.
type Result struct {
	ScanID     int64
	Discovered int
	Identified int
	Merged     int
	Seen       int

	// Released counts the addresses that a swept device left. It answered
	// elsewhere in the prefix, while these addresses were silent for longer
	// than the grace window. The rows stay, with is_current cleared.
	Released int

	// Back and Quiet count the devices that crossed the online window. Back
	// is a device seen again after longer than the window. Quiet is a device
	// gone for longer than the window, for a reading that could see it. Both
	// count every device. Only a watched device logs an event.
	Back  int
	Quiet int

	// Dropped counts the facts that had no device to record against. They had
	// no hardware address, and no address of a device. If the store trusted
	// them, it would make up a device that nothing identifies.
	Dropped int

	// New holds the address of each device that this reading found. So a scan
	// of its ports does not need to wait for the next full port scan.
	New []netip.Addr
}

// reading is what one source read: who read it, what it claims, and the
// network of the reading, if it covered exactly one.
type reading struct {
	source string
	kind   dbtype.SourceKind

	// network is the prefix of a sweep. A source that reads every network at
	// once has no limit, and leaves this nil.
	network *netip.Prefix

	facts []plugin.Fact
}

// pass holds what every write in one ingest shares. This includes the single
// timestamp of all of them.
type pass struct {
	q      *models.Queries
	scanID int64

	// sourceID puts every claim that this reading writes under the source of
	// the reading.
	sourceID int64

	networks networks
	at       dbtype.Time
	res      Result

	// seen and answered record what this pass reached. So the retire step
	// after it can tell an address that a device left from an address that it
	// still has. A device that left an address answered somewhere, but the
	// address did not.
	seen     map[int64]struct{}
	answered map[netip.Addr]struct{}

	// touched is every device of a fact, present or not. The classify pass,
	// after the ingest commits, reads each of these again.
	touched map[int64]struct{}
}

// networks matches an address to the recorded network that contains it. A
// sweep knows its prefix. A source that reads every VLAN at once knows only
// addresses. So the store looks up the network for each address.
type networks []recordedNetwork

type recordedNetwork struct {
	id     int64
	prefix netip.Prefix
}

// find returns the network that contains addr. It returns false when no
// recorded network does. The most specific match wins. A supernet and its
// subnet can both be recorded. The narrower network is the better answer.
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

// broadcast reports whether addr is the broadcast address of a recorded IPv4
// network. This is its last address, on a prefix long enough to have one. A
// /31 or /32 has no broadcast address. IPv6 has none.
func (ns networks) broadcast(addr netip.Addr) bool {
	if !addr.Is4() {
		return false
	}

	for _, n := range ns {
		if !n.prefix.Addr().Is4() || n.prefix.Bits() > 30 || !n.prefix.Contains(addr) {
			continue
		}

		b := n.prefix.Masked().Addr().As4()
		last := binary.BigEndian.Uint32(b[:]) | (1<<(32-n.prefix.Bits()) - 1)

		if addr == netip.AddrFrom4([4]byte(binary.BigEndian.AppendUint32(nil, last))) {
			return true
		}
	}

	return false
}

// networkID renders the result of find as its nullable column.
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

// RecordSweep stores the results of one sweep of prefix, under the named
// source.
//
// A sweep is one of several sources. It is different only in two ways.
// Everything that it returns answered a probe. Any name that it has came from
// its own lookup. So it states these two things, and gives the facts to the
// same path as every other source.
func (s *Store) RecordSweep(
	ctx context.Context,
	source string,
	prefix netip.Prefix,
	hosts []scanner.Host,
) (*Result, error) {
	return s.report(ctx, reading{
		source:  source,
		kind:    dbtype.SourceSweep,
		network: &prefix,
		facts:   sweptFacts(hosts),
	})
}

// sweptFacts tells what a sweep result claims. The address answered, so the
// device is here now. Its name has the standing that the sweep gave it.
func sweptFacts(hosts []scanner.Host) []plugin.Fact {
	facts := make([]plugin.Fact, len(hosts))

	for i, h := range hosts {
		f := plugin.Fact{Host: h.Host, Present: true, Services: h.Services, SeenAt: h.SeenAt}

		// The standing goes with the name. A fact with a source for a name
		// that it does not have is a standing for nothing. A name with no
		// standing counts as reverse DNS, the lookup that [hosts.BuildHost]
		// does for a sweep.
		if h.Hostname() != "" {
			f.HostnameSource = cmp.Or(h.NameSource, dbtype.HostnameFromDNS)
		}

		// An address with several reverse names keeps the first as its name.
		// The claim lists all of them. So the device page shows the names
		// that the store did not use.
		if names := h.ReverseNames(); len(names) > 1 && f.HostnameSource == dbtype.HostnameFromDNS {
			f.Detail = map[string]string{"reverse_dns": strings.Join(names, ", ")}
		}

		facts[i] = f
	}

	return facts
}

// RecordFacts stores what one source claims about the devices that it knows.
//
// The store asks a source what it knows. The source probes no prefix. So the
// reading has no network limit, and the store matches each address to its
// network. Its facts do not need to assert presence. A lease can name a device
// that is configured and does not answer.
//
// The kind is a parameter here, and fixed in RecordSweep. So the callers of a
// sweep cannot give the wrong kind.
func (s *Store) RecordFacts(
	ctx context.Context,
	source string,
	kind dbtype.SourceKind,
	facts []plugin.Fact,
) (*Result, error) {
	return s.report(ctx, reading{source: source, kind: kind, facts: facts})
}

// RecordNetworks stores the segments that a source says that it serves.
//
// It writes no scan row. A scan is a reading of devices, with its own count
// and time. The name of a prefix changes no device. It would close a scan
// that found nothing.
//
// The whole set goes in one transaction. So a run that fails in the middle
// leaves the segments as the last good reading described them.
func (s *Store) RecordNetworks(ctx context.Context, nets []plugin.Network) error {
	if len(nets) == 0 {
		return nil
	}

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin networks: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	at := s.stamp()

	for _, n := range nets {
		if !n.Prefix.IsValid() {
			continue
		}

		err := q.UpsertNetworkIdentity(ctx, models.UpsertNetworkIdentityParams{
			Cidr:      dbtype.NewPrefix(n.Prefix),
			Name:      nullString(n.Name),
			VlanID:    nullInt64(n.VLAN),
			CreatedAt: at,
		})
		if err != nil {
			return fmt.Errorf("upsert network %s: %w", n.Prefix, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit networks: %w", err)
	}

	return nil
}

// nullInt64 renders zero as null. This is an untagged segment, because zero is
// not a usable 802.1Q tag. Or it is no device, because ids start at one.
func nullInt64[T int | int64](n T) sql.NullInt64 {
	return sql.NullInt64{Int64: int64(n), Valid: n != 0}
}

// report records the reading of one source. It opens a scan row, ingests the
// facts as one transaction, and closes the scan with the outcome.
func (s *Store) report(ctx context.Context, r reading) (*Result, error) {
	scanID, sourceID, err := s.openScan(ctx, r.source, r.kind, dbtype.ScanDiscovery, dbtype.ScopeFull, r.network)
	if err != nil {
		return nil, err
	}

	res, touched, ingestErr := s.ingest(ctx, scanID, sourceID, r)

	// res is nil unless the ingest committed. So a failed scan records the
	// only true count for the table: none.
	found := 0
	if ingestErr == nil {
		found = res.Seen
	}

	// The scan row closes in its own transaction. So the store still records
	// a failure when its work rolls back.
	closeErr := s.close(ctx, scanID, found, ingestErr)

	// The scan id goes in the error, and the Result stays nil. So a caller
	// never needs to know which fields stay after a failure.
	if err := errors.Join(ingestErr, closeErr); err != nil {
		return nil, fmt.Errorf("scan %d: %w", scanID, err)
	}

	res.ScanID = scanID

	// The classify pass is best-effort. A failed update of a guess gets a log
	// line. The scan that merged its devices correctly still succeeds.
	if err := s.reclassify(ctx, scanID, touched); err != nil {
		s.log.WarnContext(ctx, "classify pass after discovery failed", slog.Int64("scan", scanID), logger.Err(err))
	}

	s.scanFinished(ctx, scanID)

	return res, nil
}

// openScan is the first of the three phases of every recorded reading. It
// registers the source, and opens a running scan row of the given kind and
// scope. If the reading covered exactly one network, it records the network.
func (s *Store) openScan(
	ctx context.Context,
	source string,
	srcKind dbtype.SourceKind,
	scanKind dbtype.ScanKind,
	scope dbtype.ScanScope,
	network *netip.Prefix,
) (scanID, sourceID int64, err error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin scan: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)
	at := s.stamp()

	src, err := q.UpsertSource(ctx, models.UpsertSourceParams{Kind: srcKind, Name: source, CreatedAt: at})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert source %q: %w", source, err)
	}

	// The record of the network here also lets the addresses in this reading
	// match it. A prefix that nothing swept before is not in the table until
	// the scan that covers it opens.
	var networkID sql.NullInt64

	if network != nil {
		nw, err := q.UpsertNetwork(ctx, models.UpsertNetworkParams{Cidr: dbtype.NewPrefix(*network), CreatedAt: at})
		if err != nil {
			return 0, 0, fmt.Errorf("upsert network %s: %w", network, err)
		}

		networkID = sql.NullInt64{Int64: nw.ID, Valid: true}
	}

	sc, err := q.CreateScan(ctx, models.CreateScanParams{
		SourceID:  src.ID,
		Kind:      scanKind,
		Scope:     scope,
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

// ingest applies every fact as one transaction. A reading goes in whole, or not
// at all. So a partial run cannot leave a device with an address that it was
// about to lose.
func (s *Store) ingest(ctx context.Context, scanID, sourceID int64, r reading) (*Result, []int64, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin ingest: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	nets, err := loadNetworks(ctx, q)
	if err != nil {
		return nil, nil, err
	}

	// One timestamp for the whole reading. Every row that it writes describes
	// the same observation. A clock read for each row could make the last seen
	// time of a device earlier than its first seen time.
	p := &pass{
		q:        q,
		scanID:   scanID,
		sourceID: sourceID,
		networks: nets,
		at:       s.stamp(),
		seen:     map[int64]struct{}{},
		answered: map[netip.Addr]struct{}{},
		touched:  map[int64]struct{}{},
	}

	for _, f := range r.facts {
		if err := s.record(ctx, p, f); err != nil {
			return nil, nil, fmt.Errorf("record %s: %w", f.Host.Address(), err)
		}
	}

	// A sweep is the only reading with a prefix limit. So only a sweep can say
	// that an address in it is absent. A router read that covers every VLAN
	// leaves r.network nil, and retires nothing.
	if r.network != nil {
		if err := s.retire(ctx, p, *r.network); err != nil {
			return nil, nil, err
		}
	}

	if err := s.depart(ctx, p, r); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit ingest: %w", err)
	}

	return &p.res, slices.Sorted(maps.Keys(p.touched)), nil
}

// close marks the scan as finished, with the ingest failure if there was one.
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

// record applies one source fact in the transaction of p. It updates the counts
// and touched devices of p. A fact without presence cannot take an address
// from its current holder. A fact that identifies no device counts as
// dropped.
func (s *Store) record(ctx context.Context, p *pass, f plugin.Fact) error {
	ip := dbtype.NewAddr(f.Host.Address())
	mac := s.hardware(ctx, f)

	// A fact that does not assert presence tells nothing about the holder of
	// the address now. So it never looks for the current holder. A static
	// lease for a disconnected device must not identify, merge or take an
	// address from the device that answers on that address today.
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
			slog.String("addr", f.Host.Address().String()), slog.String("mac", f.Host.MAC), slog.Bool("present", f.Present))

		p.res.Dropped++

		return nil
	}

	// After the ingest commits, the store classifies every device of a fact
	// again. target is the row that stays. A merge below puts the ghost holder
	// into it. So its id stays valid through the rest of record.
	p.touched[target.ID] = struct{}{}

	// A row that only ever stood for this address is the same device, with a
	// weaker name, when a hardware address claims it.
	if holder != nil && holder.ID != target.ID && holder.IdentitySource == dbtype.IdentityIP {
		if err := s.fold(ctx, p, holder, target); err != nil {
			return err
		}

		p.res.Merged++
	}

	// An address and a sighting are both presence claims. A source that does
	// not make one can still say the name of a device.
	if f.Present {
		if err := s.claim(ctx, p, target.ID, ip); err != nil {
			return err
		}
	}

	if err := s.applyClaim(ctx, p, target, f); err != nil {
		return err
	}

	if err := recordServices(ctx, p, target.ID, f.Services); err != nil {
		return err
	}

	if f.Present {
		// Before the touch, so arrive still reads the last sighting.
		if err := s.arrive(ctx, p, target); err != nil {
			return err
		}

		err := p.q.TouchDevice(ctx, models.TouchDeviceParams{
			LastSeen:    p.at,
			Vendor:      nullString(f.Host.Vendor()),
			VendorShort: nullString(f.Host.ShortName()),
			ID:          target.ID,
		})
		if err != nil {
			return fmt.Errorf("touch device %d: %w", target.ID, err)
		}

		// This device answered, on this address. The retire step reads both.
		// The device is in here. If an address of the device is not, the
		// device left that address.
		p.seen[target.ID] = struct{}{}

		if addr := ip.Addr; addr.IsValid() {
			p.answered[addr] = struct{}{}
		}
	}

	p.res.Seen++

	return nil
}

// retire releases the addresses that a swept device left. An address is one of
// these in this case. Its device answered this sweep. The address is in the
// swept prefix. It did not answer itself. Its last answer is older than the
// grace window.
//
// An answer of the device elsewhere in the prefix separates a move from an
// absence. A device that answered nowhere is offline. A silent sweep tells
// nothing about a change of its lease. The grace window then covers a second
// interface on the prefix whose replies were all lost for one sweep.
//
// The released row stays, with is_current cleared. So "where was this before"
// is still a read. last_seen stays at the last real sighting.
func (s *Store) retire(ctx context.Context, p *pass, prefix netip.Prefix) error {
	cutoff := p.at.Add(-s.addressGrace)

	seen := slices.Sorted(maps.Keys(p.seen))

	for _, deviceID := range seen {
		held, err := p.q.CurrentAddresses(ctx, deviceID)
		if err != nil {
			return fmt.Errorf("current addresses of device %d: %w", deviceID, err)
		}

		for _, a := range held {
			if _, answered := p.answered[a.IP.Addr]; answered {
				continue
			}

			if !prefix.Contains(a.IP.Addr) || !a.LastSeen.Before(cutoff) {
				continue
			}

			if err := p.q.RetireAddress(ctx, a.ID); err != nil {
				return fmt.Errorf("retire %s: %w", a.IP, err)
			}

			detail := fmt.Sprintf("unanswered since %s; device seen elsewhere in %s",
				a.LastSeen.Format(time.RFC3339), prefix)
			if err := s.event(ctx, p, deviceID, dbtype.EventAddressReleased, a.IP.String(), "", detail); err != nil {
				return err
			}

			p.res.Released++
		}
	}

	return nil
}

// hardware parses the hardware address of a fact. A value that is not a 6-byte
// address identifies nothing. So the fact keeps everything else, and the
// answering address still identifies the device.
func (s *Store) hardware(ctx context.Context, f plugin.Fact) dbtype.MAC {
	if f.Host.MAC == "" {
		return dbtype.MAC{}
	}

	mac, err := dbtype.ParseMAC(f.Host.MAC)
	if err != nil {
		s.log.DebugContext(ctx, "ignoring unusable hardware address",
			slog.String("addr", f.Host.Address().String()), slog.String("mac", f.Host.MAC), logger.Err(err))

		return dbtype.MAC{}
	}

	return mac
}

// resolve finds the device of a fact. If no device is known yet, it creates or
// identifies one. A nil holder means that nothing claims the address now. The
// store must act on this state.
//
// It returns a nil device and no error for a fact with no device to record
// against. Such a fact identifies no device, and does not say that the
// address answers. An incomplete neighbour entry names nothing. A source that
// reports a device that it did not see can enrich a known device, but cannot
// create one. A new row would put a device that nothing ever saw into the
// inventory, counted as online.
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

	// The address answered before its hardware was visible. So the row that
	// already stands for it becomes the identified device.
	if holder != nil && holder.IdentitySource == dbtype.IdentityIP {
		params := models.IdentifyDeviceParams{
			MAC:          mac,
			IsRandomised: f.Host.Randomised(),
			Vendor:       nullString(f.Host.Vendor()),
			VendorShort:  nullString(f.Host.ShortName()),
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
		holder.VendorShort = params.VendorShort
		p.res.Identified++

		return holder, nil
	}

	if !f.Present {
		return nil, nil
	}

	return s.create(ctx, p, mac, f)
}

// create records a new device and its discovery event in the transaction of p.
// Without a hardware address, the device has only an IP identity.
func (s *Store) create(ctx context.Context, p *pass, mac dbtype.MAC, f plugin.Fact) (*models.Device, error) {
	source := dbtype.IdentityIP
	if mac.Valid() {
		source = dbtype.IdentityMAC
	}

	d, err := p.q.CreateDevice(ctx, models.CreateDeviceParams{
		MAC:            mac,
		IdentitySource: source,
		IsRandomised:   f.Host.Randomised(),
		Vendor:         nullString(f.Host.Vendor()),
		VendorShort:    nullString(f.Host.ShortName()),
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

	if a := f.Host.Address(); a.IsValid() {
		p.res.New = append(p.res.New, a)
	}

	return d, nil
}

// fold merges a device that only its address identified into the device that
// its hardware address identifies. The curation of the user on the weaker row
// goes with it, because the user could not know that it was a duplicate.
// Everything recorded against the weaker row also moves. fold updates into to
// the merged row.
func (s *Store) fold(ctx context.Context, p *pass, ghost, into *models.Device) error {
	merged, err := p.q.AdoptCuration(ctx, models.AdoptCurationParams{
		FoldedLabel:      ghost.Label,
		FoldedNotes:      ghost.Notes,
		FoldedGroupName:  ghost.GroupName,
		FoldedDeviceType: ghost.DeviceType,
		IsIgnored:        into.IsIgnored || ghost.IsIgnored,
		IsWatched:        into.IsWatched || ghost.IsWatched,
		FirstSeen:        earlier(into.FirstSeen, ghost.FirstSeen),
		LastSeen:         later(into.LastSeen, ghost.LastSeen),
		PresentSince:     earlierRun(into.PresentSince, ghost.PresentSince),
		ID:               into.ID,
	})
	if err != nil {
		return fmt.Errorf("adopt curation of device %d: %w", ghost.ID, err)
	}

	*into = *merged

	if err := p.q.MoveAddresses(ctx, models.MoveAddressesParams{IntoID: into.ID, FromID: ghost.ID}); err != nil {
		return fmt.Errorf("move addresses of device %d: %w", ghost.ID, err)
	}

	// Without this, a source with claims on both rows would lose its claim to
	// the CASCADE when the ghost is deleted.
	err = p.q.MoveDeviceSources(ctx, models.MoveDeviceSourcesParams{IntoID: into.ID, FromID: ghost.ID})
	if err != nil {
		return fmt.Errorf("move claims about device %d: %w", ghost.ID, err)
	}

	// The CASCADE would also delete the ports, services and hourly rows of the
	// ghost, and the rows of other devices that name it as their peer.
	intoID := sql.NullInt64{Int64: into.ID, Valid: true}
	fromID := sql.NullInt64{Int64: ghost.ID, Valid: true}

	moves := []struct {
		what string
		move func() error
	}{
		{"ports of", func() error {
			return p.q.MoveDevicePorts(ctx, models.MoveDevicePortsParams{IntoID: into.ID, FromID: ghost.ID})
		}},
		{"services of", func() error {
			return p.q.MoveDeviceServices(ctx, models.MoveDeviceServicesParams{IntoID: into.ID, FromID: ghost.ID})
		}},
		{"traffic of", func() error {
			return p.q.MoveTraffic(ctx, models.MoveTrafficParams{IntoID: into.ID, FromID: ghost.ID})
		}},
		{"attempts of", func() error {
			return p.q.MoveAttempts(ctx, models.MoveAttemptsParams{IntoID: into.ID, FromID: ghost.ID})
		}},
		{"probes of", func() error {
			return p.q.MoveProbes(ctx, models.MoveProbesParams{IntoID: into.ID, FromID: ghost.ID})
		}},
		{"broadcasts of", func() error {
			return p.q.MoveBroadcasts(ctx, models.MoveBroadcastsParams{IntoID: into.ID, FromID: ghost.ID})
		}},
		{"lookups of", func() error {
			return p.q.MoveDNS(ctx, models.MoveDNSParams{IntoID: into.ID, FromID: ghost.ID})
		}},
		{"answers of", func() error {
			return p.q.MoveDNSAnswers(ctx, models.MoveDNSAnswersParams{IntoID: into.ID, FromID: ghost.ID})
		}},
		{"traffic with", func() error {
			return p.q.RepointTrafficPeer(ctx, models.RepointTrafficPeerParams{IntoID: intoID, FromID: fromID})
		}},
		{"attempts on", func() error {
			return p.q.RepointAttemptPeer(ctx, models.RepointAttemptPeerParams{IntoID: intoID, FromID: fromID})
		}},
	}

	for _, m := range moves {
		if err := m.move(); err != nil {
			return fmt.Errorf("move %s device %d: %w", m.what, ghost.ID, err)
		}
	}

	err = p.q.MoveEvents(ctx, models.MoveEventsParams{
		IntoID: intoID,
		FromID: fromID,
	})
	if err != nil {
		return fmt.Errorf("move events of device %d: %w", ghost.ID, err)
	}

	s.log.InfoContext(ctx, "folded device into its identified twin", slog.Int64("from", ghost.ID), slog.Int64("into", into.ID))

	detail := fmt.Sprintf("device %d folded into %d", ghost.ID, into.ID)
	if err := s.event(ctx, p, into.ID, dbtype.EventDevicesMerged, "", "", detail); err != nil {
		return err
	}

	if err := p.q.DeleteDevice(ctx, ghost.ID); err != nil {
		return fmt.Errorf("delete folded device %d: %w", ghost.ID, err)
	}

	return nil
}

// claim makes the address current for the device. It takes it from any other
// holder.
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

// applyClaim records the reading of this source against the device. Then it
// elects the name of the device row again, from the claims of every source.
//
// The row holds one name, because the device list shows one for each row and
// searches one column. The claims behind it stay. So the device page can show
// a source with a lower rank.
//
// Each pass elects from every claim again. So the store sees a new name from a
// source, or a contradiction from a higher source. A source that stops
// reporting a name keeps the last name that it gave (see UpsertDeviceSource).
// A device whose claims have no name keeps its name. So a device that goes
// quiet does not lose its name.
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

	won := resolveHostname(claims, d.Hostname.String)
	if won.name == "" || won.name == d.Hostname.String && won.standing == d.HostnameSource {
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

	// A first name is part of the discovery of the device.
	if !d.Hostname.Valid {
		return nil
	}

	// Two spellings of one name count as one. If not, a device whose PTR and
	// lease label differ by a domain would log a rename every cycle.
	if sameName(won.name, d.Hostname.String) {
		return nil
	}

	return s.event(ctx, p, d.ID, dbtype.EventHostnameChanged, d.Hostname.String, won.name, "")
}

// recordClaim writes the reading of this source over what the same source said
// before about the same address. A reading with no name keeps the last name
// (see UpsertDeviceSource). A swept name that ranks below the name of the
// claim also keeps that name (see yieldsToHeldName).
//
// last_seen moves forward whenever the source still reports the device, with
// or without presence. A router still has a static lease with nothing
// connected. devices.last_seen answers whether something answered.
func (s *Store) recordClaim(ctx context.Context, p *pass, deviceID int64, f plugin.Fact) error {
	detail, err := claimDetail(f.Detail)
	if err != nil {
		return fmt.Errorf("detail of %s: %w", f.Host.Address(), err)
	}

	name := f.Host.Hostname()

	yields, err := yieldsToHeldName(ctx, p, deviceID, f)
	if err != nil {
		return err
	}

	if yields {
		name = ""
	}

	err = p.q.UpsertDeviceSource(ctx, models.UpsertDeviceSourceParams{
		DeviceID:       deviceID,
		SourceID:       p.sourceID,
		Address:        dbtype.NewAddr(f.Host.Address()),
		Hostname:       nullString(name),
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

// yieldsToHeldName reports whether the name of f must not change the claim of
// this source at the address of f. This is true when a sweep learned both
// names, and the name of the claim ranks higher.
//
// A sweep asks each lookup in order, from the highest standing down. It asks
// the next only when the one before gave no name. A lookup that runs past its
// timeout also gives none. If the answer of the next lookup replaced the held
// name, the device would get a new name. The next sweep would then change it
// back. The claims of every other source hold no swept name. So they take
// each new name that they get.
//
// The held name does not expire. A device can stop answering the higher lookup
// for good, such as a NAS with Avahi off. It keeps that name until that lookup,
// or a higher one, gives a different name. This is accepted. An empty lookup
// of a sweep always kept the last name. In both cases, a label on the device
// shows instead of the name.
func yieldsToHeldName(ctx context.Context, p *pass, deviceID int64, f plugin.Fact) (bool, error) {
	if f.Host.Hostname() == "" || !f.HostnameSource.Swept() {
		return false, nil
	}

	held, err := p.q.GetDeviceSourceName(ctx, models.GetDeviceSourceNameParams{
		DeviceID: deviceID,
		SourceID: p.sourceID,
		Address:  dbtype.NewAddr(f.Host.Address()),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("claim about device %d: %w", deviceID, err)
	}

	return held.Hostname.Valid &&
		held.HostnameSource.Swept() &&
		held.HostnameSource.Rank() > f.HostnameSource.Rank(), nil
}

// claimDetail renders what only this source knows as the JSON of the column. It
// is null when the source knows nothing more. encoding/json sorts map keys. So
// an unchanged reading marshals in the same way, and the row does not
// change.
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

func (s *Store) event(
	ctx context.Context,
	p *pass,
	deviceID int64,
	kind dbtype.EventKind,
	from, to, detail string,
) error {
	return s.writeEvent(ctx, p.q, p.scanID, p.at, deviceID, kind, from, to, detail)
}

// writeEvent appends one row to the change log. It takes the queries, scan id
// and timestamp as separate arguments. So a reading that builds no pass, such
// as a port scan, can log in the same way.
func (s *Store) writeEvent(
	ctx context.Context,
	q *models.Queries,
	scanID int64,
	at dbtype.Time,
	deviceID int64,
	kind dbtype.EventKind,
	from, to, detail string,
) error {
	err := q.CreateEvent(ctx, models.CreateEventParams{
		DeviceID:   sql.NullInt64{Int64: deviceID, Valid: true},
		ScanID:     sql.NullInt64{Int64: scanID, Valid: true},
		Kind:       kind,
		OldValue:   nullString(from),
		NewValue:   nullString(to),
		Detail:     nullString(detail),
		OccurredAt: at,
	})
	if err != nil {
		return fmt.Errorf("record %s event: %w", kind, err)
	}

	return nil
}

// stamp reads the clock in the form that the schema stores.
func (s *Store) stamp() dbtype.Time {
	return dbtype.NewTime(s.now())
}

// currentHolder returns the device that holds ip now. It returns nil when the
// address is free. A free address is an answer. So it is a nil device with a
// nil error.
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

// earlier returns the first of the two timestamps. A merged device was seen
// from the time when either of its rows was seen.
func earlier(a, b dbtype.Time) dbtype.Time {
	if b.Before(a.Time) {
		return b
	}

	return a
}

// later returns the last of the two timestamps.
func later(a, b dbtype.Time) dbtype.Time {
	if b.After(a.Time) {
		return b
	}

	return a
}

// earlierRun returns the present run that began first. If the other device is
// quiet, it returns the run that is set.
func earlierRun(a, b dbtype.NullTime) dbtype.NullTime {
	switch {
	case !a.Valid:
		return b
	case !b.Valid:
		return a
	}

	return dbtype.NullTime{Time: earlier(a.Time, b.Time), Valid: true}
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// scanFinished tells the scan listener, if there is one, that all changes of a
// scan are committed.
func (s *Store) scanFinished(ctx context.Context, scanID int64) {
	if s.onScan != nil {
		s.onScan(ctx, scanID)
	}
}
