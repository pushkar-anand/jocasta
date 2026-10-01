// Package openwrt reads an OpenWrt router's state over ubus, the message bus
// its daemons publish their state on.
//
// uhttpd serves ubus as JSON-RPC at /ubus, which is what LuCI's own pages call,
// so any router with LuCI installed answers it with nothing added. A call
// names a session, an object, a method and its arguments; a session comes from
// logging in, and rpcd checks every call against the ACL groups the login was
// granted.
//
// Everything here reads; nothing writes. The router is a source of facts about
// the network, and a client that cannot change its configuration cannot break
// the network by being wrong.
//
// Values arrive as the router renders them. Addresses and hardware addresses
// stay strings on purpose: a router with one malformed row should cost the
// caller only that row, and the caller decides what to do with it.
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
	// endpoint is the path uhttpd serves ubus on.
	endpoint = "/ubus"

	defaultPort    = 80
	defaultSSLPort = 443
	defaultTimeout = 10 * time.Second

	// maxBody caps a response. A router will not send more than a few hundred
	// kilobytes of leases, and a device answering on this port that is not a
	// router can stream forever.
	maxBody = 8 << 20

	// anonymous is the session a login is called with, before there is one.
	anonymous = "00000000000000000000000000000000"
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

		// SSL selects https, which uhttpd serves once luci-ssl is installed.
		SSL bool

		// Insecure skips certificate verification. uhttpd generates a
		// self-signed certificate unless one is installed, so without this the
		// common setup cannot connect over https at all.
		Insecure bool

		// Timeout bounds each request on its own, defaulting to 10s, so a
		// caller's context stays the only thing that bounds a whole read.
		Timeout time.Duration
	}

	// OpenWrt is a read-only client for one router. It is safe for concurrent
	// use.
	OpenWrt struct {
		cfg    *Config
		client *http.Client
		logger *slog.Logger
		url    string

		// mu guards session, which every call shares until the router expires
		// it.
		mu      sync.Mutex
		session string
	}
)

// ErrNoHost is a config naming no router.
var ErrNoHost = errors.New("openwrt: no host configured")

// New builds a client for the router cfg names.
//
// It performs no I/O, so a router that is down at startup is retried later
// and the server still starts. Call [OpenWrt.Verify] to find out whether the
// credentials and the service are actually good.
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
		// Clone carries the default TLS settings over, but not necessarily a
		// config to hang this on.
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
	}, nil
}

// Addr is the URL this client calls, useful in a log line naming which router
// answered.
func (o *OpenWrt) Addr() string { return o.url }

// call runs method on object with args and decodes what it returns into T.
//
// It logs in first when there is no session, and once more when the router
// says the session is gone: rpcd expires a session after five idle minutes,
// which is shorter than a sweep's interval. A call refused again on a fresh
// session is one the login is not allowed to make.
func call[T any](ctx context.Context, o *OpenWrt, object, method string, args any) (*T, error) {
	session, err := o.currentSession(ctx)
	if err != nil {
		return nil, err
	}

	data, err := o.do(ctx, session, object, method, args)
	if errors.Is(err, errAccessDenied) {
		o.forget(session)

		if session, err = o.currentSession(ctx); err != nil {
			return nil, err
		}

		data, err = o.do(ctx, session, object, method, args)

		if errors.Is(err, errAccessDenied) {
			return nil, fmt.Errorf("%w: %w", ErrUnauthorized, err)
		}
	}

	if err != nil {
		return nil, err
	}

	var t T

	// A call that succeeds with nothing to say, as an empty table can,
	// answers with the status alone.
	if len(data) == 0 {
		return &t, nil
	}

	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("unmarshal %s %s: %w", object, method, err)
	}

	return &t, nil
}

// currentSession returns the session calls share, logging in when there is
// none.
func (o *OpenWrt) currentSession(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.session != "" {
		return o.session, nil
	}

	session, err := o.login(ctx)
	if err != nil {
		return "", err
	}

	o.session = session

	return session, nil
}

// forget drops session, unless another call already replaced it.
func (o *OpenWrt) forget(session string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.session == session {
		o.session = ""
	}
}

// login asks rpcd for a session. A wrong user or password answers with a
// permission status, which is [ErrUnauthorized].
func (o *OpenWrt) login(ctx context.Context) (string, error) {
	data, err := o.do(ctx, anonymous, "session", "login", map[string]string{
		"username": o.cfg.User,
		"password": o.cfg.Password,
	})
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}

	var res struct {
		Session string `json:"ubus_rpc_session"`
	}

	if err := json.Unmarshal(data, &res); err != nil || res.Session == "" {
		return "", fmt.Errorf("login: %w: no session in the answer", ErrUnexpected)
	}

	return res.Session, nil
}

// request is one JSON-RPC call to ubus.
type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// response is what ubus answers. Result holds the ubus status, then the data
// when there is any; Error is set instead when the call never reached the
// object.
type response struct {
	Result []jsontext.Value `json:"result"`
	Error  *rpcError        `json:"error"`
}

// do sends one call and returns the data it answered with.
//
// A router that cannot be reached at all is [ErrUnreachable], one whose
// certificate does not verify is [ErrTLS], and one that answers with an error
// is a [StatusError] or a JSON-RPC error saying why.
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
		// A cancelled context is the caller giving up. Calling it unreachable
		// would have the poller retry during a shutdown.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("request send: %w", err)
		}

		if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
			return nil, fmt.Errorf("%w: %w", ErrTLS, err)
		}

		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}

	defer func() { _ = resp.Body.Close() }()

	// uhttpd answers a path it does not serve, such as /ubus on a router
	// without uhttpd-mod-ubus, with a 404.
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
