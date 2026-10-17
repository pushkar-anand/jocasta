// Package openwrt reads the state of an OpenWrt router over ubus. ubus is the
// message bus on which its daemons publish their state.
//
// uhttpd serves ubus as JSON-RPC at /ubus. The pages of LuCI call this
// endpoint. So every router with LuCI answers it, and needs nothing more. A
// call names a session, an object, a method and its arguments. A session
// comes from a login. rpcd checks every call against the ACL groups of the
// login.
//
// The package only reads. It never writes. The router is a source of facts
// about the network. A client that cannot change its configuration cannot
// break the network by a mistake.
//
// Values stay as the router renders them. Addresses and hardware addresses
// stay strings on purpose. If one row is malformed, the caller loses only that
// row. The caller decides what to do with it.
package openwrt

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	// endpoint is the path on which uhttpd serves ubus.
	endpoint = "/ubus"

	defaultPort    = 80
	defaultSSLPort = 443
	defaultTimeout = 10 * time.Second

	// maxBody limits the size of a response. A router does not send more than
	// a few hundred kilobytes of leases. A device on this port that is not a
	// router can send data forever.
	maxBody = 8 << 20

	// anonymous is the session for a login call, before a session exists.
	anonymous = "00000000000000000000000000000000"

	// defaultSessionTimeout is how long rpcd keeps an idle session, when a
	// login does not tell. It is the default of rpcd.
	defaultSessionTimeout = 5 * time.Minute

	// sessionMargin comes off the timeout of a session before the client
	// trusts that the session is live. rpcd counts from the last call that it
	// saw. This client counts from the answer that it read, which arrives a
	// little later.
	sessionMargin = 10 * time.Second
)

type (
	// Config says which router to read and how to reach it.
	Config struct {
		// Host is the router's address or name, without a port.
		Host string

		// Port defaults to 80, or 443 when SSL is set.
		Port int

		// User and Password are an rpcd login, from /etc/config/rpcd.
		User     string
		Password string

		// SSL selects https. uhttpd serves https when luci-ssl is installed.
		SSL bool

		// Insecure skips certificate verification. uhttpd makes a self-signed
		// certificate, unless you install one. So the usual setup cannot
		// connect over https without this setting.
		Insecure bool

		// Timeout limits each request on its own. The default is 10s. Only the
		// context of the caller limits a whole read.
		Timeout time.Duration
	}

	// OpenWrt is a read-only client for one router. It is safe for concurrent
	// use.
	OpenWrt struct {
		cfg    *Config
		client *http.Client
		logger *slog.Logger
		url    string

		// mu guards session and refused. Every call shares session until the
		// router expires it. refused is the calls that the ACL of the login
		// does not allow.
		mu      sync.Mutex
		session string
		refused map[string]bool

		// used is when a call last worked on session. idle is how long rpcd
		// keeps the session after that. Together they tell whether session is
		// live.
		used time.Time
		idle time.Duration

		// opened is a new session on which no call worked yet. A refusal on it
		// comes from the ACL, because it cannot be expired.
		opened string

		// now is a field, so tests can move the clock.
		now func() time.Time
	}
)

// ErrNoHost is a config that names no router.
var ErrNoHost = errors.New("openwrt: no host configured")

// New builds a client for the router that cfg names.
//
// It does no I/O. So if the router is down at startup, the server still starts
// and tries again later. To find out whether the credentials and the service
// work, call [OpenWrt.Verify].
func New(cfg *Config, log *slog.Logger) (*OpenWrt, error) {
	if cfg == nil || cfg.Host == "" {
		return nil, ErrNoHost
	}

	if log == nil {
		log = slog.Default()
	}

	c := *cfg

	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}

	scheme := "http"
	port := defaultPort

	if c.SSL {
		scheme = "https"
		port = defaultSSLPort
	}

	if c.Port != 0 {
		port = c.Port
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()

	if c.Insecure {
		// Clone copies the default TLS settings, but it can leave the TLS
		// config nil.
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{} //nolint:gosec // the next line is the point.
		}

		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	u := url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(port)),
		Path:   endpoint,
	}

	return &OpenWrt{
		cfg:    &c,
		client: &http.Client{Transport: transport, Timeout: c.Timeout},
		logger: log,
		url:    u.String(),
		now:    time.Now,
	}, nil
}

// Addr is the URL that this client calls. Use it in a log line to name the
// router that answered.
func (o *OpenWrt) Addr() string { return o.url }

// call runs method on object with args, and decodes what it returns into T.
//
// If there is no session, call logs in first. rpcd gives the same refusal for
// an expired session and for a call that the ACL of the login does not allow.
// rpcd expires a session after five idle minutes. This is shorter than the
// interval of a sweep. So the first call of a sweep usually finds an expired
// session.
//
// call takes a refusal as expiry. It logs in one more time and tries again.
// It does not do this if it knows that the session is live. A session is live
// if it was just opened. It is also live if a call worked on it within its
// timeout, and the server refused this call before. A refusal on a live
// session means that the ACL does not allow the call. call returns
// [ErrUnauthorized] and remembers the refusal. Each sweep still makes the call
// on a live session at least once. So a fixed ACL works without a restart.
func call[T any](ctx context.Context, o *OpenWrt, object, method string, args any) (*T, error) {
	key, err := callKey(object, method, args)
	if err != nil {
		return nil, err
	}

	session, live, err := o.currentSession(ctx)
	if err != nil {
		return nil, err
	}

	data, err := o.do(ctx, session, object, method, args)

	// On a live session, a refusal comes from the ACL in two cases. In the
	// first, the session served no call yet, so it cannot be expired. In the
	// second, the server refused the call before. Any other refusal can be
	// expiry.
	byACL := live && (o.isFresh(session) || o.isRefused(key))

	if errors.Is(err, errAccessDenied) && !byACL {
		o.forget(session)

		if session, _, err = o.currentSession(ctx); err != nil {
			return nil, err
		}

		data, err = o.do(ctx, session, object, method, args)
	}

	if errors.Is(err, errAccessDenied) {
		o.refuse(key)

		return nil, fmt.Errorf("%w: %w", ErrUnauthorized, err)
	}

	if err != nil {
		return nil, err
	}

	o.worked(session, key)

	var t T

	// A call that succeeds with no data returns only the status. An empty
	// table can do this.
	if len(data) == 0 {
		return &t, nil
	}

	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("unmarshal %s %s: %w", object, method, err)
	}

	return &t, nil
}

// callKey names a call as the ACL of rpcd allows it: by object and method.
// For the file object, it also uses the arguments. There, the ACL allows a
// path or a command. It can allow one `file exec` command and refuse
// another.
func callKey(object, method string, args any) (string, error) {
	if object != "file" {
		return object + " " + method, nil
	}

	b, err := json.Marshal(args, json.Deterministic(true))
	if err != nil {
		return "", fmt.Errorf("request encode: %w", err)
	}

	return object + " " + method + " " + string(b), nil
}

// currentSession returns the session that calls share. If there is none, it
// logs in. live reports whether the session is known to be live. A live
// session was just opened, or was used within its timeout.
func (o *OpenWrt) currentSession(ctx context.Context) (session string, live bool, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.session != "" {
		return o.session, o.now().Sub(o.used) < o.idle-sessionMargin, nil
	}

	session, idle, err := o.login(ctx)
	if err != nil {
		return "", false, err
	}

	o.session = session
	o.idle = idle
	o.used = o.now()
	o.opened = session

	return session, true, nil
}

// isFresh reports whether session was opened for the current call, and served
// no call since. If so, a refusal on it cannot be expiry.
func (o *OpenWrt) isFresh(session string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.opened == session
}

// forget removes session, unless another call already replaced it.
func (o *OpenWrt) forget(session string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.session == session {
		o.session = ""
	}
}

// isRefused reports whether the ACL refused the call that key names, on a
// fresh session.
func (o *OpenWrt) isRefused(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.refused[key]
}

// refuse remembers that the ACL refuses the call that key names.
func (o *OpenWrt) refuse(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.refused == nil {
		o.refused = make(map[string]bool)
	}

	o.refused[key] = true
}

// worked records that the call that key names worked on session. The session
// is live from now. The ACL allows the call, even if it refused it before.
func (o *OpenWrt) worked(session, key string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.session == session {
		o.used = o.now()
	}

	if o.opened == session {
		o.opened = ""
	}

	delete(o.refused, key)
}

// login asks rpcd for a session. It returns the session and how long rpcd
// keeps it idle. A wrong user or password returns a permission status, which
// is [ErrUnauthorized].
func (o *OpenWrt) login(ctx context.Context) (string, time.Duration, error) {
	data, err := o.do(ctx, anonymous, "session", "login", map[string]string{
		"username": o.cfg.User,
		"password": o.cfg.Password,
	})
	if err != nil {
		return "", 0, fmt.Errorf("login: %w", err)
	}

	var res struct {
		Session string `json:"ubus_rpc_session"`
		Timeout int    `json:"timeout"`
	}

	if err := json.Unmarshal(data, &res); err != nil || res.Session == "" {
		return "", 0, fmt.Errorf("login: %w: no session in the answer", ErrUnexpected)
	}

	idle := defaultSessionTimeout
	if res.Timeout > 0 {
		idle = time.Duration(res.Timeout) * time.Second
	}

	return res.Session, idle, nil
}

// request is one JSON-RPC call to ubus.
type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// response is the answer of ubus. Result holds the ubus status, then the data
// if there is data. If the call did not reach the object, Error is set
// instead.
type response struct {
	Result []jsontext.Value `json:"result"`
	Error  *rpcError        `json:"error"`
}

// do sends one call and returns the data of the answer.
//
// If do cannot reach the router at all, it returns [ErrUnreachable]. If the
// certificate does not verify, it returns [ErrTLS]. If the router answers with
// an error, do returns a [StatusError] or a JSON-RPC error with the reason.
func (o *OpenWrt) do(ctx context.Context, session, object, method string, args any) (jsontext.Value, error) {
	if args == nil {
		args = struct{}{}
	}

	body, err := json.Marshal(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "call",
		Params:  []any{session, object, method, args},
	})
	if err != nil {
		return nil, fmt.Errorf("request encode: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request create: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		// A cancelled context means that the caller stopped. If the client
		// called it unreachable, the poller would try again during a shutdown.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("request send: %w", err)
		}

		if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
			return nil, fmt.Errorf("%w: %w", ErrTLS, err)
		}

		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}

	defer func() { _ = resp.Body.Close() }()

	// uhttpd answers a 404 for a path that it does not serve. An example is
	// /ubus on a router without uhttpd-mod-ubus.
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s answered 404; is uhttpd-mod-ubus installed?", ErrNotFound, o.url)
	}

	var r response

	if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxBody), &r); err != nil {
		return nil, fmt.Errorf("%w: %s %s: status %d, %w", ErrUnexpected, object, method, resp.StatusCode, err)
	}

	if r.Error != nil {
		return nil, r.Error.err(object, method)
	}

	if len(r.Result) == 0 {
		return nil, fmt.Errorf("%w: %s %s answered with no result", ErrUnexpected, object, method)
	}

	var status int
	if err := json.Unmarshal(r.Result[0], &status); err != nil {
		return nil, fmt.Errorf("%w: %s %s: status: %w", ErrUnexpected, object, method, err)
	}

	if status != 0 {
		return nil, &StatusError{Object: object, Method: method, Code: status}
	}

	if len(r.Result) < 2 {
		return nil, nil
	}

	return r.Result[1], nil
}
