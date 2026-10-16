package adguard

import (
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

// logAnswer is a page with the shape that AdGuard Home returns, with
// documentation addresses and names.
const logAnswer = `{"data":[
	{"answer":[{"type":"CNAME","value":"edge.example.net.","ttl":300},{"type":"A","value":"203.0.113.5","ttl":300}],
	 "answer_dnssec":false,"cached":false,"client":"192.0.2.10","client_info":{"whois":{},"name":"host-a","disallowed_rule":"","disallowed":false},
	 "client_proto":"","elapsedMs":"12.5","question":{"class":"IN","name":"www.example.com","type":"A"},
	 "reason":"NotFilteredNotFound","rules":[],"status":"NOERROR","time":"2026-10-11T09:00:00.123456789Z","upstream":"192.0.2.1:53"},
	{"answer":[{"type":"A","value":"0.0.0.0","ttl":10}],"cached":false,"client":"192.0.2.11",
	 "elapsedMs":"0.1","question":{"class":"IN","name":"ads.example.net","type":"A"},
	 "reason":"FilteredBlackList","rules":[{"filter_list_id":1,"text":"||ads.example.net^"}],"status":"NOERROR","time":"2026-10-11T08:59:59Z"}
],"oldest":"2026-10-11T08:59:59Z"}`

// fakeServer answers the query log for user and password. It records the
// last request, so a test can check what the client asked.
type fakeServer struct {
	user, password string
	last           *http.Request
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.last = r

	if f.user != "" {
		if u, p, ok := r.BasicAuth(); !ok || u != f.user || p != f.password {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
	}

	if r.URL.Path != "/control/querylog" {
		http.NotFound(w, r)

		return
	}

	_, _ = w.Write([]byte(logAnswer))
}

// clientFor returns a client for srv that logs in as user with password.
func clientFor(t *testing.T, srv *httptest.Server, user, password string) *AdGuard {
	t.Helper()

	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	c, err := New(&Config{Host: u.Hostname(), Port: port, User: user, Password: password})
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

// A page decodes. The request has the login and what it asks for.
func TestQueryLogDecodesAPage(t *testing.T) {
	t.Parallel()

	f := &fakeServer{user: "jocasta", password: "secret"}
	c := clientFor(t, httptest.NewServer(f), "jocasta", "secret")
	older := time.Date(2026, 10, 11, 9, 0, 0, 500, time.UTC)

	page, err := c.QueryLog(t.Context(), LogRequest{OlderThan: older, Limit: 1000})
	require.NoError(t, err)

	assert.Equal(t, url.Values{"older_than": {"2026-10-11T09:00:00.0000005Z"}, "limit": {"1000"}}, f.last.URL.Query())

	require.Len(t, page.Data, 2)

	e := page.Data[0]
	assert.Equal(t, "2026-10-11T09:00:00.123456789Z", e.Time)
	assert.Equal(t, "192.0.2.10", e.Client)
	assert.Equal(t, Question{Name: "www.example.com", Type: "A"}, e.Question)
	assert.Equal(t, []Answer{{Type: "CNAME", Value: "edge.example.net."}, {Type: "A", Value: "203.0.113.5"}}, e.Answer)
	assert.Equal(t, "NotFilteredNotFound", e.Reason)
	assert.Equal(t, "NOERROR", e.Status)

	assert.Equal(t, "FilteredBlackList", page.Data[1].Reason)
}

// The client reads a server with no login, and sends no login.
func TestQueryLogWithoutALogin(t *testing.T) {
	t.Parallel()

	f := &fakeServer{}

	_, err := clientFor(t, httptest.NewServer(f), "", "").QueryLog(t.Context(), LogRequest{})
	require.NoError(t, err)

	_, _, ok := f.last.BasicAuth()
	assert.False(t, ok)
	assert.Empty(t, f.last.URL.RawQuery)
}

func TestQueryLogRefusedLogin(t *testing.T) {
	t.Parallel()

	f := &fakeServer{user: "jocasta", password: "secret"}

	_, err := clientFor(t, httptest.NewServer(f), "jocasta", "wrong").QueryLog(t.Context(), LogRequest{})
	require.ErrorIs(t, err, ErrUnauthorized)
}

// If the server gives its own text for a refusal, the error keeps that
// text.
func TestQueryLogRefused(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "reading query log", http.StatusInternalServerError)
	}))

	_, err := clientFor(t, srv, "jocasta", "secret").QueryLog(t.Context(), LogRequest{})

	apiErr, ok := errors.AsType[*APIError](err)
	require.True(t, ok, "got %v", err)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Status)
	assert.Equal(t, "reading query log", apiErr.Message)
}

// A different service on the port is unexpected.
func TestQueryLogSomethingElseOnThePort(t *testing.T) {
	t.Parallel()

	_, err := clientFor(t, httptest.NewServer(http.NotFoundHandler()), "", "").QueryLog(t.Context(), LogRequest{})
	require.ErrorIs(t, err, ErrUnexpected)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>router</html>`))
	}))

	_, err = clientFor(t, srv, "", "").QueryLog(t.Context(), LogRequest{})
	require.ErrorIs(t, err, ErrUnexpected)
}

func TestQueryLogUnreachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	c := clientFor(t, srv, "", "")
	srv.Close()

	_, err := c.QueryLog(t.Context(), LogRequest{})
	require.ErrorIs(t, err, ErrUnreachable)
}
