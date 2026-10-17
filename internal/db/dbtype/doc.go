// Package dbtype holds the column types of the SQLite schema. Each type sets
// how a value is written as text and read back.
//
// The schema keeps timestamps and addresses as TEXT, and compares them as
// TEXT. So a UNIQUE constraint, a partial index or an ORDER BY works only if
// every writer uses one spelling. The driver alone does not do this. It
// stores a [time.Time] in the String form of Go. It cannot convert a TEXT
// column into a [time.Time] at all.
//
// The enum columns store their values in upper case. So in query output, you
// can see at once which tokens the schema constrains. Go has no convention for
// the string in a constant. MixedCaps applies only to the identifier. So the
// identifiers stay MixedCaps, with upper-case values, as [net/http.MethodGet]
// does for "GET".
package dbtype
