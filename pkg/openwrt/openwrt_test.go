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

// fakeRouter answers ubus JSON-RPC as uhttpd and rpcd do. Its canned answers
// have "object method" as key. A login with the right password opens a
// session. It refuses a call on any other session, as rpcd does.
type fakeRouter struct {
	password string

	// answers holds what each "object method" returns. This is the raw JSON
	// of the result array, such as `[0,{"hostname":"OpenWrt"}]`.
	answers map[string]string

	// refused are the "object method" calls that the ACL of the login does
	// not allow. rpcd refuses them on a live session, as on an expired one.
	refused map[string]bool

	mu       sync.Mutex
	sessions map[string]bool
	logins   atomic.Int32
	calls    []string
}

// newFakeRouter starts a fake router that replies with answers. It returns the
// router, a client that logs in with the right password, and the URL of the
// server.
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

// expire removes every session, as rpcd does after five idle minutes.
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

	// The key of a command is what it runs. So two commands get separate
	// answers.
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

	// The key of a file read is its path.
	if key == "file read" {
		var args struct {
			Path string `json:"path"`
		}

		_ = json.Unmarshal(req.Params[3], &args)

		key = "read " + args.Path
	}

	// The key of uci get is the config and the section type that it asks
	// for.
	if key == "uci get" {
		var args struct {
			Config string `json:"config"`
			Type   string `json:"type"`
		}

		_ = json.Unmarshal(req.Params[3], &args)

		key = "uci " + args.Config + " " + args.Type
	}

	// The key of getDHCPLeases is the family that it asks for.
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

// Calls share one session until the router expires it. Then the client logs
// in again by itself.
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

// If the router refuses a call on a new session, the ACL of the login does not
// allow the call. A retry does not change this. So the client does not log in
// again.
func TestACallRefusedOnAFreshSessionIsUnauthorized(t *testing.T) {
	t.Parallel()

	f, o, _ := newFakeRouter(t, map[string]string{"system board": boardAnswer})
	f.refused = map[string]bool{"system board": true}

	_, err := o.Board(t.Context())
	require.ErrorIs(t, err, ErrUnauthorized)

	assert.Equal(t, int32(1), f.logins.Load())
}

// rpcd gives the same refusal for an expired session and for a call that the
// ACL does not allow. The client takes the first refusal during a session as
// expiry, and logs in one more time. If rpcd also refuses the new session, the
// client remembers the call. A later refusal of that call keeps the
// session.
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

// The ACL allows a command, not every command. So if rpcd refuses one command,
// the other commands still work on the same session.
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

// withClock points o at a clock that the test moves.
func withClock(o *OpenWrt) *clock {
	c := &clock{at: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	o.now = c.now

	return c
}

// The interval between sweeps is longer than the idle timeout of rpcd. So a
// remembered refusal at the start of the next sweep finds an expired session.
// The client tries again on a new session. So an ACL that is fixed between
// sweeps then works.
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

// If rpcd refuses every call of a login, no call works. The client must still
// log in again when its session times out.
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

// When the ACL allows a call that it refused before, the call works without a
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

// The client does not take a different service on the port for a router.
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
