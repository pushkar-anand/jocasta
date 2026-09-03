// Package cursor pages a sorted query by remembering the row the last page
// ended on, rather than by counting rows to skip.
//
// A log is written while it is being read, and an offset counts from the top of
// a list the next insert has already shifted: a client walking one page at a
// time sees the row on the boundary twice, and the row after it not at all. A
// cursor names a row instead, so the page after it is the same page whatever
// arrived in the meantime.
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

// Kind names the type of the sort column's value, so that Decode can parse back
// what Encode wrote without being told which query the token came from.
type Kind string

// Kind values the sort column's underlying type.
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

// Errors returned by Encode and Decode when a request asks for something a
// cursor cannot represent.
var (
	// ErrUnsortableValue is a cursor Value of a type Encode has no rendering
	// for.
	ErrUnsortableValue = errors.New("cursor: value cannot be sorted")

	// ErrMalformed is a token that is not base64 of the expected four parts,
	// or whose value does not parse as the kind it names.
	ErrMalformed = errors.New("cursor: malformed")

	// ErrOrder is a token whose order field is neither ASC nor DESC.
	ErrOrder = errors.New("cursor: unknown order")

	// ErrKind is a token whose kind field names no known column type.
	ErrKind = errors.New("cursor: unknown value kind")
)

// separator splits the parts of a decoded token. It is three characters so that
// a string sort column carrying one character of it does not split the token.
const separator = ":::"

// Cursor marks the row a page ended on.
//
// It carries the whole sort key, not just the row's id: a query ordered by a
// column that is not unique -- a timestamp several rows share -- cannot resume
// from the value alone, and one ordered by a column that is unique still needs
// the tie broken when it is not.
//
// The token a client sees is base64 of
// <order>:::<kind>:::<value>:::<id>, which is opaque enough to say that
// taking it apart means depending on an ordering that is the server's to
// change. Cursor marshals to and from that token as both text and JSON, so a
// request struct can decode one straight out of a query string and a response
// can carry one back without either naming the encoding.
type Cursor struct {
	// Value is the sort column's value in the row the page ended on.
	Value any

	// ID breaks the tie when Value does not identify one row. It is the row's
	// auto-incrementing key.
	ID int64

	// Order is the direction the query sorts in, and so which side of Value
	// the next page lies on.
	Order Order
}

// IsZero reports whether c marks no row, which is what starts at the top.
func (c Cursor) IsZero() bool { return c.Value == nil }

// WithValue returns a copy of c holding v, for handing the query a value that
// renders itself the way its column stores it. A timestamp kept as text has to
// be compared in the format it was written in, which the bare time.Time that
// Decode produces does not know about.
func (c Cursor) WithValue(v any) Cursor {
	c.Value = v

	return c
}

// Encode renders the token. The zero cursor has no token: there is no page
// after the end of a list.
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

// Decode reads a token back. An empty token leaves the cursor zero, so a client
// asking for the first page need not leave the parameter off.
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

// UnmarshalText implements encoding.TextUnmarshaler, which is what lets a query
// string decode into a Cursor field without the handler unpacking it.
func (c *Cursor) UnmarshalText(text []byte) error {
	return c.Decode(strings.Trim(string(text), `"`))
}

// MarshalJSON implements json.Marshaler, rendering the token as a string and
// the zero cursor as null.
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

// Where adds the seek to a query: the rows lying past the one c marks, in the
// order c was taken in. A zero cursor adds nothing, which is the first page.
//
// The comparison is written as a bound on the sort column and then the pair
// that breaks its ties, rather than as a row-value comparison, so that an index
// on the sort column still answers it.
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
