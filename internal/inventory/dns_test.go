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

func answerRows(t *testing.T, s *Store) []string {
	t.Helper()

	return queryStrings(t, s.conn, `
		SELECT device_id || ' ' || addr || ' ' || name || ' ' || lookups || ' ' || last_hour
		FROM dns_answers ORDER BY device_id, addr, name`)
}

// answered is a lookup of name from client, answered with addrs.
func answered(client, name string, at time.Time, addrs ...string) plugin.Query {
	q := lookup(client, name, at)

	for _, a := range addrs {
		q.Answers = append(q.Answers, netip.MustParseAddr(a))
	}

	return q
}

// TestRecordQueriesKeepsAnswers checks that each answered address is kept per
// device and name with the latest hour it was seen, that blocked and failed
// lookups and unspecified or loopback answers are left out, and that a second
// read adds to the first.
func TestRecordQueriesKeepsAnswers(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""), host("192.0.2.11", macB, ""))

	a, b := deviceIDByMAC(t, conn, macA), deviceIDByMAC(t, conn, macB)

	blocked := answered("192.0.2.10", "ads.example.net", trafficHour, "198.51.100.9")
	blocked.Blocked = true

	failed := answered("192.0.2.10", "missing.example.com", trafficHour, "198.51.100.8")
	failed.Failed = true

	_, err := s.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS, []plugin.Query{
		answered("192.0.2.10", "example.com", trafficHour, "203.0.113.1", "2001:db8::1"),
		answered("192.0.2.10", "example.com", trafficHour.Add(time.Hour), "203.0.113.1"),
		answered("192.0.2.10", "cdn.example.net", trafficHour, "203.0.113.1"),
		answered("192.0.2.11", "example.com", trafficHour, "203.0.113.1"),
		answered("192.0.2.10", "sink.example.org", trafficHour, "0.0.0.0", "127.0.0.1", "::"),
		blocked,
		failed,
	}, "cursor-1")
	require.NoError(t, err)

	_, err = s.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS, []plugin.Query{
		answered("192.0.2.10", "example.com", trafficHour, "203.0.113.1"),
	}, "cursor-2")
	require.NoError(t, err)

	first := trafficHour.Format(dbtype.Layout)
	second := trafficHour.Add(time.Hour).Format(dbtype.Layout)
	ida, idb := strconv.FormatInt(a, 10), strconv.FormatInt(b, 10)

	assert.Equal(t, []string{
		ida + " 2001:db8::1 example.com 1 " + first,
		ida + " 203.0.113.1 cdn.example.net 1 " + first,
		ida + " 203.0.113.1 example.com 3 " + second,
		idb + " 203.0.113.1 example.com 1 " + first,
	}, answerRows(t, s))
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

// TestPruneDeletesLookupsPastTheirWindow checks that lookups and their answers
// follow their own window, and that the traffic window leaves them alone.
func TestPruneDeletesLookupsPastTheirWindow(t *testing.T) {
	t.Parallel()

	s, _, advance := clockStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	start := s.now().UTC().Truncate(time.Hour)

	_, err := s.RecordQueries(t.Context(), "technitium:resolver", dbtype.SourceDNS, []plugin.Query{
		answered("192.0.2.10", "example.com", start, "203.0.113.1"),
		answered("192.0.2.10", "example.com", start.Add(48*time.Hour), "203.0.113.2"),
	}, "cursor-1")
	require.NoError(t, err)

	advance(49 * time.Hour)

	res, err := s.Prune(t.Context(), Retention{Traffic: 24 * time.Hour})
	require.NoError(t, err)
	assert.Zero(t, res.Lookups)
	assert.Zero(t, res.Answers)
	assert.Len(t, dnsRows(t, s), 2)
	assert.Len(t, answerRows(t, s), 2)

	res, err = s.Prune(t.Context(), Retention{DNS: 24 * time.Hour})
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Lookups)
	assert.Equal(t, int64(1), res.Answers)
	assert.Len(t, dnsRows(t, s), 1)
	assert.Len(t, answerRows(t, s), 1)
}

// TestDeviceDNSSumsSourcesBusiestFirst checks that two nodes' counts for one
// name add up, names come busiest first, and the period drops older hours.
func TestDeviceDNSSumsSourcesBusiestFirst(t *testing.T) {
	t.Parallel()

	s, conn := newStore(t)
	sweep(t, s, host("192.0.2.10", macA, ""))

	a := deviceIDByMAC(t, conn, macA)

	read, err := s.DNSRead(t.Context())
	require.NoError(t, err)
	assert.False(t, read)

	blocked := lookup("192.0.2.10", "ads.example.net", trafficHour)
	blocked.Blocked = true

	_, err = s.RecordQueries(t.Context(), "technitium:node-a", dbtype.SourceDNS, []plugin.Query{
		lookup("192.0.2.10", "example.com", trafficHour),
		lookup("192.0.2.10", "old.example.org", trafficHour.Add(-48*time.Hour)),
		blocked,
	}, "a")
	require.NoError(t, err)

	_, err = s.RecordQueries(t.Context(), "technitium:node-b", dbtype.SourceDNS, []plugin.Query{
		lookup("192.0.2.10", "example.com", trafficHour.Add(time.Hour)),
	}, "b")
	require.NoError(t, err)

	names, err := s.DeviceDNS(t.Context(), a, trafficHour.Add(-time.Hour))
	require.NoError(t, err)

	assert.Equal(t, []*DNSName{
		{Name: "example.com", Lookups: 2, LastHour: trafficHour.Add(time.Hour)},
		{Name: "ads.example.net", Lookups: 1, Blocked: 1, LastHour: trafficHour},
	}, names)

	read, err = s.DNSRead(t.Context())
	require.NoError(t, err)
	assert.True(t, read)

	none, err := s.DeviceDNS(t.Context(), a+100, trafficHour)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	totals, err := s.DeviceDNSTotals(t.Context(), a, trafficHour.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, &DNSTotals{Lookups: 3, Names: 2, Blocked: 1}, totals)

	totals, err = s.DeviceDNSTotals(t.Context(), a+100, trafficHour)
	require.NoError(t, err)
	assert.Equal(t, &DNSTotals{}, totals)
}
