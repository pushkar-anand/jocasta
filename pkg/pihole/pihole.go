// Package pihole reads a Pi-hole v6 query log over the API its FTL engine
// serves.
//
// The API signs a client in with a password and hands back a session id,
// which the client sends with each call until the session lapses after its
// idle timeout. An app password, made in Pi-hole's web interface, signs in
// without a second factor. Pi-hole has no read-only login: any password that
// signs in can change settings too.
//
// Nothing here changes the Pi-hole's settings.
package pihole

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	defaultPort    = 80
	defaultSSLPort = 443
	defaultTimeout = 10 * time.Second

	// maxBody caps a response. A page of a thousand queries is a few hundred
	// kilobytes, and a device answering on this port that is not a Pi-hole
	// can stream forever.
	maxBody = 8 << 20

	// sidHeader carries the session id on every call after sign-in.
	sidHeader = "X-FTL-SID"
)

type (
	// Config says which Pi-hole to read and how to reach it.
	Config struct {
		// Host is the server's address or name, without a port.
		Host string

		// Port defaults to 80, or 443 when SSL is set.
		Port int

		// Password is an app password or the web interface password. Empty
		// reads a Pi-hole that has no password set.
		Password string

		// SSL selects https. Insecure skips certificate verification, for the
		// self-signed certificate Pi-hole serves until one is installed.
		SSL      bool
		Insecure bool

		// Timeout bounds each request on its own, defaulting to 10s, so a
		// caller's context stays the only thing that bounds a whole read.
		Timeout time.Duration
	}

	// PiHole is a read-only client for one Pi-hole. It is safe for
	// concurrent use, and its calls share one session.
	PiHole struct {
		client   *http.Client
		base     string
		password string

		mu       sync.Mutex
		signedIn bool

		// sid is empty before sign-in, and after it on a Pi-hole with no
		// password, which hands out no session.
		sid string
	}

	// QueryRequest picks a page of the query log, newest first.
	QueryRequest struct {
		// From drops queries logged before it. Zero reads from the oldest
		// the server holds in memory.
		From time.Time

		// Cursor drops queries with a higher id. Passing the first page's
		// newest id on later pages keeps their offsets counting from the same
		// query, whatever is logged in between. Zero starts at the newest.
		Cursor int64

		// Start skips that many queries. Length is the server's default of
		// 100 when zero.
		Start  int
		Length int
	}

	// QueryPage is one page of the query log.
	QueryPage struct {
		Queries []Query `json:"queries"`
	}

	// Query is one logged query and how Pi-hole handled it.
	Query struct {
		// ID grows with every query logged.
		ID int64 `json:"id"`

		// Time is when the query arrived, in Unix seconds.
		Time float64 `json:"time"`

		Type   string `json:"type"`
		Domain string `json:"domain"`

		// Status is how Pi-hole handled the query, such as FORWARDED, CACHE,
		// GRAVITY or REGEX. It is empty for a query still in flight when the
		// page was read.
		Status string `json:"status"`

		Client Client `json:"client"`
		Reply  Reply  `json:"reply"`
	}

	// Client is the address a query came from, and the name Pi-hole knows it
	// by, if any.
	Client struct {
		IP   string `json:"ip"`
		Name string `json:"name"`
	}

	// Reply is what kind of answer the query got, such as IP, CNAME, NXDOMAIN
	// or SERVFAIL. The log keeps the kind alone, never the addresses.
	Reply struct {
		Type string `json:"type"`
	}
)

// ErrNoHost is a config naming no server.
var ErrNoHost = errors.New("pihole: no host configured")

// New builds a client for the Pi-hole cfg names. It performs no I/O, so a
// server that is down at startup is retried later and the caller still
// starts.
func New(cfg *Config) (*PiHole, error) {
	if cfg == nil || cfg.Host == "" {
		return nil, ErrNoHost
	}

	scheme := "http"
	port := defaultPort

	if cfg.SSL {
		scheme = "https"
		port = defaultSSLPort
	}

	if cfg.Port != 0 {
		port = cfg.Port
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()

	if cfg.Insecure {
		// Clone carries the default TLS settings over, but not necessarily a
		// config to hang this on.
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{} //nolint:gosec // the next line is the point.
		}

		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	base := url.URL{Scheme: scheme, Host: net.JoinHostPort(cfg.Host, strconv.Itoa(port))}

	return &PiHole{
		client:   &http.Client{Transport: transport, Timeout: timeout},
		base:     base.String(),
		password: cfg.Password,
	}, nil
}

// Addr is the URL this client calls, useful in a log line naming which server
// answered.
func (p *PiHole) Addr() string { return p.base }

// At returns when q arrived.
func (q Query) At() time.Time {
	sec, frac := math.Modf(q.Time)

	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// Queries returns one page of the query log Pi-hole holds in memory, which
// by default is the last 24 hours.
func (p *PiHole) Queries(ctx context.Context, q QueryRequest) (*QueryPage, error) {
	v := url.Values{}

	if !q.From.IsZero() {
		v.Set("from", strconv.FormatInt(q.From.Unix(), 10))
	}

	if q.Cursor > 0 {
		v.Set("cursor", strconv.FormatInt(q.Cursor, 10))
	}

	if q.Start > 0 {
		v.Set("start", strconv.Itoa(q.Start))
	}

	if q.Length > 0 {
		v.Set("length", strconv.Itoa(q.Length))
	}

	var page QueryPage

	if err := p.get(ctx, "/api/queries", v, &page); err != nil {
		return nil, err
	}

	return &page, nil
}

// get calls path with query and decodes the answer into out, signing in
// first when the client has no session.
func (p *PiHole) get(ctx context.Context, path string, query url.Values, out any) error {
	sid, err := p.session(ctx, "")
	if err != nil {
		return err
	}

	u := p.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	err = p.call(ctx, http.MethodGet, u, sid, nil, out)
	if !errors.Is(err, ErrUnauthorized) || p.password == "" {
		return err
	}

	// A session lapses after its idle timeout, and every session ends when
	// someone makes a new app password, so one refusal signs in again.
	if sid, err = p.session(ctx, sid); err != nil {
		return err
	}

	return p.call(ctx, http.MethodGet, u, sid, nil, out)
}

// session returns the session id to call with. It signs in when the client
// has not, or when the session it holds is stale, the one a call just had
// refused.
func (p *PiHole) session(ctx context.Context, stale string) (string, error) {
	if p.password == "" {
		return "", nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.signedIn && (stale == "" || p.sid != stale) {
		return p.sid, nil
	}

	body, err := json.Marshal(map[string]string{"password": p.password})
	if err != nil {
		return "", fmt.Errorf("auth body: %w", err)
	}

	var res struct {
		Session struct {
			Valid   bool   `json:"valid"`
			SID     string `json:"sid"`
			Message string `json:"message"`
		} `json:"session"`
	}

	if err := p.call(ctx, http.MethodPost, p.base+"/api/auth", "", body, &res); err != nil {
		return "", err
	}

	if !res.Session.Valid {
		return "", fmt.Errorf("%w: %s", ErrUnauthorized, res.Session.Message)
	}

	p.sid, p.signedIn = res.Session.SID, true

	return p.sid, nil
}

// call sends one request and decodes a successful answer into out.
//
// A server that cannot be reached at all is [ErrUnreachable], one whose
// certificate does not verify is [ErrTLS], and a 401 is [ErrUnauthorized].
// Any other refusal is an [*APIError] carrying the server's reason.
func (p *PiHole) call(ctx context.Context, method, u, sid string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request create: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if sid != "" {
		req.Header.Set(sidHeader, sid)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		// A cancelled context is the caller giving up. Calling it unreachable
		// would have the poller retry during a shutdown.
		if ctx.Err() != nil {
			return fmt.Errorf("request send: %w", err)
		}

		if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
			return fmt.Errorf("%w: %w", ErrTLS, err)
		}

		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}

	defer func() { _ = resp.Body.Close() }()

	path := req.URL.Path

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %s", ErrUnauthorized, path)
	}

	if resp.StatusCode != http.StatusOK {
		var res struct {
			Error struct {
				Key     string `json:"key"`
				Message string `json:"message"`
				Hint    string `json:"hint"`
			} `json:"error"`
		}

		if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxBody), &res); err != nil || res.Error.Message == "" {
			return fmt.Errorf("%w: %s: status %d", ErrUnexpected, path, resp.StatusCode)
		}

		return &APIError{Path: path, Key: res.Error.Key, Message: res.Error.Message, Hint: res.Error.Hint}
	}

	if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxBody), out); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnexpected, path, err)
	}

	return nil
}
