package plugin

import (
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/adguard"
)

// fakeAdGuardLog serves /control/querylog the way AdGuard Home answers it:
// newest first, older_than keeping entries logged before it, and limit
// capping the page. It has no login set.
type fakeAdGuardLog struct {
	mu       sync.Mutex
	entries  []adguard.LogEntry
	requests []url.Values

	// pageCap pages by fewer entries than asked for, failPage answers the
	// request with that number, counting from 1 across reads, with an error,
	// and refuse answers every request with a 401, each when set.
	pageCap  int
	failPage int
	refuse   bool
}

// log appends a lookup of each name by 192.0.2.10, one second apart, after
// the last one logged.
func (f *fakeAdGuardLog) log(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, name := range names {
		at := pinned.Add(time.Duration(len(f.entries)+1) * time.Second)
		f.entries = append(f.entries, adguard.LogEntry{
			Time:     at.Format(time.RFC3339Nano),
			Client:   "192.0.2.10",
			Question: adguard.Question{Name: name, Type: "A"},
			Reason:   "NotFilteredNotFound",
			Status:   "NOERROR",
		})
	}
}

func (f *fakeAdGuardLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.refuse {
		w.WriteHeader(http.StatusUnauthorized)

		return
	}

	q := r.URL.Query()
	f.requests = append(f.requests, q)

	if len(f.requests) == f.failPage {
		http.Error(w, "querylog: reading file", http.StatusInternalServerError)

		return
	}

	older, _ := time.Parse(time.RFC3339Nano, q.Get("older_than"))
	limit, _ := strconv.Atoi(q.Get("limit"))

	if f.pageCap > 0 {
		limit = min(limit, f.pageCap)
	}

	var page []adguard.LogEntry

	for _, e := range slices.Backward(f.entries) {
		at, _ := time.Parse(time.RFC3339Nano, e.Time)
		if !older.IsZero() && !at.Before(older) {
			continue
		}

		if len(page) == limit {
			break
		}

		page = append(page, e)
	}

	resp, _ := json.Marshal(map[string]any{"data": page})
	_, _ = w.Write(resp)
}

// testAdGuard starts a fake AdGuard Home and returns it with a plugin reading
// it, whose clock is pinned.
func testAdGuard(t *testing.T) (*fakeAdGuardLog, *AdGuard) {
	t.Helper()

	f := &fakeAdGuardLog{}

	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	p, err := NewAdGuard("resolver", adguardClient(t, srv.URL), slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	p.now = func() time.Time { return pinned }

	return f, p
}

func adguardClient(t *testing.T, raw string) *adguard.AdGuard {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	client, err := adguard.New(&adguard.Config{Host: u.Hostname(), Port: port})
	require.NoError(t, err)

	return client
}

// cursorAt is the cursor a read leaves after the nth lookup logged.
func cursorAt(n int) string {
	return pinned.Add(time.Duration(n) * time.Second).Format(time.RFC3339Nano)
}

func TestNewAdGuard(t *testing.T) {
	client, err := adguard.New(&adguard.Config{Host: "dns.example.test"})
	require.NoError(t, err)

	_, err = NewAdGuard("", client, nil)
	require.ErrorIs(t, err, ErrNoAdGuardName)

	_, err = NewAdGuard("resolver", nil, nil)
	require.Error(t, err)

	p, err := NewAdGuard("resolver", client, nil)
	require.NoError(t, err)
	assert.Equal(t, "adguard:resolver", p.Name())
	assert.Equal(t, dbtype.SourceDNS, p.Kind())
}

func TestAdGuardMapping(t *testing.T) {
	f, p := testAdGuard(t)

	f.log("ads.example.net", "kids.example.net", "missing.example.com", "WWW.Example.COM.", "example.org", "", "example.net")

	f.entries[0].Reason = "FilteredBlackList"
	f.entries[0].Answer = []adguard.Answer{{Type: "A", Value: "0.0.0.0"}}
	f.entries[1].Reason = "FilteredParental"
	f.entries[2].Status = "NXDOMAIN"
	f.entries[3].Client = "::ffff:192.0.2.11"
	f.entries[3].Question.Type = "AAAA"
	f.entries[3].Answer = []adguard.Answer{
		{Type: "CNAME", Value: "edge.example.net."},
		{Type: "AAAA", Value: "2001:db8::5"},
		{Type: "A", Value: "203.0.113.5"},
	}
	f.entries[4].Reason = "Rewrite"
	f.entries[4].Answer = []adguard.Answer{{Type: "A", Value: "192.0.2.20"}}
	f.entries[6].Client = ""

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, cursorAt(7), cursor)

	client := netip.MustParseAddr("192.0.2.10")

	assert.Equal(t, []Query{
		{
			Client: client, Name: "ads.example.net", Type: "A", Blocked: true,
			Answers: []netip.Addr{netip.MustParseAddr("0.0.0.0")}, At: pinned.Add(1 * time.Second),
		},
		{Client: client, Name: "kids.example.net", Type: "A", Blocked: true, At: pinned.Add(2 * time.Second)},
		{Client: client, Name: "missing.example.com", Type: "A", Failed: true, At: pinned.Add(3 * time.Second)},
		{
			Client:  netip.MustParseAddr("192.0.2.11"),
			Name:    "www.example.com",
			Type:    "AAAA",
			Answers: []netip.Addr{netip.MustParseAddr("2001:db8::5"), netip.MustParseAddr("203.0.113.5")},
			At:      pinned.Add(4 * time.Second),
		},
		{
			Client: client, Name: "example.org", Type: "A",
			Answers: []netip.Addr{netip.MustParseAddr("192.0.2.20")}, At: pinned.Add(5 * time.Second),
		},
	}, qs)
}

// A read with no cursor takes the last hour, and an empty log leaves the
// cursor empty.
func TestAdGuardFirstRead(t *testing.T) {
	f, p := testAdGuard(t)

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Empty(t, qs)
	assert.Empty(t, cursor)

	f.log("old.example.com", "new.example.com")
	p.now = func() time.Time { return pinned.Add(time.Hour + 1500*time.Millisecond) }

	qs, _, err = p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, []string{"new.example.com"}, names(qs))
}

// Each read takes up where the last left off, across a page boundary, and
// pages past the last entry of the page before.
func TestAdGuardResume(t *testing.T) {
	f, p := testAdGuard(t)

	for range logPageSize + 1 {
		f.log("page.example.com")
	}

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Len(t, qs, logPageSize+1, "both pages are read")
	assert.Equal(t, cursorAt(logPageSize+1), cursor)

	assert.Empty(t, f.requests[0].Get("older_than"))
	assert.Equal(t, cursorAt(2), f.requests[1].Get("older_than"))

	f.log("a.example.com", "b.example.com")

	qs, cursor, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.example.com", "b.example.com"}, names(qs))

	qs, cursor, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Empty(t, qs, "nothing new was logged")
	assert.Equal(t, cursorAt(logPageSize+3), cursor)
}

// A server that pages by fewer entries than asked for is read to the end.
func TestAdGuardShortPages(t *testing.T) {
	f, p := testAdGuard(t)
	f.pageCap = 2

	f.log("a.example.com", "b.example.com", "c.example.com", "d.example.com", "e.example.com")

	qs, _, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com", "e.example.com"}, names(qs))
}

// A read that reaches its cap before the cursor keeps the newest lookups and
// moves the cursor past the rest.
func TestAdGuardBacklog(t *testing.T) {
	f, p := testAdGuard(t)
	f.log("seen.example.com")

	total := maxLogPages*logPageSize + 5
	for range total {
		f.log("backlog.example.com")
	}

	qs, cursor, err := p.Queries(t.Context(), cursorAt(1))
	require.NoError(t, err)
	require.Len(t, qs, maxLogPages*logPageSize)
	assert.Equal(t, pinned.Add(7*time.Second), qs[0].At, "the oldest 5 are skipped")
	assert.Equal(t, cursorAt(total+1), cursor)

	qs, _, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Empty(t, qs)
}

// A page that fails returns nothing and the cursor the read was given, so
// the next read covers the same lookups.
func TestAdGuardPartial(t *testing.T) {
	f, p := testAdGuard(t)

	for range logPageSize + 2 {
		f.log("partial.example.com")
	}

	f.failPage = 2

	qs, cursor, err := p.Queries(t.Context(), "")
	require.Error(t, err)
	assert.Empty(t, qs)
	assert.Empty(t, cursor)

	f.failPage = 0

	qs, _, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Len(t, qs, logPageSize+2)
}

// A cursor that does not parse starts the read afresh instead of refusing
// every read after it.
func TestAdGuardBadCursor(t *testing.T) {
	f, p := testAdGuard(t)
	f.log("example.com")

	for _, cursor := range []string{"garbage", "42", "2026-10-11T09:00:00Z|1"} {
		qs, _, err := p.Queries(t.Context(), cursor)
		require.NoError(t, err, cursor)
		assert.Len(t, qs, 1, cursor)
	}
}

// A refused login and a server that cannot be reached map onto this
// package's sentinels.
func TestAdGuardErrors(t *testing.T) {
	f, p := testAdGuard(t)
	f.refuse = true

	_, _, err := p.Queries(t.Context(), "")
	require.ErrorIs(t, err, ErrAuth)

	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	p.client = adguardClient(t, srv.URL)

	_, _, err = p.Queries(t.Context(), "")
	require.ErrorIs(t, err, ErrUnreachable)
}
