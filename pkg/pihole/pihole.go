// Package pihole reads the query log of a Pi-hole v6, over the API of its FTL
// engine.
//
// The API signs a client in with a password and returns a session id. The
// client sends the id with each call, until the session ends after its idle
// timeout. An app password signs in without a second factor. You make it in
// the web interface of Pi-hole. Pi-hole has no read-only login. Any password
// that signs in can also change settings.
//
// This package never changes the settings of the Pi-hole.
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

	// maxBody limits the size of a response. A page of a thousand queries is a
	// few hundred kilobytes. A device on this port that is not a Pi-hole can
	// send data forever.
	maxBody = 8 << 20

	// sidHeader holds the session id on every call after sign-in.
	sidHeader = "X-FTL-SID"
)

type (
	// Config says which Pi-hole to read and how to reach it.
	Config struct {
		// Host is the server's address or name, without a port.
		Host string

		// Port defaults to 80, or 443 when SSL is set.
		Port int

		// Password is an app password or the web interface password. Leave it
		// empty for a Pi-hole that has no password.
		Password string

		// SSL selects https. Insecure skips certificate verification. Use it
		// for the self-signed certificate that Pi-hole serves by default.
		SSL      bool
		Insecure bool

		// Timeout limits each request on its own. The default is 10s. Only the
		// context of the caller limits a whole read.
		Timeout time.Duration
	}

	// PiHole is a read-only client for one Pi-hole. It is safe for
	// concurrent use. Its calls share one session.
	PiHole struct {
		client   *http.Client
		base     string
		password string

		mu       sync.Mutex
		signedIn bool

		// sid is empty before sign-in. On a Pi-hole with no password, it stays
		// empty, because that Pi-hole gives no session.
		sid string
	}

	// QueryRequest picks a page of the query log, newest first.
	QueryRequest struct {
		// From leaves out queries logged before it. Zero reads from the oldest
		// query that the server holds in memory.
		From time.Time

		// Cursor leaves out queries with a higher id. Pass the newest id of
		// the first page on later pages. Then their offsets count from the same
		// query, even when the server logs more queries. Zero starts at the
		// newest query.
		Cursor int64

		// Start skips that number of queries. If Length is zero, the server
		// uses its default of 100.
		Start  int
		Length int
	}

	// QueryPage is one page of the query log.
	QueryPage struct {
		Queries []Query `json:"queries"`
	}

	// Query is one logged query, and how Pi-hole handled it.
	Query struct {
		// ID increases with each logged query.
		ID int64 `json:"id"`

		// Time is when the query arrived, in Unix seconds.
		Time float64 `json:"time"`

		Type   string `json:"type"`
		Domain string `json:"domain"`

		// Status is how Pi-hole handled the query, such as FORWARDED, CACHE,
		// GRAVITY or REGEX. It is empty for a query that was not complete when
		// the client read the page.
		Status string `json:"status"`

		Client Client `json:"client"`
		Reply  Reply  `json:"reply"`
	}

	// Client is the address that a query came from, and the name that
	// Pi-hole has for it, if any.
	Client struct {
		IP   string `json:"ip"`
		Name string `json:"name"`
	}

	// Reply is the kind of answer that the query got, such as IP, CNAME,
	// NXDOMAIN or SERVFAIL. The log keeps only the kind, never the addresses.
	Reply struct {
		Type string `json:"type"`
	}
)

// ErrNoHost is a config that names no server.
var ErrNoHost = errors.New("pihole: no host configured")

// New builds a client for the Pi-hole that cfg names. It does no I/O. So if
// the server is down at startup, the caller still starts and tries again
// later.
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
		// Clone copies the default TLS settings, but it can leave the TLS
		// config nil.
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

// Addr is the URL that this client calls. Use it in a log line to name the
// server that answered.
func (p *PiHole) Addr() string { return p.base }

// At returns when q arrived.
func (q Query) At() time.Time {
	sec, frac := math.Modf(q.Time)

	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// Queries returns one page of the query log that Pi-hole holds in memory. By
// default, this log covers the last 24 hours.
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

// get calls path with query and decodes the answer into out. If the client
// has no session, get signs in first.
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

	// A session ends after its idle timeout. Every session also ends when
	// someone makes a new app password. So after one refusal, get signs in
	// again.
	if sid, err = p.session(ctx, sid); err != nil {
		return err
	}

	return p.call(ctx, http.MethodGet, u, sid, nil, out)
}

// session returns the session id for a call. It signs in if the client has
// no session. It also signs in if the session is stale, which means that the
// server just refused it.
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
// If the client cannot reach the server at all, the error is
// [ErrUnreachable]. If the certificate does not verify, it is [ErrTLS]. A 401
// is [ErrUnauthorized]. Any other refusal is an [*APIError] with the reason
// of the server.
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
		// A cancelled context means that the caller stopped. If the client
		// called it unreachable, the poller would try again during a shutdown.
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
