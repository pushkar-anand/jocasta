package scanner

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseARP(t *testing.T) {
	t.Parallel()

	const table = `IP address       HW type     Flags       HW address            Mask     Device
192.0.2.1        0x1         0x2         00:00:5E:00:53:01     *        eth0
192.0.2.30       0x1         0x2         00:00:5e:00:53:1e     *        eth0
192.0.2.77       0x1         0x0         00:00:00:00:00:00     *        eth0
192.0.2.99       0x1         0x2         00:00:00:00:00:00     *        eth0
192.0.2.44       0x1         0x2         00-00-5E-00-53-2C     *        eth0
192.0.2.45       0x1         0x2         nonsense              *        eth0
not-an-address   0x1         0x2         aa:bb:cc:dd:ee:ff     *        eth0
192.0.2.5
`

	got, err := parseARP(strings.NewReader(table))
	require.NoError(t, err)

	assert.Equal(t, map[netip.Addr]string{
		netip.MustParseAddr("192.0.2.1"):  "00:00:5e:00:53:01",
		netip.MustParseAddr("192.0.2.30"): "00:00:5e:00:53:1e",
		netip.MustParseAddr("192.0.2.44"): "00:00:5e:00:53:2c",
	}, got, "only complete entries with a parseable, non-zero MAC belong in the table")
}

func TestParseARPEmpty(t *testing.T) {
	t.Parallel()

	got, err := parseARP(strings.NewReader(""))
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestNeighboursOnAnyPlatform covers the path where there is no neighbour table
// to read: no MAC is a normal outcome, not a failed scan.
func TestNeighboursOnAnyPlatform(t *testing.T) {
	t.Parallel()

	got, err := neighbours()
	require.NoError(t, err)
	assert.NotNil(t, got)
}
