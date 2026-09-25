package inventory

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/pushkar-anand/jocasta/pkg/asn"
)

// A pair of devices is one line, drawn once; a device's traffic with the
// internet is a line to the organisation, whatever addresses it used.
func TestTrafficMapLinksDevicesAndOrganisations(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"), host("192.0.2.11", macB, "host-b"))
	a, b := deviceIDByMAC(t, conn, macA), deviceIDByMAC(t, conn, macB)

	at := s.now()
	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "192.0.2.11", 51000, 22, 1_000, at),
		flow("192.0.2.11", "192.0.2.10", 22, 51000, 2_000, at),
		flow("192.0.2.11", "1.1.1.1", 51000, 443, 500, at),
		flow("1.1.1.1", "192.0.2.11", 443, 51000, 700, at),
	})
	require.NoError(t, rec.Flush(t.Context()))

	m, err := s.TrafficMap(t.Context(), at, nil)
	require.NoError(t, err)

	require.Len(t, m.Devices, 2)
	assert.Equal(t, "host-b", m.Devices[0].Name, "busiest first")

	org, ok := asn.Lookup(netip.MustParseAddr("1.1.1.1"))
	require.True(t, ok)
	require.Len(t, m.Orgs, 1)
	assert.Equal(t, org.ASN, m.Orgs[0].ASN)
	assert.Equal(t, int64(1_200), m.Orgs[0].Bytes)

	links := map[string]int64{}
	for _, l := range m.Links {
		links[fmt.Sprintf("%d-%d-%d", l.Device, l.PeerDevice, l.PeerASN)] = l.Bytes
	}

	assert.Equal(t, map[string]int64{
		fmt.Sprintf("%d-%d-0", a, b):       3_000,
		fmt.Sprintf("%d-0-%d", b, org.ASN): 1_200,
	}, links)
	assert.Zero(t, m.MoreDevices)
	assert.False(t, m.Devices[0].Active, "no recent edges given")
}

// What the recorder saw lately marks the devices, organisation and lines it
// covers; the rest stay still.
func TestTrafficMapMarksRecentActivity(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"), host("192.0.2.11", macB, "host-b"))

	at := s.now()
	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "192.0.2.11", 51000, 22, 1_000, at),
		flow("192.0.2.11", "192.0.2.10", 22, 51000, 2_000, at),
		flow("192.0.2.11", "1.1.1.1", 51000, 443, 500, at),
	})
	require.NoError(t, rec.Flush(t.Context()))

	recent := []RecentEdge{{A: netip.MustParseAddr("1.1.1.1"), B: netip.MustParseAddr("192.0.2.11"), Bytes: 500}}

	m, err := s.TrafficMap(t.Context(), at, recent)
	require.NoError(t, err)

	active := map[string]bool{}
	for _, d := range m.Devices {
		active[d.Name] = d.Active
	}

	assert.Equal(t, map[string]bool{"host-a": false, "host-b": true}, active)
	require.Len(t, m.Orgs, 1)
	assert.True(t, m.Orgs[0].Active)

	for _, l := range m.Links {
		assert.Equal(t, l.PeerASN != 0, l.Active, "only the internet line is active")
	}
}

// Past the cap the quietest devices are only counted, and their lines go with
// them.
func TestTrafficMapFoldsTheQuietest(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	var hosts []scanner.Host
	for i := range MapMaxDevices + 2 {
		hosts = append(hosts, host(fmt.Sprintf("192.0.2.%d", 10+i), fmt.Sprintf("00:00:5e:00:53:%02x", 0x10+i), ""))
	}

	sweep(t, s, hosts...)

	at := s.now()
	rec := newRecorder(s, nil)

	var flows []plugin.Flow
	for i := range MapMaxDevices + 2 {
		flows = append(flows, flow(fmt.Sprintf("192.0.2.%d", 10+i), "1.1.1.1", 51000, 443, uint64(1_000+i), at))
	}

	rec.Add(trafficSource{}, flows)
	require.NoError(t, rec.Flush(t.Context()))

	m, err := s.TrafficMap(t.Context(), at.Add(-time.Minute), nil)
	require.NoError(t, err)

	assert.Len(t, m.Devices, MapMaxDevices)
	assert.Equal(t, 2, m.MoreDevices)
	assert.Len(t, m.Links, MapMaxDevices)
}

// A company announcing from two ASNs is one organisation on the map, under
// its busier number, and every ASN leads to it.
func TestMergeOrgsFoldsOneCompanysNumbers(t *testing.T) {
	t.Parallel()

	merged, canon := mergeOrgs(map[uint32]*MapOrg{
		64500: {ASN: 64500, Short: "Example", Bytes: 100},
		64501: {ASN: 64501, Short: "Example", Bytes: 900},
		64502: {ASN: 64502, Short: "Other", Bytes: 500},
	})

	require.Len(t, merged, 2)
	assert.Equal(t, MapOrg{ASN: 64501, Short: "Example", Bytes: 1_000}, *merged[0])
	assert.Equal(t, MapOrg{ASN: 64502, Short: "Other", Bytes: 500}, *merged[1])
	assert.Equal(t, map[uint32]uint32{64500: 64501, 64501: 64501, 64502: 64502}, canon)
}

// A line lists the services it carried, once each and by port, named where
// the port has a usual name.
func TestTrafficMapLinksCarryTheirServices(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))

	at := s.now()
	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "1.1.1.1", 51000, 443, 500, at),
		flow("192.0.2.10", "1.1.1.1", 51001, 22, 500, at),
		flow("192.0.2.10", "1.1.1.1", 51002, 443, 500, at),
	})
	require.NoError(t, rec.Flush(t.Context()))

	m, err := s.TrafficMap(t.Context(), at, nil)
	require.NoError(t, err)
	require.Len(t, m.Links, 1)
	assert.Equal(t, []MapService{
		{Protocol: protoTCP, Port: 22, Name: "ssh"},
		{Protocol: protoTCP, Port: 443, Name: "https"},
	}, m.Links[0].Services)
}

func TestParseServicesSkipsWhatDoesNotParse(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []MapService{{Protocol: 17, Port: 123}}, parseServices("17/123,x/1,6/99999,junk"))
	assert.Empty(t, parseServices(""))
}
