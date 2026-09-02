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
