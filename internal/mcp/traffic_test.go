package mcp

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trafficSource struct{}

func (trafficSource) Name() string            { return "netflow:test" }
func (trafficSource) Kind() dbtype.SourceKind { return dbtype.SourceRouter }

func tcp(src, dst string, dstPort uint16, bytes uint64) plugin.Flow {
	return plugin.Flow{
		Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: 51000, DstPort: dstPort, Protocol: 6,
		Bytes: bytes, Packets: 1, End: time.Now(),
	}
}

// trafficStore is seededStore with traffic recorded: the printer (1) reaches
// the NAS (2) and two public resolvers of one organisation, the NAS reaches
// another. The public addresses are the ones the asn tests use.
func trafficStore(t *testing.T) *inventory.Store {
	t.Helper()

	store := seededStore(t)

	rec := inventory.NewTrafficRecorder(store, testLogger(), nil)
	rec.Add(trafficSource{}, []plugin.Flow{
		tcp("192.0.2.10", "192.0.2.11", 445, 5_000),
		tcp("192.0.2.10", "1.1.1.1", 443, 700),
		tcp("192.0.2.10", "1.0.0.1", 443, 300),
		tcp("192.0.2.11", "8.8.8.8", 53, 9_000),
	})
	require.NoError(t, rec.Flush(t.Context()))

	return store
}

func TestListTraffic(t *testing.T) {
	t.Parallel()

	cs := connect(t, listTraffic(trafficStore(t), time.Now))

	t.Run("one device's peers", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic", map[string]any{"device_id": 1}))
		assert.True(t, out.Recorded)
		require.NotNil(t, out.Device)

		require.Len(t, out.Device.Local, 1)
		assert.Equal(t, int64(2), out.Device.Local[0].DeviceID)
		assert.Equal(t, "smb", out.Device.Local[0].Service)

		require.Len(t, out.Device.Internet, 1)
		assert.Equal(t, "Cloudflare", out.Device.Internet[0].Short)
		assert.Equal(t, int64(1_000), out.Device.Internet[0].Sent)
		assert.Len(t, out.Device.Internet[0].Peers, 2)

		assert.Nil(t, out.BusiestDevices, "only the view asked for")
	})

	t.Run("scoped to the internet", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic",
			map[string]any{"device_id": 1, "scope": "internet"}))
		assert.Empty(t, out.Device.Local)
		assert.NotNil(t, out.Device.Local, "empty, not absent")
		assert.Len(t, out.Device.Internet, 1)
	})

	t.Run("the network summary", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic", nil))
		require.Len(t, out.BusiestDevices, 2)
		assert.Equal(t, int64(2), out.BusiestDevices[0].DeviceID, "the NAS moved the most")

		require.Len(t, out.Organisations, 2)
		assert.Equal(t, "Google", out.Organisations[0].Short)
		assert.Nil(t, out.Device)
	})

	t.Run("first contacts, for one device", func(t *testing.T) {
		t.Parallel()

		out := decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic",
			map[string]any{"first_contact_only": true, "days": 7, "device_id": 2}))
		require.NotNil(t, out.FirstContacts)
		assert.True(t, out.FirstContacts.Partial, "records began moments ago")

		require.Len(t, out.FirstContacts.Contacts, 1)
		assert.Equal(t, "Google", out.FirstContacts.Contacts[0].Short)
	})

	t.Run("a device that does not exist", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "list_traffic", map[string]any{"device_id": 99}))
		assert.Equal(t, float64(http.StatusNotFound), doc["status"])
	})

	t.Run("days past the ceiling are refused", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "list_traffic", map[string]any{"days": trafficMaxDays + 1}))
		assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
	})

	t.Run("an unknown scope is refused", func(t *testing.T) {
		t.Parallel()

		doc := problemOf(t, callTool(t, cs, "list_traffic", map[string]any{"device_id": 1, "scope": "lan"}))
		assert.Equal(t, float64(http.StatusBadRequest), doc["status"])
	})
}

// A group narrows the summary to its devices and the organisations they
// reached.
func TestListTrafficNarrowsToAGroup(t *testing.T) {
	t.Parallel()

	store := trafficStore(t)

	_, err := store.UpdateCuration(t.Context(), 1, inventory.Curation{Group: "office"})
	require.NoError(t, err)

	cs := connect(t, listTraffic(store, time.Now))

	out := decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic", map[string]any{"group": "office"}))
	require.Len(t, out.BusiestDevices, 1)
	assert.Equal(t, int64(1), out.BusiestDevices[0].DeviceID)

	require.Len(t, out.Organisations, 1)
	assert.Equal(t, "Cloudflare", out.Organisations[0].Short, "Google was reached only by the NAS")
}

func TestListTrafficReportsProbingAndAttempts(t *testing.T) {
	t.Parallel()

	store := trafficStore(t)

	// The printer pings 25 addresses nobody answers from.
	var flows []plugin.Flow
	for i := 1; i <= 25; i++ {
		flows = append(flows, plugin.Flow{
			Src: netip.MustParseAddr("192.0.2.10"), Dst: netip.MustParseAddr(fmt.Sprintf("198.51.100.%d", i)),
			Protocol: 1, ICMPType: 8, Bytes: 84, Packets: 1, End: time.Now(),
		})
	}

	rec := inventory.NewTrafficRecorder(store, testLogger(), nil)
	rec.Add(trafficSource{}, flows)
	require.NoError(t, rec.Flush(t.Context()))

	cs := connect(t, listTraffic(store, time.Now))

	out := decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic", nil))
	require.Len(t, out.Probing, 1)
	assert.Equal(t, int64(1), out.Probing[0].DeviceID)
	assert.Equal(t, int64(25), out.Probing[0].Peers)

	out = decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic", map[string]any{"device_id": 1, "limit": 5}))
	assert.Len(t, out.Attempts, 5, "limited like the peers")
	assert.Nil(t, out.Probing, "only with the network summary")

	out = decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic", map[string]any{"device_id": 2}))
	assert.Empty(t, out.Attempts, "the NAS tried nothing")
}

// Nothing collecting reads differently from a quiet network, and the lists
// are empty arrays.
func TestListTrafficWithNothingRecorded(t *testing.T) {
	t.Parallel()

	cs := connect(t, listTraffic(seededStore(t), time.Now))

	out := decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic", map[string]any{"device_id": 1}))
	assert.False(t, out.Recorded)
	require.NotNil(t, out.Device)
	assert.NotNil(t, out.Device.Local)
	assert.NotNil(t, out.Device.Internet)
}

// The network summary says what the internet reached and what it tried: the
// NAS's 443 opened from outside, and a knock on its 22 that carried nothing.
func TestListTrafficReportsIncomingAndProbes(t *testing.T) {
	t.Parallel()

	store := trafficStore(t)

	knock := tcp("1.1.1.1", "192.0.2.11", 22, 60)
	knock.TCPFlags = 0x02

	rec := inventory.NewTrafficRecorder(store, testLogger(), nil)
	rec.Add(trafficSource{}, []plugin.Flow{tcp("1.1.1.1", "192.0.2.11", 443, 4_000), knock})
	require.NoError(t, rec.Flush(t.Context()))

	cs := connect(t, listTraffic(store, time.Now))

	out := decodeAs[listTrafficOutput](t, callTool(t, cs, "list_traffic", nil))
	require.Len(t, out.Incoming, 1)
	assert.Equal(t, int64(2), out.Incoming[0].DeviceID)
	assert.Equal(t, uint16(443), out.Incoming[0].Port)

	require.Len(t, out.Probed, 1)
	assert.Equal(t, int64(2), out.Probed[0].DeviceID)
	assert.False(t, out.Probed[0].Outside)
	assert.Equal(t, []uint16{22}, out.Probed[0].Ports)
}
