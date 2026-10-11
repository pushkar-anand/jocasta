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
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// QueryResult counts what one recorded read of a query log held.
type QueryResult struct {
	// Recorded is the lookups counted against a device, and Dropped those
	// from an address no device holds.
	Recorded int
	Dropped  int
}

// QueryCursor returns where the last recorded read of source left off, and
// an empty cursor when nothing has been recorded from it.
func (s *Store) QueryCursor(ctx context.Context, source string) (string, error) {
	pos, err := s.q.SourceCursor(ctx, source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("cursor of %s: %w", source, err)
	}

	return pos, nil
}

// RecordQueries adds one read of source's query log to each device's hourly
// counts, and stores cursor as where the next read starts. Both land in one
// transaction, so a read is counted once whatever fails around it.
//
// A lookup is counted against the device holding its client address now. A
// lookup from an address no device holds says nothing about the inventory
// and is dropped.
//
// The addresses a lookup was answered with are kept against the device too,
// unless the resolver blocked it or found nothing: a blocked name's answer is
// whatever the resolver blocks with, which names no peer.
func (s *Store) RecordQueries(
	ctx context.Context,
	source string,
	kind dbtype.SourceKind,
	queries []plugin.Query,
	cursor string,
) (*QueryResult, error) {
	holders, err := s.currentHolders(ctx)
	if err != nil {
		return nil, err
	}

	type key struct {
		device int64
		hour   time.Time
		name   string
	}

	type answerKey struct {
		device int64
		addr   netip.Addr
		name   string
	}

	counts := make(map[key]*models.UpsertDNSParams)
	answers := make(map[answerKey]*models.UpsertDNSAnswerParams)

	var res QueryResult

	for _, lookup := range queries {
		device := holders[lookup.Client]
		if device == 0 {
			res.Dropped++

			continue
		}

		res.Recorded++

		k := key{device: device, hour: lookup.At.UTC().Truncate(time.Hour), name: lookup.Name}

		c, ok := counts[k]
		if !ok {
			c = &models.UpsertDNSParams{DeviceID: device, Hour: dbtype.NewTime(k.hour), Name: lookup.Name}
			counts[k] = c
		}

		c.Lookups++

		if lookup.Blocked {
			c.Blocked++
		}

		if lookup.Failed {
			c.Failed++
		}

		if lookup.Blocked || lookup.Failed {
			continue
		}

		for _, addr := range lookup.Answers {
			if addr.IsUnspecified() || addr.IsLoopback() {
				continue
			}

			ak := answerKey{device: device, addr: addr, name: lookup.Name}

			a, ok := answers[ak]
			if !ok {
				a = &models.UpsertDNSAnswerParams{DeviceID: device, Addr: dbtype.NewAddr(addr), Name: lookup.Name}
				answers[ak] = a
			}

			a.Lookups++

			if k.hour.After(a.LastHour.Time) {
				a.LastHour = dbtype.NewTime(k.hour)
			}
		}
	}

	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin queries: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	src, err := q.UpsertSource(ctx, models.UpsertSourceParams{Kind: kind, Name: source, CreatedAt: s.stamp()})
	if err != nil {
		return nil, fmt.Errorf("source %s: %w", source, err)
	}

	for _, c := range counts {
		c.SourceID = src.ID

		if err := q.UpsertDNS(ctx, *c); err != nil {
			return nil, fmt.Errorf("lookups for device %d: %w", c.DeviceID, err)
		}
	}

	for _, a := range answers {
		if err := q.UpsertDNSAnswer(ctx, *a); err != nil {
			return nil, fmt.Errorf("answers for device %d: %w", a.DeviceID, err)
		}
	}

	if err := q.SetSourceCursor(ctx, models.SetSourceCursorParams{SourceID: src.ID, Position: cursor}); err != nil {
		return nil, fmt.Errorf("cursor of %s: %w", source, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit queries: %w", err)
	}

	if res.Dropped > 0 {
		s.log.DebugContext(ctx, "dropped lookups from addresses no device holds",
			slog.String("src", source),
			slog.Int("lookups", res.Dropped),
		)
	}

	return &res, nil
}

// deviceDNSLimit caps the names one device's view reads. A device that looked
// up more distinct names than this in a month is a crawler, and its busiest
// names are the ones worth showing.
const deviceDNSLimit = 2000

// DNSName is how often one device looked up one name over a period.
type DNSName struct {
	Name string `json:"name"`

	// Lookups counts every lookup of the name, Blocked those the resolver
	// blocked, and Failed those that found nothing or failed upstream.
	Lookups int64 `json:"lookups"`
	Blocked int64 `json:"blocked"`
	Failed  int64 `json:"failed"`

	// LastHour is the start of the latest hour the name was looked up in.
	LastHour time.Time `json:"last_hour"`
}

// DeviceDNS returns the names device id looked up from the hour since falls
// in, busiest first. A device that looked nothing up has an empty list.
func (s *Store) DeviceDNS(ctx context.Context, id int64, since time.Time) ([]*DNSName, error) {
	rows, err := s.q.DeviceDNS(ctx, models.DeviceDNSParams{
		DeviceID: id,
		Hour:     hourOf(since),
		Limit:    deviceDNSLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("lookups of device %d: %w", id, err)
	}

	out := make([]*DNSName, 0, len(rows))

	for _, r := range rows {
		last, err := parseHour("lookup", r.LastHour)
		if err != nil {
			return nil, err
		}

		out = append(out, &DNSName{Name: r.Name, Lookups: r.Lookups, Blocked: r.Blocked, Failed: r.Failed, LastHour: last})
	}

	return out, nil
}

// DNSTotals is what one device looked up over a period, added up over every
// name.
type DNSTotals struct {
	Lookups, Names, Blocked, Failed int64
}

// DeviceDNSTotals adds up what device id looked up from the hour since falls
// in. It counts every name, so it stays exact for a device that looked up
// more names than [Store.DeviceDNS] returns.
func (s *Store) DeviceDNSTotals(ctx context.Context, id int64, since time.Time) (*DNSTotals, error) {
	row, err := s.q.DeviceDNSTotals(ctx, models.DeviceDNSTotalsParams{DeviceID: id, Hour: hourOf(since)})
	if err != nil {
		return nil, fmt.Errorf("lookup totals of device %d: %w", id, err)
	}

	return &DNSTotals{Lookups: row.Lookups, Names: row.Names, Blocked: row.Blocked, Failed: row.Failed}, nil
}

// deviceAnswers returns, for each address device id's lookups were answered
// with from the hour since falls in, the names it looked up, most looked up
// first.
func (s *Store) deviceAnswers(ctx context.Context, id int64, since time.Time) (map[netip.Addr][]string, error) {
	rows, err := s.q.DeviceDNSAnswers(ctx, models.DeviceDNSAnswersParams{DeviceID: id, LastHour: hourOf(since)})
	if err != nil {
		return nil, fmt.Errorf("answers of device %d: %w", id, err)
	}

	out := make(map[netip.Addr][]string)

	for _, r := range rows {
		out[r.Addr.Addr] = append(out[r.Addr.Addr], r.Name)
	}

	return out, nil
}

// DNSRead reports whether any resolver's query log has been read.
func (s *Store) DNSRead(ctx context.Context) (bool, error) {
	n, err := s.q.AnyDNSRead(ctx)
	if err != nil {
		return false, fmt.Errorf("any lookups read: %w", err)
	}

	return n == 1, nil
}
