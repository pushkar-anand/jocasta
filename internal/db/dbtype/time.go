package dbtype

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Layout is the format of a stored timestamp. It matches the strftime
// expression of the column defaults.
//
// Timestamps are TEXT, and SQLite compares them as TEXT. So every writer must
// render them with the same fixed width. This package exists for that reason.
// The driver alone stores a time.Time in the String form of Go. Its ' '
// separator sorts before the 'T' of every value from the defaults.
//
// The zeros keep the trailing digits, and this is their purpose.
// time.RFC3339 has no fraction. The nines of time.RFC3339Nano remove trailing
// zeros. So a value on a whole second renders short, and sorts before every
// fractional value in the same second. Milliseconds are the finest
// precision, because %f of strftime is the widest that SQLite offers.
const Layout = "2006-01-02T15:04:05.000Z"

// Time is a timestamp column.
type Time struct {
	time.Time
}

// NewTime returns t as a timestamp, truncated to the precision of the column.
// So a value that you write and read back is equal to the value in memory.
func NewTime(t time.Time) Time {
	return Time{t.UTC().Truncate(time.Millisecond)}
}

// Now returns the current time as a timestamp.
func Now() Time {
	return NewTime(time.Now())
}

// Value takes a value receiver and Scan takes a pointer, as [sql.NullTime] and
// the other database/sql null types do. Scan must change the value. Value must
// be in the method set of a Time held by value. Generated code gives a Time to
// the driver in this way.
func (t Time) Value() (driver.Value, error) {
	return t.UTC().Format(Layout), nil
}

// Scan reads a stored timestamp into t.
func (t *Time) Scan(src any) error {
	parsed, err := parseTime(src)
	if err != nil {
		return err
	}

	t.Time = parsed

	return nil
}

// NullTime is a Time for a column that can be null. [sql.NullTime] cannot
// replace it. Its Scan uses convertAssign, which cannot convert the string
// from a TEXT column. Its Value returns a bare time.Time, and the driver
// renders it in any form.
type NullTime struct {
	Time  Time
	Valid bool
}

// NewNullTime returns t as a non-null timestamp.
func NewNullTime(t time.Time) NullTime {
	return NullTime{Time: NewTime(t), Valid: true}
}

// Value renders n for the driver. It stores an invalid value as null.
func (n NullTime) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}

	return n.Time.Value()
}

// Scan reads a stored timestamp into n. It reads null as invalid.
func (n *NullTime) Scan(src any) error {
	if src == nil {
		n.Time, n.Valid = Time{}, false

		return nil
	}

	parsed, err := parseTime(src)
	if err != nil {
		return err
	}

	n.Time, n.Valid = Time{parsed}, true

	return nil
}

// parseTime accepts only Layout. An error is better than a value that reads but
// sorts in the wrong order against its neighbours. time.Parse also accepts a
// fractional second that the layout does not include. This is the one
// exception, and it is safe, because the instant is still correct.
func parseTime(src any) (time.Time, error) {
	// This runs only if a column has a date affinity. Then the driver parses
	// the value before this code sees it.
	if v, ok := src.(time.Time); ok {
		return v.UTC(), nil
	}

	s, err := text(src)
	if err != nil {
		return time.Time{}, err
	}

	t, err := time.Parse(Layout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("dbtype: parse %q: %w", s, err)
	}

	return t, nil
}
