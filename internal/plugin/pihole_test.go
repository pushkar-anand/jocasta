package plugin

import (
	"cmp"
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
	"github.com/pushkar-anand/jocasta/pkg/pihole"
)

// fakePiHoleLog serves /api/queries as FTL does from memory: newest first,
// with from inclusive. cursor keeps ids at or below it. start and length page
// by offset. It has no password, so nothing signs in.
type fakePiHoleLog struct {
	mu       sync.Mutex
	queries  []pihole.Query
	requests []url.Values

	// When set, failPage answers the page with that number with an error.
	// refuse answers every request with a 401.
	failPage int
	refuse   bool
}

// log appends a lookup of each name by 192.0.2.10, one second apart, after the
// last logged lookup.
func (f *fakePiHoleLog) log(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, name := range names {
		id := int64(len(f.queries) + 1)
		f.queries = append(f.queries, pihole.Query{
			ID:     id,
			Time:   float64(pinned.Unix() + id),
			Type:   "A",
			Domain: name,
			Status: "FORWARDED",
			Client: pihole.Client{IP: "192.0.2.10"},
			Reply:  pihole.Reply{Type: "IP"},
		})
	}
}

func (f *fakePiHoleLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.refuse {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"key":"unauthorized","message":"Unauthorized","hint":null}}`))

		return
	}

	q := r.URL.Query()
	f.requests = append(f.requests, q)

	from, _ := strconv.ParseFloat(q.Get("from"), 64)
	cursor, _ := strconv.ParseInt(q.Get("cursor"), 10, 64)
	start, _ := strconv.Atoi(q.Get("start"))
	length, _ := strconv.Atoi(q.Get("length"))

	if start/length+1 == f.failPage {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"key":"database_error","message":"Database is locked","hint":null}}`))

		return
	}

	var matched []pihole.Query

	for _, e := range f.queries {
		if e.Time >= from && (cursor == 0 || e.ID <= cursor) {
			matched = append(matched, e)
		}
	}

	slices.SortFunc(matched, func(a, b pihole.Query) int { return cmp.Compare(b.ID, a.ID) })

	lo := min(start, len(matched))
	hi := min(lo+length, len(matched))

	resp, _ := json.Marshal(map[string]any{"queries": matched[lo:hi]})
	_, _ = w.Write(resp)
}

// testPiHole starts a fake Pi-hole. It returns it with a plugin that reads it,
// with a fixed clock.
func testPiHole(t *testing.T) (*fakePiHoleLog, *PiHole) {
	t.Helper()

	f := &fakePiHoleLog{}

	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	p, err := NewPiHole("resolver", piholeClient(t, srv.URL), slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	p.now = func() time.Time { return pinned }

	return f, p
}

func piholeClient(t *testing.T, raw string) *pihole.PiHole {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	client, err := pihole.New(&pihole.Config{Host: u.Hostname(), Port: port})
	require.NoError(t, err)

	return client
}

func TestNewPiHole(t *testing.T) {
	client, err := pihole.New(&pihole.Config{Host: "dns.example.test"})
	require.NoError(t, err)

	_, err = NewPiHole("", client, nil)
	require.ErrorIs(t, err, ErrNoPiHoleName)

	_, err = NewPiHole("resolver", nil, nil)
	require.Error(t, err)

	p, err := NewPiHole("resolver", client, nil)
	require.NoError(t, err)
	assert.Equal(t, "pihole:resolver", p.Name())
	assert.Equal(t, dbtype.SourceDNS, p.Kind())
}

func TestPiHoleMapping(t *testing.T) {
	f, p := testPiHole(t)

	f.log("ads.example.net", "tracker.example.net", "missing.example.com", "WWW.Example.COM.", "pending.example.com", "", "example.org")

	f.queries[0].Status = "GRAVITY"
	f.queries[0].Reply.Type = "NXDOMAIN"
	f.queries[1].Status = "EXTERNAL_BLOCKED_IP"
	f.queries[2].Reply.Type = "NXDOMAIN"
	f.queries[3].Client.IP = "::ffff:192.0.2.11"
	f.queries[3].Type = "AAAA"
	f.queries[4].Status = ""
	f.queries[4].Reply.Type = ""
	f.queries[6].Client.IP = ""

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, "7", cursor)

	client := netip.MustParseAddr("192.0.2.10")

	assert.Equal(t, []Query{
		{Client: client, Name: "ads.example.net", Type: "A", Blocked: true, At: pinned.Add(1 * time.Second)},
		{Client: client, Name: "tracker.example.net", Type: "A", Blocked: true, At: pinned.Add(2 * time.Second)},
		{Client: client, Name: "missing.example.com", Type: "A", Failed: true, At: pinned.Add(3 * time.Second)},
		{Client: netip.MustParseAddr("192.0.2.11"), Name: "www.example.com", Type: "AAAA", At: pinned.Add(4 * time.Second)},
		{Client: client, Name: "pending.example.com", Type: "A", At: pinned.Add(5 * time.Second)},
	}, qs)
}

// A read with no cursor starts one hour back. An empty log leaves the cursor
// empty.
func TestPiHoleFirstRead(t *testing.T) {
	f, p := testPiHole(t)

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Empty(t, qs)
	assert.Empty(t, cursor)

	require.Len(t, f.requests, 1)
	assert.Equal(t, strconv.FormatInt(pinned.Add(-time.Hour).Unix(), 10), f.requests[0].Get("from"))
}

// Each read continues where the last read stopped, across a page boundary. The
// later pages of a read count from the newest query of the first page.
func TestPiHoleResume(t *testing.T) {
	f, p := testPiHole(t)

	for range logPageSize + 1 {
		f.log("page.example.com")
	}

	qs, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	assert.Len(t, qs, logPageSize+1, "both pages are read")
	assert.Equal(t, strconv.Itoa(logPageSize+1), cursor)

	require.Len(t, f.requests, 2)
	assert.Empty(t, f.requests[0].Get("cursor"))
	assert.Equal(t, strconv.Itoa(logPageSize+1), f.requests[1].Get("cursor"))
	assert.Equal(t, strconv.Itoa(logPageSize), f.requests[1].Get("start"))

	f.log("a.example.com", "b.example.com")

	qs, cursor, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.example.com", "b.example.com"}, names(qs))
	assert.Empty(t, f.requests[2].Get("from"), "a read with a cursor asks for no time")

	qs, cursor, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Empty(t, qs, "nothing new was logged")
	assert.Equal(t, strconv.Itoa(logPageSize+3), cursor)
}

// A read that reaches its limit before the cursor keeps the newest lookups. It
// moves the cursor past the rest.
func TestPiHoleBacklog(t *testing.T) {
	f, p := testPiHole(t)
	f.log("seen.example.com")

	total := maxLogPages*logPageSize + 5
	for range total {
		f.log("backlog.example.com")
	}

	qs, cursor, err := p.Queries(t.Context(), "1")
	require.NoError(t, err)
	require.Len(t, qs, maxLogPages*logPageSize)
	assert.Equal(t, pinned.Add(7*time.Second), qs[0].At, "the oldest 5 are skipped")
	assert.Equal(t, strconv.Itoa(total+1), cursor)

	qs, _, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Empty(t, qs)
}

// A page that fails returns nothing, with the cursor that the read got. So the
// next read covers the same lookups.
func TestPiHolePartial(t *testing.T) {
	f, p := testPiHole(t)

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

// A log with ids below the cursor is read from one hour back. It does not wait
// until its ids pass the cursor. The log of Pi-hole is like this after its
// database is gone.
func TestPiHoleReset(t *testing.T) {
	f, p := testPiHole(t)
	f.log("before.example.com", "before.example.com", "before.example.com")

	_, cursor, err := p.Queries(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, "3", cursor)

	f.queries = nil
	f.log("after.example.com")

	qs, cursor, err := p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Equal(t, []string{"after.example.com"}, names(qs))
	assert.Equal(t, "1", cursor)
	assert.Equal(t, strconv.FormatInt(pinned.Add(-time.Hour).Unix(), 10), f.requests[len(f.requests)-1].Get("from"))

	f.log("next.example.com")

	qs, _, err = p.Queries(t.Context(), cursor)
	require.NoError(t, err)
	assert.Equal(t, []string{"next.example.com"}, names(qs), "the read after the reset resumes from its cursor")
}

// A cursor that does not parse starts the read again. It does not refuse every
// later read.
func TestPiHoleBadCursor(t *testing.T) {
	f, p := testPiHole(t)
	f.log("example.com")

	for _, cursor := range []string{"garbage", "-3", "2026-10-11T09:00:00Z|1"} {
		qs, _, err := p.Queries(t.Context(), cursor)
		require.NoError(t, err, cursor)
		assert.Len(t, qs, 1, cursor)
	}
}

// A refused password and an unreachable server map to the sentinels of this
// package.
func TestPiHoleErrors(t *testing.T) {
	f, p := testPiHole(t)
	f.refuse = true

	_, _, err := p.Queries(t.Context(), "")
	require.ErrorIs(t, err, ErrAuth)

	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	p.client = piholeClient(t, srv.URL)

	_, _, err = p.Queries(t.Context(), "")
	require.ErrorIs(t, err, ErrUnreachable)
}
