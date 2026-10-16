// Package models holds the query helpers over the generated SQL code.
package models

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/cursor"
)

// The two logs page by a seek past the row on which the last page ended. So the
// WHERE clause is there, or it is not. sqlc has no shape for that. It gives a
// nullable cursor parameter the type interface{}. That type would take a
// timestamp in any format. The alternative is the same SELECT written twice.
// So the paged queries are built here. Everything with a fixed shape stays in
// queries/inventory.sql.

// PageParams is one window of a log.
type PageParams struct {
	// Cursor is the row after which to continue. The zero cursor starts at
	// the top.
	Cursor cursor.Cursor

	// Limit is how many rows to read.
	Limit int64

	// DeviceID limits the change log to one device. Zero adds no clause, so
	// the result is the whole log. Only ListEvents uses it.
	DeviceID int64

	// EventKinds, ExcludeIgnored, ScanKind and ScanScope can limit their
	// logs.
	EventKinds     []dbtype.EventKind
	ExcludeIgnored bool
	ScanKind       dbtype.ScanKind
	ScanScope      dbtype.ScanScope
}

// ListEventsRow is one entry of the change log, with the device that it named.
//
// The device columns come from a LEFT JOIN, because an event can stay after
// its device. A delete of a device sets events.device_id to NULL, and keeps
// the record.
type ListEventsRow struct {
	Event          Event          `json:"event"`
	DeviceLabel    sql.NullString `json:"device_label"`
	DeviceHostname sql.NullString `json:"device_hostname"`
	DeviceMAC      dbtype.MAC     `json:"device_mac"`
}

// ListEvents returns one page of the change log, most recent first.
func (q *Queries) ListEvents(ctx context.Context, arg PageParams) ([]*ListEventsRow, error) {
	sb := squirrel.
		Select(
			"e.id", "e.device_id", "e.scan_id", "e.kind",
			"e.old_value", "e.new_value", "e.detail", "e.occurred_at",
			"d.label", "d.hostname", "d.mac",
		).
		From("events e").
		LeftJoin("devices d ON d.id = e.device_id").
		OrderBy("e.occurred_at DESC", "e.id DESC").
		Limit(pageLimit(arg.Limit))

	// A log for one device is the same read with one more clause. An event
	// whose device was deleted has a NULL device_id. So it never matches. This
	// is right, because no device page for it can exist.
	if arg.DeviceID != 0 {
		sb = sb.Where(squirrel.Eq{"e.device_id": arg.DeviceID})
	}

	if len(arg.EventKinds) != 0 {
		sb = sb.Where(squirrel.Eq{"e.kind": arg.EventKinds})
	}

	if arg.ExcludeIgnored {
		sb = sb.Where(squirrel.Eq{"d.is_ignored": false})
	}

	// Bind the timestamp of the cursor as a dbtype.Time. So it renders in the
	// one format of the column. occurred_at is TEXT, and SQLite compares it as
	// TEXT. So a value in a different form would compare wrong, with no
	// error.
	sb = arg.Cursor.WithValue(cursorTime(arg.Cursor)).Where(sb, "e.occurred_at", "e.id")

	rows, err := q.selectRows(ctx, sb)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var items []*ListEventsRow

	for rows.Next() {
		i := &ListEventsRow{}

		if err := rows.Scan(
			&i.Event.ID,
			&i.Event.DeviceID,
			&i.Event.ScanID,
			&i.Event.Kind,
			&i.Event.OldValue,
			&i.Event.NewValue,
			&i.Event.Detail,
			&i.Event.OccurredAt,
			&i.DeviceLabel,
			&i.DeviceHostname,
			&i.DeviceMAC,
		); err != nil {
			return nil, err
		}

		items = append(items, i)
	}

	return items, closeRows(rows)
}

// ListScansRow is one run of one source.
//
// network_cidr is cast and coalesced. The column override gives it the type
// non-null Prefix. That type cannot scan the NULL that a scan with no network
// gives here. A scan whose network was deleted also gives NULL.
type ListScansRow struct {
	Scan        Scan   `json:"scan"`
	SourceName  string `json:"source_name"`
	NetworkCidr string `json:"network_cidr"`
}

// ListScans returns one page of the scan history, most recent first.
func (q *Queries) ListScans(ctx context.Context, arg PageParams) ([]*ListScansRow, error) {
	sb := squirrel.
		Select(
			"s.id", "s.source_id", "s.kind", "s.scope", "s.network_id", "s.status",
			"s.error", "s.found_count", "s.started_at", "s.finished_at",
			"src.name",
			"CAST(COALESCE(n.cidr, '') AS TEXT)",
		).
		From("scans s").
		Join("sources src ON src.id = s.source_id").
		LeftJoin("networks n ON n.id = s.network_id").
		OrderBy("s.started_at DESC", "s.id DESC").
		Limit(pageLimit(arg.Limit))

	if arg.ScanKind != "" {
		sb = sb.Where(squirrel.Eq{"s.kind": arg.ScanKind})
	}

	if arg.ScanScope != "" {
		sb = sb.Where(squirrel.Eq{"s.scope": arg.ScanScope})
	}

	sb = arg.Cursor.WithValue(cursorTime(arg.Cursor)).Where(sb, "s.started_at", "s.id")

	rows, err := q.selectRows(ctx, sb)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	var items []*ListScansRow

	for rows.Next() {
		i := &ListScansRow{}

		if err := rows.Scan(
			&i.Scan.ID,
			&i.Scan.SourceID,
			&i.Scan.Kind,
			&i.Scan.Scope,
			&i.Scan.NetworkID,
			&i.Scan.Status,
			&i.Scan.Error,
			&i.Scan.FoundCount,
			&i.Scan.StartedAt,
			&i.Scan.FinishedAt,
			&i.SourceName,
			&i.NetworkCidr,
		); err != nil {
			return nil, err
		}

		items = append(items, i)
	}

	return items, closeRows(rows)
}

// pageLimit converts a row count for the query builder, which counts rows as
// unsigned. A direct conversion of a limit below one would wrap to a very
// large page. So pageLimit reads it as the empty page that it asks for.
func pageLimit(n int64) uint64 {
	if n < 1 {
		return 0
	}

	return uint64(n)
}

// selectRows renders the built query, and runs it through the same connection
// as the generated queries. So a page read in a transaction sees the
// transaction.
func (q *Queries) selectRows(ctx context.Context, sb squirrel.SelectBuilder) (*sql.Rows, error) {
	query, args, err := sb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	// A built query has no prepared statement. So pass it with a nil
	// statement. It runs directly against the connection or transaction.
	return q.query(ctx, nil, query, args...)
}

// cursorTime binds the timestamp of a cursor again, as the type of the column.
// It does not change a cursor that holds a different value. A zero cursor adds
// no clause.
func cursorTime(c cursor.Cursor) any {
	t, ok := c.Value.(time.Time)
	if !ok {
		return c.Value
	}

	return dbtype.NewTime(t)
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		return err
	}

	return rows.Close()
}
