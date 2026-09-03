package inventory

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// segment builds what a source claims one prefix is. A malformed argument is a
// broken test.
func segment(cidr, name string, vlan int) plugin.Network {
	return plugin.Network{Prefix: netip.MustParsePrefix(cidr), Name: name, VLAN: vlan}
}

func TestRecordNetworksNamesAndTagsASegment(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	require.NoError(t, s.RecordNetworks(t.Context(),
		[]plugin.Network{segment(prefix, "Home", 10)}))

	networks, err := s.ListNetworks(t.Context())
	require.NoError(t, err)

	require.Len(t, networks, 1)
	assert.Equal(t, prefix, networks[0].CIDR)
	assert.Equal(t, "Home", networks[0].Name)
	assert.Equal(t, 10, networks[0].VLAN)
}

// A sweep records the prefix and a source records what it is called. They must
// meet on the same row, or the overview lists every segment twice.
func TestRecordNetworksFillsInTheRowASweepOpened(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	require.NoError(t, s.RecordNetworks(t.Context(),
		[]plugin.Network{segment(prefix, "Home", 10)}))

	assert.Equal(t, 1, queryInt(t, conn, `SELECT COUNT(*) FROM networks`))

	networks, err := s.ListNetworks(t.Context())
	require.NoError(t, err)

	require.Len(t, networks, 1)
	assert.Equal(t, "Home", networks[0].Name)
	assert.Equal(t, 10, networks[0].VLAN)
	assert.Equal(t, 1, networks[0].Total, "the devices on it are untouched")
}

// A later sweep must not clear what the router said the segment is.
func TestASweepDoesNotUnnameASegment(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	require.NoError(t, s.RecordNetworks(t.Context(),
		[]plugin.Network{segment(prefix, "Home", 10)}))

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))

	networks, err := s.ListNetworks(t.Context())
	require.NoError(t, err)

	require.Len(t, networks, 1)
	assert.Equal(t, "Home", networks[0].Name)
	assert.Equal(t, 10, networks[0].VLAN)
}

// The router is the authority on what its segments are, so a segment renamed
// there is renamed here, and one that loses its tag loses it here.
func TestRecordNetworksFollowsTheRouter(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	require.NoError(t, s.RecordNetworks(t.Context(),
		[]plugin.Network{segment(prefix, "Home", 10)}))
	require.NoError(t, s.RecordNetworks(t.Context(),
		[]plugin.Network{segment(prefix, "", 0)}))

	networks, err := s.ListNetworks(t.Context())
	require.NoError(t, err)

	require.Len(t, networks, 1)
	assert.Empty(t, networks[0].Name)
	assert.Zero(t, networks[0].VLAN)
}

// An address on a segment no sweep covers still belongs to it, which is the
// reason the segments are recorded before the devices are.
func TestASegmentTheRouterDescribedCatchesItsAddresses(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	require.NoError(t, s.RecordNetworks(t.Context(),
		[]plugin.Network{segment("198.51.100.0/24", "IoT", 20)}))

	report(t, s, fact("198.51.100.5", macA, "camera", true, dbtype.HostnameFromDHCPLease))

	networks, err := s.ListNetworks(t.Context())
	require.NoError(t, err)

	require.Len(t, networks, 1)
	assert.Equal(t, "IoT", networks[0].Name)
	assert.Equal(t, 1, networks[0].Total)

	assert.Equal(t, 1, queryInt(t, conn,
		`SELECT COUNT(*) FROM addresses a JOIN networks n ON n.id = a.network_id WHERE a.ip = ?`,
		"198.51.100.5"))
}

// A device the user ignored is left off the network page's list, so it must be
// left out of the count above it too.
func TestListNetworksLeavesOutIgnoredDevices(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	sweep(t, s, host("192.0.2.10", macA, "printer.local"))
	sweep(t, s, host("192.0.2.11", macB, "nas.local"))

	_, err := s.UpdateCuration(t.Context(), deviceIDByMAC(t, conn, macB), Curation{Ignored: true})
	require.NoError(t, err)

	networks, err := s.ListNetworks(t.Context())
	require.NoError(t, err)

	require.Len(t, networks, 1)
	assert.Equal(t, 1, networks[0].Total, "the ignored device is not on the prefix as far as the overview is concerned")
	assert.Equal(t, 1, networks[0].Online)
}

func TestRecordNetworksAcceptsNothingToRecord(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	require.NoError(t, s.RecordNetworks(t.Context(), nil))

	assert.Zero(t, queryInt(t, conn, `SELECT COUNT(*) FROM networks`))
}

// A zero prefix names nothing, and writing it would put a row in the table
// that no address can ever match.
func TestRecordNetworksSkipsAPrefixThatIsNotOne(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	require.NoError(t, s.RecordNetworks(t.Context(),
		[]plugin.Network{{Name: "nowhere"}, segment(prefix, "Home", 10)}))

	assert.Equal(t, 1, queryInt(t, conn, `SELECT COUNT(*) FROM networks`))
	assert.Equal(t, "Home", queryString(t, conn, `SELECT name FROM networks`))
}

// Nothing has named the segment, so it holds a null rather than an empty
// string: the two read the same from Go and only one of them is true.
func TestAnUnnamedSegmentHoldsNoName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)

	require.NoError(t, s.RecordNetworks(t.Context(),
		[]plugin.Network{segment(prefix, "", 0)}))

	assert.Equal(t, 1, queryInt(t, conn,
		`SELECT COUNT(*) FROM networks WHERE name IS NULL AND vlan_id IS NULL`))
}
