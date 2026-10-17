package pihole

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queriesAnswer is a page with the shape that FTL returns, with
// documentation addresses and names.
const queriesAnswer = `{"queries":[
	{"id":42,"time":1791709200.25,"type":"A","domain":"www.example.com","cname":null,"status":"FORWARDED",
	 "client":{"ip":"192.0.2.10","name":"host-a"},"dnssec":"INSECURE","reply":{"type":"IP","time":12.5},
	 "list_id":null,"upstream":"192.0.2.1#53","ede":{"code":-1,"text":null}},
	{"id":41,"time":1791709199,"type":"AAAA","domain":"ads.example.net","cname":null,"status":null,
	 "client":{"ip":"192.0.2.11","name":null},"dnssec":"UNKNOWN","reply":{"type":null,"time":-1},
	 "list_id":null,"upstream":null,"ede":{"code":-1,"text":null}}
],"cursor":42,"recordsTotal":2,"recordsFiltered":2,"draw":0,"took":0.001}`

// fakePiHole signs clients in with password. It answers /api/queries for a
// session that it gave out.
type fakePiHole struct {
	password string

	mu      sync.Mutex
	sid     string
	logins  int
	last    url.Values
	lastSID string
}

func (f *fakePiHole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.URL.Path {
	case "/api/auth":
		var body struct {
			Password string `json:"password"`
		}

		_ = json.UnmarshalRead(r.Body, &body)

		if body.Password != f.password {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"session":{"valid":false,"totp":false,"sid":null,"validity":-1,"message":"password incorrect"},"took":0.001}`))

			return
		}

		f.logins++
		f.sid = "sid-" + strconv.Itoa(f.logins)
		_, _ = w.Write([]byte(`{"session":{"valid":true,"totp":false,"sid":"` + f.sid + `","validity":1800,"message":"app-password correct"},"took":0.001}`))
	case "/api/queries":
		f.lastSID = r.Header.Get(sidHeader)

		if f.password != "" && (f.sid == "" || f.lastSID != f.sid) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"key":"unauthorized","message":"Unauthorized","hint":null},"took":0.001}`))

			return
		}

		f.last = r.URL.Query()
		_, _ = w.Write([]byte(queriesAnswer))
	default:
		http.NotFound(w, r)
	}
}

// newFake starts a Pi-hole with serverPassword. It returns the Pi-hole and a
// client that holds password.
func newFake(t *testing.T, serverPassword, password string) (*fakePiHole, *PiHole) {
	t.Helper()

	f := &fakePiHole{password: serverPassword}

	return f, clientFor(t, httptest.NewServer(f), password)
}

func clientFor(t *testing.T, srv *httptest.Server, password string) *PiHole {
	t.Helper()

	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	c, err := New(&Config{Host: u.Hostname(), Port: port, Password: password})
	require.NoError(t, err)

	return c
}

func TestNewRefusesNoHost(t *testing.T) {
	t.Parallel()

	_, err := New(&Config{})
	require.ErrorIs(t, err, ErrNoHost)
}

func TestNewPicksPortFromSSL(t *testing.T) {
	t.Parallel()

	plain, err := New(&Config{Host: "192.0.2.53"})
	require.NoError(t, err)
	assert.Equal(t, "http://192.0.2.53:80", plain.Addr())

	secure, err := New(&Config{Host: "192.0.2.53", SSL: true})
	require.NoError(t, err)
	assert.Equal(t, "https://192.0.2.53:443", secure.Addr())
}

// A page decodes, and the request sends what it asks for. Two calls share
// one sign-in.
func TestQueriesDecodesAPageOnOneSession(t *testing.T) {
	t.Parallel()

	f, c := newFake(t, "secret", "secret")
	from := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)

	page, err := c.Queries(t.Context(), QueryRequest{From: from, Cursor: 42, Start: 1000, Length: 1000})
	require.NoError(t, err)

	_, err = c.Queries(t.Context(), QueryRequest{})
	require.NoError(t, err)

	assert.Equal(t, 1, f.logins)
	assert.Equal(t, "sid-1", f.lastSID)

	require.Len(t, page.Queries, 2)

	q := page.Queries[0]
	assert.Equal(t, int64(42), q.ID)
	assert.Equal(t, "www.example.com", q.Domain)
	assert.Equal(t, "A", q.Type)
	assert.Equal(t, "FORWARDED", q.Status)
	assert.Equal(t, "192.0.2.10", q.Client.IP)
	assert.Equal(t, "IP", q.Reply.Type)
	assert.Equal(t, time.Date(2026, 10, 11, 9, 0, 0, 250_000_000, time.UTC), q.At())

	// FTL writes null for a query that is not complete.
	assert.Empty(t, page.Queries[1].Status)
	assert.Empty(t, page.Queries[1].Reply.Type)
}

func TestQueriesSendsTheRequest(t *testing.T) {
	t.Parallel()

	f, c := newFake(t, "secret", "secret")
	from := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)

	_, err := c.Queries(t.Context(), QueryRequest{From: from, Cursor: 42, Start: 1000, Length: 1000})
	require.NoError(t, err)

	assert.Equal(t, url.Values{
		"from":   {strconv.FormatInt(from.Unix(), 10)},
		"cursor": {"42"},
		"start":  {"1000"},
		"length": {"1000"},
	}, f.last)
}

// If a session ends between reads, the client signs in one more time. The
// read then succeeds.
func TestQueriesSignsInAgainWhenTheSessionLapses(t *testing.T) {
	t.Parallel()

	f, c := newFake(t, "secret", "secret")

	_, err := c.Queries(t.Context(), QueryRequest{})
	require.NoError(t, err)

	f.mu.Lock()
	f.sid = "ended"
	f.mu.Unlock()

	_, err = c.Queries(t.Context(), QueryRequest{})
	require.NoError(t, err)

	assert.Equal(t, 2, f.logins)
}

func TestQueriesRefusedPassword(t *testing.T) {
	t.Parallel()

	_, c := newFake(t, "secret", "wrong")

	_, err := c.Queries(t.Context(), QueryRequest{})
	require.ErrorIs(t, err, ErrUnauthorized)
}

// A Pi-hole with a password refuses a read with no password.
func TestQueriesNoPasswordAgainstOneThatWantsIt(t *testing.T) {
	t.Parallel()

	_, c := newFake(t, "secret", "")

	_, err := c.Queries(t.Context(), QueryRequest{})
	require.ErrorIs(t, err, ErrUnauthorized)
}

// The client reads a Pi-hole with no password, and does not sign in.
func TestQueriesWithoutAPassword(t *testing.T) {
	t.Parallel()

	f, c := newFake(t, "", "")

	_, err := c.Queries(t.Context(), QueryRequest{})
	require.NoError(t, err)

	assert.Zero(t, f.logins)
	assert.Empty(t, f.lastSID)
}

// When all session seats are in use, the server refuses with its own error.
// The error tells what to change.
func TestQueriesSeatsTaken(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"key":"api_seats_exceeded","message":"API seats exceeded",` +
			`"hint":"increase webserver.api.max_sessions"},"took":0.001}`))
	}))

	_, err := clientFor(t, srv, "secret").Queries(t.Context(), QueryRequest{})

	apiErr, ok := errors.AsType[*APIError](err)
	require.True(t, ok, "got %v", err)
	assert.Equal(t, "api_seats_exceeded", apiErr.Key)
	assert.Contains(t, err.Error(), "increase webserver.api.max_sessions")
}

// A different service on the port, such as a Pi-hole v5 web page, is
// unexpected.
func TestQueriesSomethingElseOnThePort(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth" {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write([]byte(`<html>admin</html>`))
	}))

	_, err := clientFor(t, srv, "").Queries(t.Context(), QueryRequest{})
	require.ErrorIs(t, err, ErrUnexpected)

	_, err = clientFor(t, httptest.NewServer(http.NotFoundHandler()), "secret").Queries(t.Context(), QueryRequest{})
	require.ErrorIs(t, err, ErrUnexpected)
}

func TestQueriesUnreachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	c := clientFor(t, srv, "")
	srv.Close()

	_, err := c.Queries(t.Context(), QueryRequest{})
	require.ErrorIs(t, err, ErrUnreachable)
}
