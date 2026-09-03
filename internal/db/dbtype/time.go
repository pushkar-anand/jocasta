package dbtype

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Layout is the format the schema stores a timestamp in, matching the strftime
// expression behind the column defaults.
//
// Timestamps are TEXT and are compared as TEXT, so every writer has to render
// them the same fixed width. That is what this package is for: the driver left
// to itself stores a time.Time in Go's String form, whose ' ' separator sorts
// before the 'T' of every value the defaults produce.
//
// The zeros keep the trailing digits, which is the whole point: time.RFC3339
// carries no fraction at all, and time.RFC3339Nano's nines strip trailing
// zeros, so a value landing on a whole second renders short and sorts before
// every fractional value in the same second. Milliseconds are also as fine as
// this can go, since strftime's %f is the widest SQLite offers.
const Layout = "2006-01-02T15:04:05.000Z"

// Time is a timestamp column.
type Time struct {
	time.Time
}

// NewTime returns t as a timestamp, truncated to the precision the column keeps so
// a value written and read back compares equal to the one held in memory.
func NewTime(t time.Time) Time {
	return Time{t.UTC().Truncate(time.Millisecond)}
}

// Now returns the current time as a timestamp.
func Now() Time {
	return NewTime(time.Now())
}

// Value takes a value receiver and Scan a pointer, as [sql.NullTime] and the
// rest of the database/sql null types do: Scan has to mutate, while Value has
// to be in the method set of a Time held by value, which is how generated code
// hands one to the driver.
func (t Time) Value() (driver.Value, error) {
	return t.UTC().Format(Layout), nil
}

// Scan reads a stored timestamp back into t.
func (t *Time) Scan(src any) error {
	parsed, err := parseTime(src)
	if err != nil {
		return err
	}

	t.Time = parsed

	return nil
}

// NullTime is a Time for a column that may be null. [sql.NullTime] cannot stand
// in for it: its Scan defers to convertAssign, which has no conversion from the
// string a TEXT column yields, and its Value hands back a bare time.Time for
// the driver to render however it chooses.
type NullTime struct {
	Time  Time
	Valid bool
}

// NewNullTime returns t as a non-null timestamp.
func NewNullTime(t time.Time) NullTime {
	return NullTime{Time: NewTime(t), Valid: true}
}

// Value renders n for the driver, storing an invalid value as null.
func (n NullTime) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}

	return n.Time.Value()
}

// Scan reads a stored timestamp back into n, treating null as invalid.
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

// parseTime accepts only Layout, so a value that cannot be read is preferred to one
// that reads but orders wrongly against its neighbours. Note that time.Parse
// admits a fractional second the layout does not mention, which is the one
// widening of that rule and costs nothing: the instant is still correct.
func parseTime(src any) (time.Time, error) {
	// Reached only if a column is ever declared with a date affinity, which
	// makes the driver parse the value before this sees it.
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
