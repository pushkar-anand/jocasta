package technitium

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The answers below have the shape that the server returns, with
// documentation addresses and names.
const (
	appsAnswer = `{"response":{"apps":[
		{"name":"Block Page","version":"1.0","dnsApps":[{"classPath":"BlockPageWebServer.App","isQueryLogger":false}]},
		{"name":"Query Logs (Sqlite)","version":"8.0","dnsApps":[{"classPath":"QueryLogsSqlite.App","isQueryLogger":true}]}
	]},"status":"ok"}`

	logsAnswer = `{"response":{"pageNumber":1,"totalPages":3,"totalEntries":3,"entries":[
		{"rowNumber":1,"timestamp":"2026-10-11T09:00:00.1234567Z","clientIpAddress":"192.0.2.10","protocol":"Udp",
		 "responseType":"Recursive","responseRtt":12.5,"rcode":"NoError","qname":"www.example.com","qtype":"A","qclass":"IN",
		 "answer":"CNAME edge.example.net, A 203.0.113.5"}
	]},"status":"ok"}`
)

// fakeServer answers each path with a canned body. It records the last
// request, so a test can check what the client asked.
type fakeServer struct {
	token   string
	answers map[string]string
	last    *http.Request
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.last = r

	if r.Header.Get("Authorization") != "Bearer "+f.token {
		_, _ = w.Write([]byte(`{"status":"invalid-token","errorMessage":"Invalid token or session expired."}`))

		return
	}

	body, ok := f.answers[r.URL.Path]
	if !ok {
		http.NotFound(w, r)

		return
	}

	_, _ = w.Write([]byte(body))
}

// newFake starts a server that replies with answers. It returns the server
// and a client that holds token.
func newFake(t *testing.T, token string, answers map[string]string) (*fakeServer, *Technitium) {
	t.Helper()

	f := &fakeServer{token: "secret", answers: answers}

	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	c, err := New(&Config{Host: u.Hostname(), Port: port, Token: token})
	require.NoError(t, err)

	return f, c
}

func TestNewNeedsAHost(t *testing.T) {
	_, err := New(&Config{})
	require.ErrorIs(t, err, ErrNoHost)

	_, err = New(nil)
	require.ErrorIs(t, err, ErrNoHost)
}

// TestNewPorts checks the default port of each scheme. It also checks that a
// configured port wins.
func TestNewPorts(t *testing.T) {
	for _, tc := range []struct {
		cfg  Config
		want string
	}{
		{Config{Host: "dns.example.test"}, "http://dns.example.test:5380"},
		{Config{Host: "dns.example.test", SSL: true}, "https://dns.example.test:53443"},
		{Config{Host: "dns.example.test", SSL: true, Port: 443}, "https://dns.example.test:443"},
		{Config{Host: "2001:db8::53"}, "http://[2001:db8::53]:5380"},
	} {
		c, err := New(&tc.cfg)
		require.NoError(t, err)
		assert.Equal(t, tc.want, c.Addr())
	}
}

func TestApps(t *testing.T) {
	f, c := newFake(t, "secret", map[string]string{"/api/apps/list": appsAnswer})

	apps, err := c.Apps(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []App{
		{Name: "Block Page", Version: "1.0", DNSApps: []DNSApp{{ClassPath: "BlockPageWebServer.App"}}},
		{Name: "Query Logs (Sqlite)", Version: "8.0", DNSApps: []DNSApp{{ClassPath: "QueryLogsSqlite.App", IsQueryLogger: true}}},
	}, apps)
	assert.Equal(t, "application/json", f.last.Header.Get("Accept"))
}

// TestQueryLogs checks the request for a page. It also checks that each
// field of an entry decodes.
func TestQueryLogs(t *testing.T) {
	f, c := newFake(t, "secret", map[string]string{"/api/logs/query": logsAnswer})

	page, err := c.QueryLogs(t.Context(), LogQuery{
		App:       "Query Logs (Sqlite)",
		ClassPath: "QueryLogsSqlite.App",
		Start:     time.Date(2026, 10, 11, 14, 30, 0, 123456700, time.FixedZone("IST", 5*3600+1800)),
		Page:      2,
		PerPage:   1000,
	})
	require.NoError(t, err)

	q := f.last.URL.Query()
	assert.Equal(t, "Query Logs (Sqlite)", q.Get("name"))
	assert.Equal(t, "QueryLogsSqlite.App", q.Get("classPath"))
	assert.Equal(t, "2", q.Get("pageNumber"))
	assert.Equal(t, "1000", q.Get("entriesPerPage"))
	assert.Equal(t, "false", q.Get("descendingOrder"))
	assert.Equal(t, "2026-10-11T09:00:00.1234567Z", q.Get("start"), "start is sent in UTC")

	assert.Equal(t, &LogPage{
		PageNumber:   1,
		TotalPages:   3,
		TotalEntries: 3,
		Entries: []LogEntry{{
			Timestamp:       "2026-10-11T09:00:00.1234567Z",
			ClientIPAddress: "192.0.2.10",
			Protocol:        "Udp",
			ResponseType:    "Recursive",
			RCode:           "NoError",
			QName:           "www.example.com",
			QType:           "A",
			QClass:          "IN",
			Answer:          "CNAME edge.example.net, A 203.0.113.5",
		}},
	}, page)
}

// TestQueryLogsDefaults checks that the client leaves a zero start and page
// size to the server. It also checks that a zero page asks for the first.
func TestQueryLogsDefaults(t *testing.T) {
	f, c := newFake(t, "secret", map[string]string{"/api/logs/query": logsAnswer})

	_, err := c.QueryLogs(t.Context(), LogQuery{App: "Query Logs (Sqlite)", ClassPath: "QueryLogsSqlite.App"})
	require.NoError(t, err)

	q := f.last.URL.Query()
	assert.Equal(t, "1", q.Get("pageNumber"))
	assert.False(t, q.Has("start"))
	assert.False(t, q.Has("entriesPerPage"))
}

// TestNullFields checks that a null field decodes as empty. The server writes
// null for a query with no answer.
func TestNullFields(t *testing.T) {
	_, c := newFake(t, "secret", map[string]string{"/api/logs/query": `{"response":{"pageNumber":1,"totalPages":1,"totalEntries":1,
		"entries":[{"timestamp":"2026-10-11T09:00:00Z","clientIpAddress":"192.0.2.10","qname":null,"answer":null}]},"status":"ok"}`})

	page, err := c.QueryLogs(t.Context(), LogQuery{})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Empty(t, page.Entries[0].QName)
	assert.Empty(t, page.Entries[0].Answer)
}

func TestErrors(t *testing.T) {
	answers := map[string]string{
		"/api/apps/list":  `{"status":"error","errorMessage":"Access was denied.","stackTrace":"..."}`,
		"/api/logs/query": `{"status":"2fa-required"}`,
	}

	t.Run("wrong token", func(t *testing.T) {
		_, c := newFake(t, "wrong", answers)

		_, err := c.Apps(t.Context())
		require.ErrorIs(t, err, ErrUnauthorized)
	})

	t.Run("error status", func(t *testing.T) {
		_, c := newFake(t, "secret", answers)

		_, err := c.Apps(t.Context())

		apiErr, ok := errors.AsType[*APIError](err)
		require.True(t, ok, err)
		assert.Equal(t, &APIError{Path: "/api/apps/list", Message: "Access was denied."}, apiErr)
	})

	t.Run("unknown status", func(t *testing.T) {
		_, c := newFake(t, "secret", answers)

		_, err := c.QueryLogs(t.Context(), LogQuery{})
		require.ErrorIs(t, err, ErrUnexpected)
	})

	t.Run("not the API", func(t *testing.T) {
		f, c := newFake(t, "secret", map[string]string{})

		_, err := c.Apps(t.Context())
		require.ErrorIs(t, err, ErrUnexpected, "a 404 page is not JSON")
		assert.Equal(t, "/api/apps/list", f.last.URL.Path)
	})

	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)
		srv.Close()

		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)

		c, err := New(&Config{Host: u.Hostname(), Port: port})
		require.NoError(t, err)

		_, err = c.Apps(t.Context())
		require.ErrorIs(t, err, ErrUnreachable)
	})

	t.Run("cancelled", func(t *testing.T) {
		_, c := newFake(t, "secret", answers)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := c.Apps(ctx)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrUnreachable, "a caller giving up is not the server being down")
	})

	t.Run("self-signed certificate", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.NotFoundHandler())
		t.Cleanup(srv.Close)

		u, err := url.Parse(srv.URL)
		require.NoError(t, err)

		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)

		c, err := New(&Config{Host: u.Hostname(), Port: port, SSL: true})
		require.NoError(t, err)

		_, err = c.Apps(t.Context())
		require.ErrorIs(t, err, ErrTLS)

		c, err = New(&Config{Host: u.Hostname(), Port: port, SSL: true, Insecure: true})
		require.NoError(t, err)

		_, err = c.Apps(t.Context())
		require.ErrorIs(t, err, ErrUnexpected, "Insecure gets past the certificate to the 404 behind it")
	})
}
