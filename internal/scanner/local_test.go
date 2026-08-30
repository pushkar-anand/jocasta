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

func TestApplyHardware(t *testing.T) {
	t.Parallel()

	var (
		self     = netip.MustParseAddr("192.0.2.50")
		onLink   = netip.MustParseAddr("192.0.2.30")
		routed   = netip.MustParseAddr("198.51.100.7")
		loopback = netip.MustParseAddr("127.0.0.1")
	)

	hosts := []Host{{Addr: self}, {Addr: onLink}, {Addr: routed}, {Addr: loopback}}

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

	applyHardware(hosts, table, local)

	assert.Equal(t, "00:00:5e:00:53:32", hosts[0].MAC, "local interface must win over the neighbour table")
	assert.True(t, hosts[0].Self)
	assert.Equal(t, "eth0", hosts[0].Interface)

	assert.Equal(t, "00:00:5e:00:53:1e", hosts[1].MAC)
	assert.False(t, hosts[1].Self)
	assert.Empty(t, hosts[1].Interface)

	assert.Empty(t, hosts[2].MAC, "a routed address has no neighbour entry")
	assert.False(t, hosts[2].Self)

	// An interface with no hardware address is still ours, and must not be
	// left looking like somebody else's host.
	assert.True(t, hosts[3].Self)
	assert.Equal(t, "lo", hosts[3].Interface)
	assert.Empty(t, hosts[3].MAC)
}
