package cursor

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoundTripsEveryKind(t *testing.T) {
	t.Parallel()

	// Truncated to the precision RFC3339Nano keeps, so the value read back
	// compares equal to the one written.
	at := time.Date(2026, 8, 31, 11, 26, 19, 360_000_000, time.UTC)

	tests := []struct {
		name string
		in   Cursor
	}{
		{"time", Cursor{Value: at, ID: 3, Order: Desc}},
		{"string", Cursor{Value: "printer.local", ID: 7, Order: Asc}},
		{"int", Cursor{Value: int64(42), ID: 1, Order: Desc}},
		{"float", Cursor{Value: 1.5, ID: 9, Order: Asc}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			token, err := tc.in.Encode()
			require.NoError(t, err)
			require.NotEmpty(t, token)

			var got Cursor
			require.NoError(t, got.Decode(token))

			assert.Equal(t, tc.in.ID, got.ID)
			assert.Equal(t, tc.in.Order, got.Order)
			assert.Equal(t, tc.in.Value, got.Value)
		})
	}
}

// The token is what a client is handed, so it has to survive a query string and
// a JSON body without being escaped into something else.
func TestTokenIsURLAndJSONSafe(t *testing.T) {
	t.Parallel()

	c := Cursor{Value: "a value/with+padding=", ID: 3, Order: Desc}

	text, err := c.MarshalText()
	require.NoError(t, err)
	assert.NotContains(t, string(text), "/")
	assert.NotContains(t, string(text), "+")

	var fromText Cursor
	require.NoError(t, fromText.UnmarshalText(text))
	assert.Equal(t, c, fromText)

	encoded, err := c.MarshalJSON()
	require.NoError(t, err)

	var fromJSON Cursor
	require.NoError(t, fromJSON.UnmarshalJSON(encoded))
	assert.Equal(t, c, fromJSON)
}

// A separator inside a string value must not split the token, which is why the
// separator is more than one character.
func TestValueMayContainASeparatorCharacter(t *testing.T) {
	t.Parallel()

	c := Cursor{Value: "a:b::c", ID: 2, Order: Asc}

	token, err := c.Encode()
	require.NoError(t, err)

	var got Cursor
	require.NoError(t, got.Decode(token))
	assert.Equal(t, "a:b::c", got.Value)
}

func TestZeroCursorHasNoToken(t *testing.T) {
	t.Parallel()

	var c Cursor

	assert.True(t, c.IsZero())

	token, err := c.Encode()
	require.NoError(t, err)
	assert.Empty(t, token)

	encoded, err := c.MarshalJSON()
	require.NoError(t, err)
	assert.JSONEq(t, "null", string(encoded))
}

// An empty token is the first page rather than a failure, so a client need not
// leave the parameter off to ask for it.
func TestEmptyTokenDecodesToZero(t *testing.T) {
	t.Parallel()

	c := Cursor{Value: "set", ID: 1, Order: Asc}
	require.NoError(t, c.Decode(""))
	assert.True(t, c.IsZero())
}

func TestRejectsMalformedTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{"not base64", "!!!!", ErrMalformed},
		{"too few parts", base64.URLEncoding.EncodeToString([]byte("DESC:::time:::2026-08-31T11:26:19Z")), ErrMalformed},
		{"unknown order", base64.URLEncoding.EncodeToString([]byte("SIDEWAYS:::int:::1:::2")), ErrOrder},
		{"unknown kind", base64.URLEncoding.EncodeToString([]byte("DESC:::colour:::red:::2")), ErrKind},
		{"value not of its kind", base64.URLEncoding.EncodeToString([]byte("DESC:::int:::red:::2")), ErrMalformed},
		{"id not a number", base64.URLEncoding.EncodeToString([]byte("DESC:::int:::1:::red")), ErrMalformed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var c Cursor
			assert.ErrorIs(t, c.Decode(tc.token), tc.want)
		})
	}
}

func TestUnsortableValueIsRefused(t *testing.T) {
	t.Parallel()

	_, err := Cursor{Value: struct{}{}, ID: 1, Order: Desc}.Encode()
	assert.ErrorIs(t, err, ErrUnsortableValue)
}

// The clause has to admit exactly the rows past the cursor: the ones the sort
// column already separates, and the ones it ties that the id separates.
func TestWhereSeeksPastTheRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		order Order
		want  string
	}{
		{"descending", Desc, "WHERE at <= ? AND (at < ? OR id < ?)"},
		{"ascending", Asc, "WHERE at >= ? AND (at > ? OR id > ?)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := Cursor{Value: 10, ID: 3, Order: tc.order}

			sql, args, err := c.Where(squirrel.Select("id").From("t"), "at", "id").ToSql()
			require.NoError(t, err)

			assert.Contains(t, sql, tc.want)
			assert.Equal(t, []any{10, 10, int64(3)}, args)
		})
	}
}

// The first page has nothing to seek past, so the query is left alone rather
// than given a clause that admits everything.
func TestWhereAddsNothingForTheZeroCursor(t *testing.T) {
	t.Parallel()

	var c Cursor

	sql, args, err := c.Where(squirrel.Select("id").From("t"), "at", "id").ToSql()
	require.NoError(t, err)

	assert.NotContains(t, sql, "WHERE")
	assert.Empty(t, args)
}

// WithValue is how a caller hands the query a value that renders itself the way
// its column stores it.
func TestWithValueReplacesOnlyTheValue(t *testing.T) {
	t.Parallel()

	c := Cursor{Value: "before", ID: 4, Order: Asc}
	got := c.WithValue("after")

	assert.Equal(t, "after", got.Value)
	assert.Equal(t, int64(4), got.ID)
	assert.Equal(t, Asc, got.Order)
	assert.Equal(t, "before", c.Value, "the original is untouched")
}
