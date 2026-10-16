package inventory

import (
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/cursor"
)

// Cursor marks the row on which a page of a log ended. It is the type of
// pkg/cursor, with a name here. So a caller of the inventory needs nothing
// from the database layer.
type Cursor = cursor.Cursor

// DefaultPageSize is the page size for a caller that names no limit.
// MaxPageSize is the largest page that a caller can ask for. The API and the
// MCP tools both page the event and scan logs by these.
const (
	DefaultPageSize = 50
	MaxPageSize     = 500
)

// Page is one window of a log: at most Limit rows, after Cursor.
type Page struct {
	Limit int

	// Cursor is where to continue. The zero cursor starts at the top of the
	// log.
	Cursor Cursor

	// Device limits the change log to the entries of one device. Zero is every
	// device, as the full log shows. Only ListEvents reads it.
	Device int64

	// EventKinds limits the change log to these kinds. Empty is every kind.
	// Only ListEvents reads it.
	EventKinds []dbtype.EventKind

	// ExcludeIgnored leaves out the events of ignored devices. Only ListEvents
	// reads it. The full log includes their history on purpose.
	ExcludeIgnored bool

	// ScanKind limits the scan history to one kind. Empty is every kind. Only
	// ListScans reads it.
	ScanKind dbtype.ScanKind

	// ScanScope limits the scan history to one scope. Empty is every scope.
	// Only ListScans reads it.
	ScanScope dbtype.ScanScope
}

// EventPage is one window of the change log.
type EventPage struct {
	Events []*Event

	// Next is the next window. It is the zero cursor when this window reached
	// the end of the log.
	Next Cursor
}

// ScanPage is one window of the scan history.
type ScanPage struct {
	Scans []*Scan
	Next  Cursor
}

// seek is how many rows to ask for. This is one more than the page holds. So
// the same read answers whether another page exists. A separate count could
// disagree with it.
func (p Page) seek() int64 { return int64(p.Limit) + 1 }

// trim removes the extra row, and reports where the next page starts. Fewer
// rows than asked for mean that the log ended.
func trim[T any](rows []T, limit int, at func(T) Cursor) ([]T, Cursor) {
	if limit < 1 || len(rows) <= limit {
		return rows, Cursor{}
	}

	rows = rows[:limit]

	return rows, at(rows[limit-1])
}
