package dbtype

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	sample   = time.Date(2026, 8, 31, 6, 20, 21, 93_000_000, time.UTC)
	rendered = "2026-08-31T06:20:21.093Z"
)

func TestValueIsTheSchemaFormat(t *testing.T) {
	t.Parallel()

	v, err := NewTime(sample).Value()
	require.NoError(t, err)
	assert.Equal(t, rendered, v)
}

// Comparison is lexicographic, so every value has to render to the same width
// whatever the clock hands over.
func TestValueIsFixedWidth(t *testing.T) {
	t.Parallel()

	for _, in := range []time.Time{
		sample,
		// Exactly on the second, and a fraction ending in zeros: the two cases
		// a layout built from nines renders short.
		sample.Truncate(time.Second),
		sample.Truncate(time.Second).Add(500 * time.Millisecond),
		time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC),
		time.Date(999, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		v, err := NewTime(in).Value()
		require.NoError(t, err)

		s, ok := v.(string)
		require.True(t, ok)
		assert.Len(t, s, len(rendered), "rendered %q", s)
		assert.Equal(t, byte('Z'), s[len(s)-1])
	}
}

func TestNewNormalisesToUTCMilliseconds(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("IST", 5*3600+1800)
	local := sample.In(zone).Add(900 * time.Microsecond)

	got := NewTime(local)

	assert.Equal(t, time.UTC, got.Location())
	assert.True(t, got.Equal(sample), "precision the column cannot hold was kept: %s", got)
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	v, err := NewTime(sample).Value()
	require.NoError(t, err)

	var got Time
	require.NoError(t, got.Scan(v))
	assert.True(t, got.Equal(sample), "got %s", got)
}

func TestScanAcceptsBytes(t *testing.T) {
	t.Parallel()

	var got Time
	require.NoError(t, got.Scan([]byte(rendered)))
	assert.True(t, got.Equal(sample))
}

// Ordering is what the fixed width buys, so a rendering that drops the
// fraction is not a shorter spelling of the same value, it is one that sorts
// wrongly against its neighbours.
func TestScanRejectsAVariableWidthRendering(t *testing.T) {
	t.Parallel()

	var got Time
	assert.Error(t, got.Scan(sample.Format(time.RFC3339)))
	assert.Error(t, got.Scan(sample.Truncate(time.Second).Format(time.RFC3339Nano)))
}

// One accepted rendering is one sort order. Anything else has to fail loudly
// rather than read back as a value that orders wrongly against its neighbours.
func TestScanRejectsOtherFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   any
	}{
		{"sqlite CURRENT_TIMESTAMP", "2026-08-31 06:20:21"},
		{"go String", "2026-08-31 06:20:21.093 +0000 UTC"},
		{"date only", "2026-08-31"},
		{"no fraction", "2026-08-31T06:20:21Z"},
		{"empty", ""},
		{"not a time", "yesterday"},
		{"wrong type", 1756621221},
		{"null into a non-null column", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got Time
			assert.Error(t, got.Scan(tt.in))
		})
	}
}

func TestNullTime(t *testing.T) {
	t.Parallel()

	var n NullTime
	require.NoError(t, n.Scan(nil))
	assert.False(t, n.Valid)

	v, err := n.Value()
	require.NoError(t, err)
	assert.Nil(t, v)

	require.NoError(t, n.Scan(rendered))
	assert.True(t, n.Valid)
	assert.True(t, n.Time.Equal(sample))

	v, err = NewNullTime(sample).Value()
	require.NoError(t, err)
	assert.Equal(t, rendered, v)
}

func TestNullTimeRejectsOtherFormats(t *testing.T) {
	t.Parallel()

	var n NullTime
	require.Error(t, n.Scan("2026-08-31 06:20:21"))
}
