package openwrt

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRouter answers ubus JSON-RPC the way uhttpd and rpcd do, from canned
// answers keyed by "object method". A login with the right password opens a
// session, and a call on any other session is refused as rpcd refuses it.
type fakeRouter struct {
	password string

	// answers holds what each "object method" returns: the raw JSON of the
	// result array, such as `[0,{"hostname":"OpenWrt"}]`.
	answers map[string]string

	// refused are the "object method" calls the login's ACL does not grant,
	// which rpcd refuses on a live session as it refuses an expired one.
	refused map[string]bool

	mu       sync.Mutex
	sessions map[string]bool
	logins   atomic.Int32
	calls    []string
}

// newFakeRouter starts a fake router answering with answers, and returns it
// with a client logged in with the right password and the server's URL.
func newFakeRouter(t *testing.T, answers map[string]string) (*fakeRouter, *OpenWrt, string) {
	t.Helper()

	f := &fakeRouter{password: "secret", answers: answers, sessions: map[string]bool{}}

	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	return f, clientFor(t, srv.URL, "secret"), srv.URL
}

// clientFor builds a client for the server at raw with password.
func clientFor(t *testing.T, raw, password string) *OpenWrt {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	o, err := New(&Config{Host: u.Hostname(), Port: port, User: "jocasta", Password: password}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	return o
}

// expire drops every session, as rpcd does after five idle minutes.
func (f *fakeRouter) expire() {
	f.mu.Lock()
	defer f.mu.Unlock()

	clear(f.sessions)
}

func (f *fakeRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != endpoint {
		http.NotFound(w, r)

		return
	}

	var req struct {
		Params []jsontext.Value `json:"params"`
	}

	if err := json.UnmarshalRead(r.Body, &req); err != nil || len(req.Params) != 4 {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	var session, object, method string

	_ = json.Unmarshal(req.Params[0], &session)
	_ = json.Unmarshal(req.Params[1], &object)
	_ = json.Unmarshal(req.Params[2], &method)

	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, object+" "+method)

	if object == "session" && method == "login" {
		f.logins.Add(1)

		var args struct {
			Password string `json:"password"`
		}

		_ = json.Unmarshal(req.Params[3], &args)

		if args.Password != f.password {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[6]}`))

			return
		}

		id := strconv.Itoa(len(f.sessions)+1) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		f.sessions[id] = true

		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[0,{"ubus_rpc_session":"` + id + `","timeout":300}]}`))

		return
	}

	if !f.sessions[session] {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32002,"message":"Access denied"}}`))

		return
	}

	key := object + " " + method

	if f.refused[key] {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32002,"message":"Access denied"}}`))

		return
	}

	// A command is keyed by what it runs, so two commands answer apart.
	if key == "file exec" {
		var args struct {
			Command string   `json:"command"`
			Params  []string `json:"params"`
		}

		_ = json.Unmarshal(req.Params[3], &args)

		key = "exec " + args.Command
		for _, p := range args.Params {
			key += " " + p
		}

		if f.refused[key] {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[6]}`))

			return
		}
	}

	// uci get is keyed by the config and the section type it asks for.
	if key == "uci get" {
		var args struct {
			Config string `json:"config"`
			Type   string `json:"type"`
		}

		_ = json.Unmarshal(req.Params[3], &args)

		key = "uci " + args.Config + " " + args.Type
	}

	// getDHCPLeases is keyed by the family it asks for.
	if key == "luci-rpc getDHCPLeases" {
		var args struct {
			Family int `json:"family"`
		}

		_ = json.Unmarshal(req.Params[3], &args)

		key += " " + strconv.Itoa(args.Family)
	}

	answer, ok := f.answers[key]
	if !ok {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"Object not found"}}`))

		return
	}

	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + answer + `}`))
}

const boardAnswer = `[0,{"hostname":"OpenWrt","model":"GL.iNet GL-MT6000",` +
	`"release":{"version":"24.10.4","description":"OpenWrt 24.10.4 r28959-29397011cc"}}]`

func TestVerifyLogsInAndReadsTheBoard(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{"system board": boardAnswer})

	b, err := o.Verify(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "OpenWrt", b.Hostname)
	assert.Equal(t, "GL.iNet GL-MT6000", b.Model)
	assert.Equal(t, "24.10.4", b.Release.Version)
	assert.Equal(t, int32(1), f.logins.Load())
}

// Calls share one session until the router expires it, and then the client
// logs in again by itself.
func TestCallsShareASessionAndRenewIt(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{"system board": boardAnswer})

	for range 3 {
		_, err := o.Board(t.Context())
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), f.logins.Load())

	f.expire()

	_, err := o.Board(t.Context())
	require.NoError(t, err)

	assert.Equal(t, int32(2), f.logins.Load())
}

func TestAWrongPasswordIsUnauthorized(t *testing.T) {
	t.Parallel()

	_, _, raw := newFakeRouter(t, map[string]string{"system board": boardAnswer})

	_, err := clientFor(t, raw, "wrong").Verify(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)
}

// A call refused on a session that was just opened is one the login's ACL does
// not grant, which no retry will change, so the client does not log in again.
func TestACallRefusedOnAFreshSessionIsUnauthorized(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{"system board": boardAnswer})
	f.refused = map[string]bool{"system board": true}

	_, err := o.Board(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)

	assert.Equal(t, int32(1), f.logins.Load())
}

// rpcd refuses an expired session and a call the ACL does not grant alike. The
// first refusal mid-session is taken as expiry and costs one more login; once
// the fresh session is refused too, the call is remembered, and refusing it
// again keeps the session.
func TestACallTheACLRefusesKeepsTheSession(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{
		"system board":           boardAnswer,
		"luci-rpc getDHCPLeases": `[0,{"dhcp_leases":[]}]`,
	})
	f.refused = map[string]bool{"luci-rpc getDHCPLeases": true}

	_, err := o.Board(t.Context())
	require.NoError(t, err)

	for range 3 {
		_, err = o.DHCPLeases(t.Context())
		require.ErrorIs(t, err, ErrUnauthorized)
	}

	_, err = o.Board(t.Context())
	require.NoError(t, err)

	assert.Equal(t, int32(2), f.logins.Load())
}

// The ACL grants a command, not every command, so one refused command leaves
// the others working on the same session.
func TestACommandTheACLRefusesLeavesTheOthers(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{
		"exec /sbin/ip -4 neigh show": `[0,{"code":0,"stdout":"192.0.2.10 dev br-lan lladdr 00:00:5e:00:53:01 REACHABLE\n"}]`,
	})
	f.refused = map[string]bool{"exec /sbin/ip -6 neigh show": true}

	for range 2 {
		got, err := o.Neighbours(t.Context())
		require.ErrorIs(t, err, ErrUnauthorized)
		assert.Len(t, got, 1)
	}

	assert.Equal(t, int32(1), f.logins.Load())
}

// clock is a time a test moves by hand.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = c.at.Add(d)
}

// withClock points o at a clock the test moves.
func withClock(o *OpenWrt) *clock {
	c := &clock{at: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	o.now = c.now

	return c
}

// A sweep runs further apart than rpcd's idle timeout, so a remembered refusal
// that opens the next sweep meets an expired session. It is retried on a new
// one, so an ACL fixed between sweeps works then.
func TestARefusalAfterTheSessionTimeoutIsRetried(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{
		"system board":             boardAnswer,
		"luci-rpc getDHCPLeases 4": `[0,{"dhcp_leases":[]}]`,
		"luci-rpc getDHCPLeases 6": `[0,{"dhcp6_leases":[]}]`,
	})
	f.refused = map[string]bool{"luci-rpc getDHCPLeases": true}

	c := withClock(o)

	_, err := o.Board(t.Context())
	require.NoError(t, err)

	_, err = o.DHCPLeases(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)

	// The next sweep, after the ACL is fixed.
	c.advance(5 * time.Minute)
	f.expire()

	f.mu.Lock()
	f.refused = nil
	f.mu.Unlock()

	_, err = o.DHCPLeases(t.Context())
	require.NoError(t, err)
}

// A login whose every call is refused never has a call work, and must still
// log in again once its session times out.
func TestASessionThatServedNothingIsRenewedAfterItsTimeout(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{"system board": boardAnswer})
	f.refused = map[string]bool{"system board": true}

	c := withClock(o)

	_, err := o.Board(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)

	c.advance(5 * time.Minute)
	f.expire()

	f.mu.Lock()
	f.refused = nil
	f.mu.Unlock()

	_, err = o.Board(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int32(2), f.logins.Load())
}

// Once the ACL grants a call it once refused, the call works without a
// restart.
func TestACallTheACLGrantsLaterWorks(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{"system board": boardAnswer})
	f.refused = map[string]bool{"system board": true}

	_, err := o.Board(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)

	f.mu.Lock()
	f.refused = nil
	f.mu.Unlock()

	_, err = o.Board(t.Context())
	require.NoError(t, err)
}

func TestAMissingObjectIsNotFound(t *testing.T) {
	t.Parallel()

	_, o, _ := newFakeRouter(t, map[string]string{})

	_, err := o.Board(t.Context())
	require.ErrorIs(t, err, ErrNotFound)
}

// A router without uhttpd-mod-ubus answers /ubus with a 404.
func TestNoUbusEndpointIsNotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	_, err := clientFor(t, srv.URL, "secret").Verify(t.Context())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestARouterThatIsDownIsUnreachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	raw := srv.URL
	srv.Close()

	_, err := clientFor(t, raw, "secret").Verify(t.Context())
	require.ErrorIs(t, err, ErrUnreachable)
}

// Something else answering the port is not mistaken for a router.
func TestAnAnswerThatIsNotUbusIsUnexpected(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>hello</html>"))
	}))
	t.Cleanup(srv.Close)

	_, err := clientFor(t, srv.URL, "secret").Verify(t.Context())
	require.ErrorIs(t, err, ErrUnexpected)
}

func TestNewRefusesNoHost(t *testing.T) {
	t.Parallel()

	_, err := New(&Config{}, nil)
	require.ErrorIs(t, err, ErrNoHost)
}

func TestNewPicksTheSchemeAndPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cfg  Config
		want string
	}{
		{Config{Host: "192.0.2.1"}, "http://192.0.2.1:80/ubus"},
		{Config{Host: "192.0.2.1", SSL: true}, "https://192.0.2.1:443/ubus"},
		{Config{Host: "router.lan", Port: 8080}, "http://router.lan:8080/ubus"},
		{Config{Host: "2001:db8::1", SSL: true}, "https://[2001:db8::1]:443/ubus"},
	}

	for _, tt := range tests {
		o, err := New(&tt.cfg, nil)
		require.NoError(t, err)

		assert.Equal(t, tt.want, o.Addr())
	}
}
