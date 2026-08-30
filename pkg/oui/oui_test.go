package oui_test

import (
	"net"
	"testing"

	"github.com/pushkar-anand/jocasta/pkg/oui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustMAC(t *testing.T, s string) net.HardwareAddr {
	t.Helper()

	hw, err := net.ParseMAC(s)
	require.NoError(t, err)

	return hw
}

func TestLookupResolvesRegisteredPrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		mac   string
		short string
		bits  int
	}{
		{"24-bit assignment", "00:00:0c:11:22:33", "Cisco", 24},
		{"lowercase input", "3c:22:fb:aa:bb:cc", "Apple", 24},
		{"uppercase input", "3C:22:FB:AA:BB:CC", "Apple", 24},
		{"hyphen separated", "3C-22-FB-AA-BB-CC", "Apple", 24},
		// A 36-bit assignment inside a block registered to the IEEE itself.
		// Matching the 24-bit parent would report the registry, not the vendor.
		{"36-bit assignment", "00:1b:c5:00:00:01", "Converging", 36},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v, ok := oui.Lookup(mustMAC(t, tt.mac))
			require.True(t, ok)
			assert.Equal(t, tt.short, v.Short)
			assert.Equal(t, tt.bits, v.Bits)
		})
	}
}

func TestLookupPrefersTheMostSpecificAssignment(t *testing.T) {
	t.Parallel()

	parent, ok := oui.Lookup(mustMAC(t, "00:1b:c5:ff:ff:ff"))
	require.True(t, ok)
	assert.Equal(t, 24, parent.Bits)

	child, ok := oui.Lookup(mustMAC(t, "00:1b:c5:00:00:01"))
	require.True(t, ok)
	assert.Equal(t, 36, child.Bits)

	assert.NotEqual(t, parent.Name, child.Name,
		"the specific assignment must not resolve to its parent block")
}

func TestLookupReportsMissForUnregisteredAndShortInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hw   net.HardwareAddr
	}{
		{"nil", nil},
		{"empty", net.HardwareAddr{}},
		// Four bytes cannot decide a 36-bit match.
		{"too short", net.HardwareAddr{0x00, 0x00, 0x0c, 0x11}},
		// A randomised address matches no registry by construction.
		{"locally administered", mustMACAddr("02:00:5e:10:00:01")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, ok := oui.Lookup(tt.hw)
			assert.False(t, ok)
		})
	}
}

func TestShortFallsBackToTheRegisteredName(t *testing.T) {
	t.Parallel()

	// No abbreviation is published for this assignment.
	v, ok := oui.Lookup(mustMAC(t, "00:00:17:11:22:33"))
	require.True(t, ok)
	assert.Equal(t, "Oracle", v.Name)
	assert.Equal(t, v.Name, v.Short)
}

func TestShortIgnoresAPrefixMasqueradingAsAName(t *testing.T) {
	t.Parallel()

	// The upstream abbreviation for this block is the prefix itself, which
	// would be worse to display than the registered name.
	v, ok := oui.Lookup(mustMAC(t, "00:00:00:11:22:33"))
	require.True(t, ok)
	assert.Equal(t, v.Name, v.Short)
	assert.NotContains(t, v.Short, ":")
}

func TestIsLocallyAdministered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mac  string
		want bool
	}{
		{"globally unique", "00:00:0c:11:22:33", false},
		{"randomised client", "02:00:5e:10:00:01", true},
		{"documentation address", "00:00:5e:00:53:01", false},
		{"second bit of first octet only", "06:11:22:33:44:55", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, oui.IsLocallyAdministered(mustMAC(t, tt.mac)))
		})
	}

	assert.False(t, oui.IsLocallyAdministered(nil), "an absent address is not locally administered")
}

func mustMACAddr(s string) net.HardwareAddr {
	hw, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}

	return hw
}

func BenchmarkLookup(b *testing.B) {
	hw, err := net.ParseMAC("00:1b:c5:00:00:01")
	if err != nil {
		b.Fatal(err)
	}

	// Warm the table so the benchmark measures lookups, not the one-off parse.
	oui.Lookup(hw)

	b.ReportAllocs()

	for b.Loop() {
		oui.Lookup(hw)
	}
}
