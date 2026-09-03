// Package dbtype carries the column types the SQLite schema stores, each one
// controlling how a value is rendered to text and read back.
//
// The schema keeps timestamps and addresses as TEXT and compares them as TEXT,
// so what a UNIQUE constraint, a partial index or an ORDER BY is worth depends
// entirely on every writer agreeing on one spelling. Left to itself the driver
// does not: it stores a [time.Time] in Go's String form and has no conversion
// at all from a TEXT column into one.
//
// The enum columns store their values in upper case, so that query output
// tells a token the schema constrains apart from free text at a glance. Go has
// no convention for the string a constant holds -- MixedCaps governs the
// identifier -- so the identifiers stay MixedCaps over upper-case values, as
// [net/http.MethodGet] does for "GET".
package dbtype
