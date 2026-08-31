// Package models holds the query helpers backed by the generated SQL code.
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

// The two logs are paged by seeking past the row the last page ended on, which
// makes the WHERE clause a thing that is there or is not. sqlc has no shape for
// that: a nullable cursor parameter is typed as an interface{}, which would
// take a timestamp rendered in any format at all, and the alternative is the
// same SELECT written twice. They are built here instead, and everything whose
// shape is fixed stays in queries/inventory.sql.

// PageParams is one window onto a log.
type PageParams struct {
	// Cursor is the row to resume after. The zero cursor starts at the top.
	Cursor cursor.Cursor

	// Limit is how many rows to read.
	Limit int64
}

// ListEventsRow is one entry of the change log with the device it named.
//
// The device columns come from a LEFT JOIN because an event outlives the device
// it described: deleting one sets events.device_id to NULL rather than taking
// the record with it.
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

	// The cursor's timestamp is bound as a dbtype.Time so that it renders in
	// the one format the column is written in: occurred_at is TEXT and is
	// compared as TEXT, so a value spelled any other way compares wrong rather
	// than failing.
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
// network_cidr is cast and coalesced rather than selected as itself: the column
// override types it as a non-null Prefix, which cannot scan the NULL a scan
// with no network -- or one whose network was deleted -- produces here.
type ListScansRow struct {
	Scan        Scan   `json:"scan"`
	SourceName  string `json:"source_name"`
	NetworkCidr string `json:"network_cidr"`
}

// ListScans returns one page of the scan history, most recent first.
func (q *Queries) ListScans(ctx context.Context, arg PageParams) ([]*ListScansRow, error) {
	sb := squirrel.
		Select(
			"s.id", "s.source_id", "s.kind", "s.network_id", "s.status",
			"s.error", "s.found_count", "s.started_at", "s.finished_at",
			"src.name",
			"CAST(COALESCE(n.cidr, '') AS TEXT)",
		).
		From("scans s").
		Join("sources src ON src.id = s.source_id").
		LeftJoin("networks n ON n.id = s.network_id").
		OrderBy("s.started_at DESC", "s.id DESC").
		Limit(pageLimit(arg.Limit))

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
// unsigned. A limit below one is not a smaller page but a wrapped enormous one,
// so it is read as the empty page it was asking for.
func pageLimit(n int64) uint64 {
	if n < 1 {
		return 0
	}

	return uint64(n)
}

// selectRows renders the built query and runs it through the same connection
// the generated queries use, so a page read inside a transaction sees it.
func (q *Queries) selectRows(ctx context.Context, sb squirrel.SelectBuilder) (*sql.Rows, error) {
	query, args, err := sb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	// A built query has no prepared statement to reuse, so it is passed with a
	// nil one and runs against the connection or transaction directly.
	return q.query(ctx, nil, query, args...)
}

// cursorTime re-binds a cursor's timestamp as the column's own type. A cursor
// that holds anything else is left alone, and a zero one adds no clause at all.
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
