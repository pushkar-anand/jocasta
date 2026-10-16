// Package cursor pages a sorted query. It remembers the row on which the last
// page ended.
//
// Jocasta writes to a log while clients read it. An offset counts from the top
// of the list, and each insert moves the rows down. So a client that reads one
// page at a time sees the row on the boundary twice, and misses the next row.
// A cursor names a row. So the next page is the same, even when new rows
// arrive.
package cursor

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
)

// Order is the direction a query sorts in.
type Order string

// Asc and Desc are the two directions a query can sort in.
const (
	Asc  Order = "ASC"
	Desc Order = "DESC"
)

// Valid reports whether o is one of the known directions.
func (o Order) Valid() bool { return o == Asc || o == Desc }

// Kind names the type of the value in the sort column. With it, Decode can
// parse what Encode wrote without knowing the query of the token.
type Kind string

// The kinds of value a sort column can hold.
const (
	KindString Kind = "string"
	KindInt    Kind = "int"
	KindFloat  Kind = "float"
	KindTime   Kind = "time"
)

// Valid reports whether k is one of the known column value types.
func (k Kind) Valid() bool {
	switch k {
	case KindString, KindInt, KindFloat, KindTime:
		return true
	}

	return false
}

// Encode and Decode return these errors when a request asks for something
// that a cursor cannot represent.
var (
	// ErrUnsortableValue reports a cursor Value of a type that Encode cannot
	// render.
	ErrUnsortableValue = errors.New("cursor: value cannot be sorted")

	// ErrMalformed is a token that is not base64 of the four expected parts.
	// It is also a token whose value does not parse as the kind it names.
	ErrMalformed = errors.New("cursor: malformed")

	// ErrOrder is a token whose order field is neither ASC nor DESC.
	ErrOrder = errors.New("cursor: unknown order")

	// ErrKind is a token whose kind field names no known column type.
	ErrKind = errors.New("cursor: unknown value kind")
)

// separator splits the parts of a decoded token. It has three characters. So
// if a string sort column contains one of them, the token does not split.
const separator = ":::"

// Cursor marks the row on which a page ended.
//
// It holds the whole sort key: the value of the sort column and the id of the
// row. Several rows can share a value in a sort column, such as a timestamp.
// A query cannot continue from the value alone, so the id breaks the tie.
//
// The token that a client sees is base64 of
// <order>:::<kind>:::<value>:::<id>. The base64 tells the client not to take
// the token apart. The server can change the order. Cursor marshals to and
// from the token as text and as JSON. So a request struct can decode it from
// a query string, and a response can return it. Neither needs to know the
// encoding.
type Cursor struct {
	// Value is the value of the sort column in the row on which the page
	// ended.
	Value any

	// ID breaks the tie when Value does not identify one row. It is the
	// auto-incrementing key of the row.
	ID int64

	// Order is the direction of the sort. It tells on which side of Value the
	// next page is.
	Order Order
}

// IsZero reports whether c marks no row. The zero cursor is the first page.
func (c Cursor) IsZero() bool { return c.Value == nil }

// WithValue returns a copy of c with the value v. Use it to give the query a
// value that renders in the same format as its column. A query must compare a
// text timestamp in the format of the column. The time.Time from Decode does
// not know that format.
func (c Cursor) WithValue(v any) Cursor {
	c.Value = v

	return c
}

// Encode renders the token. The zero cursor has no token, because there is no
// page after the end of a list.
func (c Cursor) Encode() (string, error) {
	if c.IsZero() {
		return "", nil
	}

	value, kind, err := encodeValue(c.Value)
	if err != nil {
		return "", err
	}

	order := c.Order
	if !order.Valid() {
		order = Desc
	}

	raw := strings.Join([]string{
		string(order), string(kind), value, strconv.FormatInt(c.ID, 10),
	}, separator)

	return base64.URLEncoding.EncodeToString([]byte(raw)), nil
}

// Decode reads a token. An empty token leaves the cursor zero. So a client
// that asks for the first page can send the parameter empty.
func (c *Cursor) Decode(token string) error {
	if token == "" {
		*c = Cursor{}

		return nil
	}

	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	parts := strings.Split(string(raw), separator)
	if len(parts) != 4 {
		return ErrMalformed
	}

	order := Order(parts[0])
	if !order.Valid() {
		return ErrOrder
	}

	kind := Kind(parts[1])
	if !kind.Valid() {
		return ErrKind
	}

	value, err := decodeValue(kind, parts[2])
	if err != nil {
		return err
	}

	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	c.Value = value
	c.ID = id
	c.Order = order

	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (c Cursor) MarshalText() ([]byte, error) {
	token, err := c.Encode()
	if err != nil {
		return nil, err
	}

	return []byte(token), nil
}

// UnmarshalText implements encoding.TextUnmarshaler. With it, a query string
// decodes into a Cursor field, and the handler does not unpack it.
func (c *Cursor) UnmarshalText(text []byte) error {
	return c.Decode(strings.Trim(string(text), `"`))
}

// MarshalJSON implements json.Marshaler. It renders the token as a string,
// and the zero cursor as null.
func (c Cursor) MarshalJSON() ([]byte, error) {
	token, err := c.Encode()
	if err != nil {
		return nil, err
	}

	if token == "" {
		return []byte("null"), nil
	}

	return []byte(strconv.Quote(token)), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (c *Cursor) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*c = Cursor{}

		return nil
	}

	token, err := strconv.Unquote(string(data))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	return c.Decode(token)
}

// Where adds the seek to a query. The seek selects the rows after the row
// that c marks, in the order of c. A zero cursor adds nothing, so the query
// returns the first page.
//
// The comparison is a bound on the sort column, then the pair that breaks
// ties. So an index on the sort column can still answer it.
func (c Cursor) Where(sb squirrel.SelectBuilder, valueColumn, idColumn string) squirrel.SelectBuilder {
	if c.IsZero() {
		return sb
	}

	if c.Order == Asc {
		return sb.
			Where(squirrel.GtOrEq{valueColumn: c.Value}).
			Where(squirrel.Or{
				squirrel.Gt{valueColumn: c.Value},
				squirrel.Gt{idColumn: c.ID},
			})
	}

	return sb.
		Where(squirrel.LtOrEq{valueColumn: c.Value}).
		Where(squirrel.Or{
			squirrel.Lt{valueColumn: c.Value},
			squirrel.Lt{idColumn: c.ID},
		})
}

// encodeValue returns the text and kind of a cursor value. For a type that it
// does not support, it returns an error that wraps ErrUnsortableValue.
func encodeValue(v any) (string, Kind, error) {
	switch t := v.(type) {
	case string:
		return t, KindString, nil
	case int:
		return strconv.Itoa(t), KindInt, nil
	case int64:
		return strconv.FormatInt(t, 10), KindInt, nil
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32), KindFloat, nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), KindFloat, nil
	case time.Time:
		return t.Format(time.RFC3339Nano), KindTime, nil
	default:
		return "", "", fmt.Errorf("%w: %T", ErrUnsortableValue, v)
	}
}

// decodeValue restores a cursor value, using int64 and float64 for numbers.
// Unknown kinds return ErrKind; parse failures wrap ErrMalformed.
func decodeValue(kind Kind, raw string) (any, error) {
	switch kind {
	case KindString:
		return raw, nil
	case KindInt:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
		}

		return v, nil
	case KindFloat:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
		}

		return v, nil
	case KindTime:
		v, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
		}

		return v, nil
	default:
		return nil, ErrKind
	}
}
