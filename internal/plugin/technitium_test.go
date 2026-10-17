package plugin

import (
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/technitium"
)

// fakeResolver serves the two calls of the plugin, as the SQLite logger of
// Technitium does. It returns entries from start, inclusive, in log order,
// paged by offset. A page after the last returns the last page.
type fakeResolver struct {
	apps  string
	token string

	mu      sync.Mutex
	entries []technitium.LogEntry
	starts  []string

	// When set, failPage answers that page of the log with an error.
	failPage int
}

func (f *fakeResolver) log(entries ...technitium.LogEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.entries = append(f.entries, entries...)
}

func (f *fakeResolver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		_, _ = w.Write([]byte(`{"status":"invalid-token"}`))

		return
	}

	switch r.URL.Path {
	case "/api/apps/list":
		_, _ = w.Write([]byte(`{"status":"ok","response":{"apps":` + f.apps + `}}`))
	case "/api/logs/query":
		f.serveLogs(w, r.URL.Query())
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeResolver) serveLogs(w http.ResponseWriter, q url.Values) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.starts = append(f.starts, q.Get("start"))

	page, _ := strconv.Atoi(q.Get("pageNumber"))
	if page == f.failPage {
		_, _ = w.Write([]byte(`{"status":"error","errorMessage":"The database is locked."}`))

		return
	}

	var matched []technitium.LogEntry

	start, _ := time.Parse(time.RFC3339Nano, q.Get("start"))

	for _, e := range f.entries {
		at, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
		if !at.Before(start) {
			matched = append(matched, e)
		}
	}

	per, _ := strconv.Atoi(q.Get("entriesPerPage"))
	total := (len(matched) + per - 1) / per
	page = min(page, max(total, 1))

	lo := min((page-1)*per, len(matched))
	hi := min(lo+per, len(matched))

	resp, _ := json.Marshal(map[string]any{
		"status": "ok",
		"response": technitium.LogPage{
			PageNumber:   int64(page),
			TotalPages:   int64(total),
			TotalEntries: int64(len(matched)),
			Entries:      matched[lo:hi],
		},
	})
	_, _ = w.Write(resp)
}

// queryLoggerApps is an apps listing with one app that logs queries.
const queryLoggerApps = `[
	{"name":"Block Page","dnsApps":[{"classPath":"BlockPageWebServer.App","isQueryLogger":false}]},
	{"name":"Query Logs (Sqlite)","dnsApps":[{"classPath":"QueryLogsSqlite.App","isQueryLogger":true}]}
]`

// testTechnitium starts a fake resolver. It returns it with a plugin that reads
// it, with a fixed clock.
func testTechnitium(t *testing.T, apps, logApp string) (*fakeResolver, *Technitium) {
	t.Helper()

	f := &fakeResolver{apps: apps, token: "secret"}

	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	client, err := technitium.New(&technitium.Config{Host: u.Hostname(), Port: port, Token: "secret"})
	require.NoError(t, err)

	p, err := NewTechnitium("resolver", client, logApp, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	p.now = func() time.Time { return pinned }

	return f, p
}

// entry is a lookup of name by 192.0.2.10, logged at offset after pinned.
func entry(offset time.Duration, name string) technitium.LogEntry {
	return technitium.LogEntry{
		Timestamp:       pinned.Add(offset).UTC().Format(time.RFC3339Nano),
		ClientIPAddress: "192.0.2.10",
		ResponseType:    "Recursive",
		RCode:           "NoError",
		QName:           name,
		QType:           "A",
	}
}

// names returns the name of each query, in order.
func names(qs []Query) []string {
	out := make([]string, len(qs))
	for i, q := range qs {
		out[i] = q.Name
	}

	return out
}

func TestNewTechnitium(t *testing.T) {
	client, err := technitium.New(&technitium.Config{Host: "dns.example.test"})
	require.NoError(t, err)

	_, err = NewTechnitium("", client, "", nil)
	require.ErrorIs(t, err, ErrNoTechnitiumName)

	_, err = NewTechnitium("resolver", nil, "", nil)
	require.Error(t, err)

	p, err := NewTechnitium("resolver", client, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "technitium:resolver", p.Name())
	assert.Equal(t, dbtype.SourceDNS, p.Kind())
}

// TestTechnitiumQueryLogger checks which log the plugin reads. It is the log of
// the only app that logs queries, or of the app that log_app names. Else, the
// error names what to set.
func TestTechnitiumQueryLogger(t *testing.T) {
	two := `[
		{"name":"Query Logs (Sqlite)","dnsApps":[{"classPath":"QueryLogsSqlite.App","isQueryLogger":true}]},
		{"name":"Query Logs (MySQL)","dnsApps":[{"classPath":"QueryLogsMySql.App","isQueryLogger":true}]}
	]`

	for _, tc := range []struct {
		name, apps, logApp string
		want               error
		wantMsg            string
	}{
		{name: "one", apps: queryLoggerApps},
		{name: "chosen of two", apps: two, logApp: "Query Logs (MySQL)"},
		{name: "none", apps: `[{"name":"Block Page","dnsApps":[{"classPath":"BlockPageWebServer.App"}]}]`, want: ErrNoQueryLogger, wantMsg: "install one"},
		{name: "two unchosen", apps: two, want: ErrSeveralQueryLoggers, wantMsg: "set log_app"},
		{name: "chosen missing", apps: queryLoggerApps, logApp: "Query Logs (MySQL)", want: ErrNoQueryLogger, wantMsg: `"Query Logs (MySQL)"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := testTechnitium(t, tc.apps, tc.logApp)

			_, cursor, err := p.Queries(t.Context(), "")
			if tc.want == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.want)
			assert.ErrorContains(t, err, tc.wantMsg)
			assert.Empty(t, cursor, "a failed read leaves the cursor where it was")
		})
	}
}

// TestTechnitiumMapping checks each field that a query takes from a log entry.
// It also checks that the plugin leaves out an entry with no client or no
// name.
func TestTechnitiumMapping(t *testing.T) {
	f, p := testTechnitium(t, queryLoggerApps, "")

	blocked := entry(1*time.Second, "ads.example.net")
	blocked.ResponseType = "Blocked"
	blocked.RCode = "NxDomain"

	upstream := entry(2*time.Second, "tracker.example.net")
	upstream.ResponseType = "UpstreamBlocked"

	missing := entry(3*time.Second, "missing.example.com")
	missing.RCode = "NxDomain"

	answered := entry(4*time.Second, "WWW.Example.COM.")
	answered.ClientIPAddress = "::ffff:192.0.2.11"
	answered.QType = "AAAA"
	answered.Answer = "CNAME edge.example.net, AAAA 2001:db8::5, A 203.0.113.5"

	legacy := entry(5*time.Second, "old.example.com")
	legacy.Answer = "203.0.113.6"

	root := entry(6*time.Second, "")
	noClient := entry(7*time.Second, "example.org")
	noClient.ClientIPAddress = ""

	f.log(blocked, upstream, missing, answered, legacy, root, noClient)

	qs, _, err := p.Queries(t.Context(), "")
	require.NoError(t, err)

	client := netip.MustParseAddr("192.0.2.10")

	assert.Equal(t, []Query{
		{Client: client, Name: "ads.example.net", Type: "A", Blocked: true, At: pinned.Add(1 * time.Second)},
		{Client: client, Name: "tracker.example.net", Type: "A", Blocked: true, At: pinned.Add(2 * time.Second)},
		{Client: client, Name: "missing.example.com", Type: "A", Failed: true, At: pinned.Add(3 * time.Second)},
		{
			Client:  netip.MustParseAddr("192.0.2.11"),
			Name:    "www.example.com",
			Type:    "AAAA",
			Answers: []netip.Addr{netip.MustParseAddr("2001:db8::5"), netip.MustParseAddr("203.0.113.5")},
			At:      pinned.Add(4 * time.Second),
		},
		{Client: client, Name: "old.example.com", Type: "A", Answers: []netip.Addr{netip.MustParseAddr("203.0.113.6")}, At: pinned.Add(5 * time.Second)},
	}, qs)
}

// TestTechnitiumFirstRead checks that a read with no cursor starts one hour
// back. The returned cursor names where an empty log stops.
func TestTechnitiumFirstRead(t *testing.T) {
	f, p := testTechnitium(t, queryLoggerApps, "")

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Empty(t, qs)

	hourBack := pinned.Add(-time.Hour).UTC()
	assert.Equal(t, []string{hourBack.Format("2006-01-02T15:04:05.0000000Z")}, f.starts)
	assert.Equal(t, hourBack.Format(time.RFC3339Nano)+"|0", cursor)
}

// TestTechnitiumResume checks that each read continues where the last read
// stopped. This works across a page boundary, and across lookups at one
// timestamp before and after a read.
func TestTechnitiumResume(t *testing.T) {
	f, p := testTechnitium(t, queryLoggerApps, "")

	for i := range logPageSize + 1 {
		f.log(entry(time.Duration(i)*time.Millisecond, "page.example.com"))
	}

	f.log(entry(time.Minute, "a.example.com"), entry(time.Minute, "b.example.com"))

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Len(t, qs, logPageSize+3, "both pages are read")

	// Two more at the same timestamp as the last two, and one after them.
	f.log(entry(time.Minute, "c.example.com"), entry(time.Minute, "d.example.com"), entry(2*time.Minute, "e.example.com"))

	qs, cursor, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Equal(t, []string{"c.example.com", "d.example.com", "e.example.com"}, names(qs))

	qs, _, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Empty(t, qs, "nothing new was logged")
}

// TestTechnitiumBacklog checks that one read stops after its part of a long
// log. The next read continues from there.
func TestTechnitiumBacklog(t *testing.T) {
	f, p := testTechnitium(t, queryLoggerApps, "")

	total := maxLogPages*logPageSize + 5
	for i := range total {
		f.log(entry(time.Duration(i)*time.Millisecond, "backlog.example.com"))
	}

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Len(t, qs, maxLogPages*logPageSize)

	qs, _, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Len(t, qs, 5)
}

// TestTechnitiumPartial checks that a failed page returns the lookups that the
// read took before it, with a cursor that covers exactly those.
func TestTechnitiumPartial(t *testing.T) {
	f, p := testTechnitium(t, queryLoggerApps, "")

	for i := range logPageSize + 2 {
		f.log(entry(time.Duration(i)*time.Millisecond, "partial.example.com"))
	}

	f.failPage = 2

	qs, cursor, err := p.Queries(t.Context(), "")
	require.Error(t, err)
	assert.Len(t, qs, logPageSize)

	f.failPage = 0

	qs, _, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Len(t, qs, 2)
}

// TestTechnitiumBadCursor checks that a cursor that does not parse starts the
// read again. It does not refuse every later read.
func TestTechnitiumBadCursor(t *testing.T) {
	f, p := testTechnitium(t, queryLoggerApps, "")
	f.log(entry(time.Second, "example.com"))

	for _, cursor := range []string{"garbage", "2026-10-11T09:00:00Z", "2026-10-11T09:00:00Z|x", "yesterday|1"} {
		qs, _, err := p.Queries(t.Context(), cursor)
		require.NoError(t, err, cursor)
		assert.Len(t, qs, 1, cursor)
	}
}

// TestTechnitiumErrors checks that a refused token and an unreachable server
// map to the sentinels of this package.
func TestTechnitiumErrors(t *testing.T) {
	f, p := testTechnitium(t, queryLoggerApps, "")
	f.token = "another"

	_, _, err := p.Queries(t.Context(), "")
	require.ErrorIs(t, err, ErrAuth)

	srv := httptest.NewServer(http.NotFoundHandler())
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	srv.Close()

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	p.client, err = technitium.New(&technitium.Config{Host: u.Hostname(), Port: port})
	require.NoError(t, err)

	_, _, err = p.Queries(t.Context(), "")
	require.ErrorIs(t, err, ErrUnreachable)
}

func TestParseLogTime(t *testing.T) {
	want := time.Date(2026, 10, 11, 9, 0, 0, 123456700, time.UTC)

	for _, s := range []string{"2026-10-11T09:00:00.1234567Z", "2026-10-11T09:00:00.1234567", "2026-10-11T14:30:00.1234567+05:30"} {
		got, err := parseLogTime(s)
		require.NoError(t, err, s)
		assert.True(t, want.Equal(got), s)
		assert.Equal(t, time.UTC, got.Location(), s)
	}

	_, err := parseLogTime("")
	require.Error(t, err)
}
