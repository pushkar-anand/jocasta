package inventory

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/pkg/geo"
)

// Internet traffic is gathered by the country each address is registered in,
// with the devices and organisations behind it; a device on the network is in
// no country.
func TestTrafficByCountryPlacesTheInternet(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"), host("192.0.2.11", macB, "host-b"))

	at := s.now()
	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		flow("192.0.2.10", "1.1.1.1", 51000, 443, 700, at),
		flow("192.0.2.11", "1.1.1.1", 51000, 443, 300, at),
		flow("192.0.2.10", "192.0.2.11", 51000, 22, 5_000, at),
	})
	require.NoError(t, rec.Flush(t.Context()))

	code, ok := geo.Lookup(netip.MustParseAddr("1.1.1.1"))
	require.True(t, ok)

	got, err := s.TrafficByCountry(t.Context(), at, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)

	c := got[0]
	assert.Equal(t, code, c.Code)
	assert.NotEmpty(t, c.Name)
	assert.Equal(t, int64(1_000), c.Bytes)
	assert.False(t, c.Active)
	require.Len(t, c.Devices, 2)
	assert.Equal(t, "host-a", c.Devices[0].Name, "busiest first")
	require.Len(t, c.Orgs, 1)
	assert.Equal(t, int64(1_000), c.Orgs[0].Bytes)
}

// A recent exchange with an address marks its country active.
func TestTrafficByCountryMarksRecentActivity(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, "host-a"))

	at := s.now()
	rec := newRecorder(s, nil)
	rec.Add(trafficSource{}, []plugin.Flow{flow("192.0.2.10", "1.1.1.1", 51000, 443, 700, at)})
	require.NoError(t, rec.Flush(t.Context()))

	got, err := s.TrafficByCountry(t.Context(), at, []RecentEdge{{
		A: netip.MustParseAddr("1.1.1.1"), B: netip.MustParseAddr("192.0.2.10"),
	}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Active)
}

func TestTopPartsCountsTheRest(t *testing.T) {
	t.Parallel()

	var parts []*CountryPart
	for i := range countryTop + 3 {
		parts = append(parts, &CountryPart{Name: string(rune('a' + i)), Bytes: int64(i)})
	}

	top, more := topParts(parts)
	assert.Len(t, top, countryTop)
	assert.Equal(t, 3, more)
	assert.Equal(t, int64(countryTop+2), top[0].Bytes)
}
