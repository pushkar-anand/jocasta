package scanner

import (
	"log/slog"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Address enumeration itself is covered in pkg/cidr. What belongs here is the
// sweep cap, which is this scanner's policy rather than a property of a prefix.
func TestScanRejectsPrefixOverCap(t *testing.T) {
	t.Parallel()

	s := New(slog.New(slog.DiscardHandler))

	_, err := s.Scan(t.Context(), netip.MustParsePrefix("10.0.0.0/15"))

	require.ErrorIs(t, err, ErrPrefixTooLarge)
}

func TestScanRejectsIPv6(t *testing.T) {
	t.Parallel()

	s := New(slog.New(slog.DiscardHandler))

	_, err := s.Scan(t.Context(), netip.MustParsePrefix("2001:db8::/120"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not IPv4")
}

func TestApplyVendor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mac        string
		vendor     string
		randomised bool
	}{
		{
			name:   "registered prefix resolves to its vendor",
			mac:    "00:00:0c:11:22:33",
			vendor: "Cisco",
		},
		{
			// The randomised addresses that current phones present belong to
			// no vendor, so no table will ever name one.
			name:       "locally administered address is marked randomised",
			mac:        "02:00:5e:10:00:01",
			randomised: true,
		},
		{
			// Unassigned by IEEE. The RFC 7042 documentation range is not a
			// substitute here: 00:00:5E is registered to IANA and resolves.
			name: "unregistered prefix yields nothing",
			mac:  "00:08:33:11:22:33",
		},
		{
			name: "absent address yields nothing",
			mac:  "",
		},
		{
			name: "unparseable address yields nothing",
			mac:  "not-a-mac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := Host{MAC: tt.mac}
			applyVendor(&h)

			assert.Equal(t, tt.vendor, h.Vendor)
			assert.Equal(t, tt.randomised, h.Randomised)
		})
	}
}
