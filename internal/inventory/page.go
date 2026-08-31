package inventory

import (
	"github.com/pushkar-anand/jocasta/pkg/cursor"
)

// Cursor marks the row a page of a log ended on. It is pkg/cursor's, named here
// so that a caller reading the inventory needs nothing from the database layer.
type Cursor = cursor.Cursor

// Page is one window onto a log: at most Limit rows, starting after Cursor.
type Page struct {
	Limit int

	// Cursor is where to resume. The zero cursor starts at the top of the log.
	Cursor Cursor
}

// EventPage is one window onto the change log.
type EventPage struct {
	Events []*Event

	// Next is the window after this one, and is the zero cursor when this one
	// reached the end of the log.
	Next Cursor
}

// ScanPage is one window onto the scan history.
type ScanPage struct {
	Scans []*Scan
	Next  Cursor
}

// seek is how many rows to ask for: one more than the page holds, so that
// whether another page exists is answered by the same read rather than by a
// count that could disagree with it.
func (p Page) seek() int64 { return int64(p.Limit) + 1 }

// trim cuts the extra row off and reports where the next page resumes. Reading
// fewer rows than were asked for is what says the log ended.
func trim[T any](rows []T, limit int, at func(T) Cursor) ([]T, Cursor) {
	if limit < 1 || len(rows) <= limit {
		return rows, Cursor{}
	}

	rows = rows[:limit]

	return rows, at(rows[limit-1])
}
