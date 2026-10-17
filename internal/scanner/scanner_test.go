package scanner

import (
	"log/slog"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests of pkg/cidr cover the address list itself. This file covers the
// sweep limit, which is a policy of this scanner.
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
