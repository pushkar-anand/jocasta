package dbtype

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddrValue(t *testing.T) {
	t.Parallel()

	v, err := NewAddr(netip.MustParseAddr("192.0.2.10")).Value()
	require.NoError(t, err)
	assert.Equal(t, "192.0.2.10", v)
}

// A UNIQUE constraint over a spelling is only worth what the spelling is, so
// the forms that name one address have to reach the column as one string.
func TestAddrCanonicalises(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"4-in-6", "::ffff:192.0.2.10", "192.0.2.10"},
		{"expanded v6", "2001:0db8:0000:0000:0000:0000:0000:0001", "2001:db8::1"},
		{"uppercase v6", "2001:DB8::1", "2001:db8::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v, err := NewAddr(netip.MustParseAddr(tt.in)).Value()
			require.NoError(t, err)
			assert.Equal(t, tt.want, v)

			var scanned Addr
			require.NoError(t, scanned.Scan(tt.in))

			back, err := scanned.Value()
			require.NoError(t, err)
			assert.Equal(t, tt.want, back, "scanning did not canonicalise the way writing does")
		})
	}
}

func TestAddrRejectsInvalid(t *testing.T) {
	t.Parallel()

	_, err := Addr{}.Value()
	assert.Error(t, err, "the zero address is not a value any row should hold")

	var a Addr
	assert.Error(t, a.Scan("not-an-address"))
	assert.Error(t, a.Scan(42))
}

func TestPrefixMasksToItsBase(t *testing.T) {
	t.Parallel()

	// A host address inside the network names the same network, and the column
	// is UNIQUE, so it has to arrive as the base either way.
	v, err := NewPrefix(netip.MustParsePrefix("192.0.2.5/24")).Value()
	require.NoError(t, err)
	assert.Equal(t, "192.0.2.0/24", v)

	var p Prefix
	require.NoError(t, p.Scan("192.0.2.5/24"))

	back, err := p.Value()
	require.NoError(t, err)
	assert.Equal(t, "192.0.2.0/24", back)
}

func TestPrefixRejectsInvalid(t *testing.T) {
	t.Parallel()

	_, err := Prefix{}.Value()
	assert.Error(t, err)

	var p Prefix
	assert.Error(t, p.Scan("192.0.2.0"))
}

func TestParseMAC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already canonical", "00:00:5e:00:53:01", "00:00:5e:00:53:01"},
		{"uppercase", "00:00:5E:00:53:01", "00:00:5e:00:53:01"},
		{"hyphenated", "00-00-5E-00-53-01", "00:00:5e:00:53:01"},
		{"dotted", "0000.5e00.5301", "00:00:5e:00:53:01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := ParseMAC(tt.in)
			require.NoError(t, err)
			assert.True(t, m.Valid())

			v, err := m.Value()
			require.NoError(t, err)
			assert.Equal(t, tt.want, v)
		})
	}
}

func TestParseMACRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"not a mac", "not-a-mac"},
		// The column holds 6-byte addresses; anything wider has nowhere to go.
		{"eui-64", "00:00:5e:00:53:01:02:03"},
		{"infiniband", "00:00:5e:00:53:01:02:03:04:05:06:07:08:09:0a:0b:0c:0d:0e:0f"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := ParseMAC(tt.in)
			require.Error(t, err)
			assert.False(t, m.Valid())
		})
	}
}

// The zero MAC is the null one: a device has no hardware address until
// something learns it, and that needs no separate type.
func TestMACZeroValueIsNull(t *testing.T) {
	t.Parallel()

	v, err := MAC{}.Value()
	require.NoError(t, err)
	assert.Nil(t, v)

	var m MAC
	require.NoError(t, m.Scan(nil))
	assert.False(t, m.Valid())

	require.NoError(t, m.Scan("00:00:5e:00:53:01"))
	assert.True(t, m.Valid())

	require.Error(t, m.Scan("nonsense"))
}
