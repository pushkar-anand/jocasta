package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
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

	counts := make(map[key]*models.UpsertDNSParams)

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

// DNSRead reports whether any resolver's query log has been read.
func (s *Store) DNSRead(ctx context.Context) (bool, error) {
	n, err := s.q.AnyDNSRead(ctx)
	if err != nil {
		return false, fmt.Errorf("any lookups read: %w", err)
	}

	return n == 1, nil
}
