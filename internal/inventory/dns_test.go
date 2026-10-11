package inventory

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/plugin"
)

// lookup is a query for name from client, logged at.
func lookup(client, name string, at time.Time) plugin.Query {
	return plugin.Query{Client: netip.MustParseAddr(client), Name: name, Type: "A", At: at}
}

func dnsRows(t *testing.T, s *Store) []string {
	t.Helper()

	return queryStrings(t, s.conn, `
		SELECT device_id || ' ' || hour || ' ' || name || ' ' || lookups || ' ' || blocked || ' ' || failed
		FROM dns_hourly ORDER BY device_id, hour, name`)
}

// TestRecordQueriesCountsPerDeviceHourAndName checks the rollup: one row per
// device, hour and name, a lookup from an address no device holds dropped,
// and a second read adding to the first.
func TestRecordQueriesCountsPerDeviceHourAndName(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	a, b := deviceIDByMAC(t, conn, macA), deviceIDByMAC(t, conn, macB)

	blocked := lookup("192.0.2.10", "ads.example.net", trafficHour.Add(time.Minute))
	blocked.Blocked = true

	failed := lookup("192.0.2.11", "missing.example.com", trafficHour)
	failed.Failed = true

	res, err := s.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS, []plugin.Query{
		lookup("192.0.2.10", "example.com", trafficHour.Add(5*time.Minute)),
		lookup("192.0.2.10", "example.com", trafficHour.Add(50*time.Minute)),
		lookup("192.0.2.10", "example.com", trafficHour.Add(65*time.Minute)),
		blocked,
		failed,
		lookup("198.51.100.1", "example.com", trafficHour),
	}, "cursor-1")
	require.NoError(t, err)
	assert.Equal(t, &QueryResult{Recorded: 5, Dropped: 1}, res)

	_, err = s.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS, []plugin.Query{
		lookup("192.0.2.10", "example.com", trafficHour.Add(10*time.Minute)),
	}, "cursor-2")
	require.NoError(t, err)

	first := trafficHour.Format(dbtype.Layout)
	second := trafficHour.Add(time.Hour).Format(dbtype.Layout)

	assert.Equal(t, []string{
		strconv.FormatInt(a, 10) + " " + first + " ads.example.net 1 1 0",
		strconv.FormatInt(a, 10) + " " + first + " example.com 3 0 0",
		strconv.FormatInt(a, 10) + " " + second + " example.com 1 0 0",
		strconv.FormatInt(b, 10) + " " + first + " missing.example.com 1 0 1",
	}, dnsRows(t, s))

	cursor, err := s.QueryCursor(t.Context(), "technitium:resolver")
	require.NoError(t, err)
	assert.Equal(t, "cursor-2", cursor)
}

// TestQueryCursorIsEmptyBeforeAnyRead checks that a source never recorded
// starts with no cursor, and that a read with no lookups still moves it.
func TestQueryCursorIsEmptyBeforeAnyRead(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)

	cursor, err := s.QueryCursor(t.Context(), "technitium:resolver")
	require.NoError(t, err)
	assert.Empty(t, cursor)

	_, err = s.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS, nil, "cursor-1")
	require.NoError(t, err)

	cursor, err = s.QueryCursor(t.Context(), "technitium:resolver")
	require.NoError(t, err)
	assert.Equal(t, "cursor-1", cursor)
	assert.Empty(t, dnsRows(t, s))
}

// TestPruneDeletesLookupsPastTheirWindow checks that lookups follow their own
// window, and that the traffic window leaves them alone.
func TestPruneDeletesLookupsPastTheirWindow(t *testing.T) {
	t.Parallel()

	s, _, advance := clockStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	start := s.now().UTC().Truncate(time.Hour)

	_, err := s.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS, []plugin.Query{
		lookup("192.0.2.10", "example.com", start),
		lookup("192.0.2.10", "example.com", start.Add(48*time.Hour)),
	}, "cursor-1")
	require.NoError(t, err)

	advance(49 * time.Hour)

	res, err := s.Prune(t.Context(), Retention{Traffic: 24 * time.Hour})
	require.NoError(t, err)
	assert.Zero(t, res.Lookups)
	assert.Len(t, dnsRows(t, s), 2)

	res, err = s.Prune(t.Context(), Retention{DNS: 24 * time.Hour})
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Lookups)
	assert.Len(t, dnsRows(t, s), 1)
}
