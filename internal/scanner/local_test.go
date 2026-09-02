package scanner

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalAddrs checks the kernel is readable and reports loopback, which
// every host has whatever else it is configured with.
func TestLocalAddrs(t *testing.T) {
	t.Parallel()

	got, err := localAddrs()
	require.NoError(t, err)
	require.NotEmpty(t, got)

	var loopback bool

	for addr, iface := range got {
		if addr.IsLoopback() {
			loopback = true

			assert.NotEmpty(t, iface.Name, "loopback reported without an interface name")
		}

		if iface.MAC != "" {
			require.Len(t, iface.MAC, 17, "MAC %q is not in colon form", iface.MAC)
			assert.Equal(t, strings.ToLower(iface.MAC), iface.MAC, "MAC %q was not normalised", iface.MAC)
		}
	}

	assert.True(t, loopback, "no loopback address found")
}

func TestHardwareFor(t *testing.T) {
	t.Parallel()

	var (
		self     = netip.MustParseAddr("192.0.2.50")
		onLink   = netip.MustParseAddr("192.0.2.30")
		routed   = netip.MustParseAddr("198.51.100.7")
		loopback = netip.MustParseAddr("127.0.0.1")
	)

	table := map[netip.Addr]string{
		onLink: "00:00:5e:00:53:1e",
		// The neighbour table can still carry a stale entry for a local
		// address; the kernel's own view of the interface must win.
		self: "00:00:5e:00:53:ff",
	}

	local := map[netip.Addr]localInterface{
		self:     {MAC: "00:00:5e:00:53:32", Name: "eth0"},
		loopback: {MAC: "", Name: "lo"},
	}

	tests := []struct {
		name  string
		addr  netip.Addr
		mac   string
		iface string
		self  bool
	}{
		{
			name:  "local interface wins over the neighbour table",
			addr:  self,
			mac:   "00:00:5e:00:53:32",
			iface: "eth0",
			self:  true,
		},
		{
			name: "an on-link address takes the neighbour table entry",
			addr: onLink,
			mac:  "00:00:5e:00:53:1e",
		},
		{
			name: "a routed address has no neighbour entry",
			addr: routed,
		},
		{
			// An interface with no hardware address is still ours, and must
			// not be left looking like somebody else's host.
			name:  "an interface without a hardware address is still ours",
			addr:  loopback,
			iface: "lo",
			self:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mac, iface, self := hardwareFor(tt.addr, table, local)

			assert.Equal(t, tt.mac, mac)
			assert.Equal(t, tt.iface, iface)
			assert.Equal(t, tt.self, self)
		})
	}
}
