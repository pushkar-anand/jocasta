package localnet

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ipNet(addr string, ones int) *net.IPNet {
	return &net.IPNet{IP: net.ParseIP(addr), Mask: net.CIDRMask(ones, 32)}
}

func TestPrefixOf(t *testing.T) {
	t.Parallel()

	addrs := []net.Addr{
		ipNet("127.0.0.1", 8),
		ipNet("198.51.100.7", 22),
		ipNet("192.0.2.20", 24),
	}

	tests := []struct {
		name string
		addr string
		want string
	}{
		{"masks the address to its network", "192.0.2.20", "192.0.2.0/24"},
		{"picks the interface that has the address", "198.51.100.7", "198.51.100.0/22"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := prefixOf(netip.MustParseAddr(tt.addr), addrs)
			require.NoError(t, err)
			assert.Equal(t, netip.MustParsePrefix(tt.want), got)
		})
	}
}

func TestPrefixOfNoInterface(t *testing.T) {
	t.Parallel()

	_, err := prefixOf(netip.MustParseAddr("203.0.113.5"), []net.Addr{ipNet("192.0.2.20", 24)})
	require.Error(t, err)
}
