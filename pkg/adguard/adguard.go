// Package adguard reads an AdGuard Home query log over the API its web
// interface serves.
//
// The API takes the web interface login over HTTP Basic authentication.
// AdGuard Home has no read-only users: any login that reads the log can
// change settings too.
//
// Everything here reads; nothing writes. Values arrive as the server renders
// them and stay strings, so a malformed row costs the caller only that row.
package adguard

import (
	"context"
	"crypto/tls"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort    = 80
	defaultSSLPort = 443
	defaultTimeout = 10 * time.Second

	// maxBody caps a response. A page of a thousand log entries is under a
	// megabyte, and a device answering on this port that is not AdGuard Home
	// can stream forever.
	maxBody = 8 << 20

	// maxReason caps how much of a refusal's body is kept for its message.
	maxReason = 512
)

type (
	// Config says which AdGuard Home to read and how to reach it.
	Config struct {
		// Host is the server's address or name, without a port.
		Host string

		// Port defaults to 80, or 443 when SSL is set.
		Port int

		// User and Password are the web interface login. An empty User reads
		// a server with no login set.
		User     string
		Password string

		// SSL selects https, which the server serves once encryption is set
		// up in its settings. Insecure skips certificate verification, for a
		// self-signed certificate.
		SSL      bool
		Insecure bool

		// Timeout bounds each request on its own, defaulting to 10s, so a
		// caller's context stays the only thing that bounds a whole read.
		Timeout time.Duration
	}

	// AdGuard is a read-only client for one AdGuard Home. It is safe for
	// concurrent use.
	AdGuard struct {
		client   *http.Client
		base     string
		user     string
		password string
	}

	// LogRequest picks a page of the query log, newest first.
	LogRequest struct {
		// OlderThan drops entries logged at or after it, which pages past the
		// last entry of the page before. Zero starts at the newest.
		OlderThan time.Time

		// Limit is the server's default page size when zero.
		Limit int
	}

	// LogPage is one page of the query log.
	LogPage struct {
		Data []LogEntry `json:"data"`
	}

	// LogEntry is one logged query and how the server answered it.
	LogEntry struct {
		// Time is when the query arrived, in RFC 3339 with nanoseconds.
		Time string `json:"time"`

		// Client is the address that asked, with its last part zeroed when
		// the server anonymises client addresses.
		Client string `json:"client"`

		Question Question `json:"question"`

		// Answer is the answer section the client got.
		Answer []Answer `json:"answer"`

		// Reason is why the server answered as it did, such as
		// NotFilteredNotFound, FilteredBlackList or Rewrite.
		Reason string `json:"reason"`

		// Status is the response code, such as NOERROR, NXDOMAIN or SERVFAIL.
		Status string `json:"status"`
	}

	// Question is the name and record type a query asked for.
	Question struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}

	// Answer is one record of an answer section. Value is the record's data,
	// which for A and AAAA is the address.
	Answer struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
)

// ErrNoHost is a config naming no server.
var ErrNoHost = errors.New("adguard: no host configured")

// New builds a client for the server cfg names. It performs no I/O, so a
// server that is down at startup is retried later and the caller still
// starts.
func New(cfg *Config) (*AdGuard, error) {
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

	return &AdGuard{
		client:   &http.Client{Transport: transport, Timeout: timeout},
		base:     base.String(),
		user:     cfg.User,
		password: cfg.Password,
	}, nil
}

// Addr is the URL this client calls, useful in a log line naming which server
// answered.
func (a *AdGuard) Addr() string { return a.base }

// QueryLog returns one page of the query log.
func (a *AdGuard) QueryLog(ctx context.Context, q LogRequest) (*LogPage, error) {
	v := url.Values{}

	if !q.OlderThan.IsZero() {
		v.Set("older_than", q.OlderThan.Format(time.RFC3339Nano))
	}

	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}

	return get[LogPage](ctx, a, "/control/querylog", v)
}

// get calls path with query and decodes the answer into T.
//
// A server that cannot be reached at all is [ErrUnreachable], one whose
// certificate does not verify is [ErrTLS], and a 401 is [ErrUnauthorized].
// Any other refusal is an [*APIError] carrying the server's reason.
func get[T any](ctx context.Context, a *AdGuard, path string, query url.Values) (*T, error) {
	u := a.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("request create: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	if a.user != "" {
		req.SetBasicAuth(a.user, a.password)
	}

	resp, err := a.client.Do(req)
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

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %s", ErrUnauthorized, path)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s: status %d", ErrUnexpected, path, resp.StatusCode)
	default:
		// The server words its refusals as plain text, such as the wait after
		// too many failed sign-ins.
		reason, _ := io.ReadAll(io.LimitReader(resp.Body, maxReason))

		return nil, &APIError{Path: path, Status: resp.StatusCode, Message: strings.TrimSpace(string(reason))}
	}

	var v T

	if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxBody), &v); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnexpected, path, err)
	}

	return &v, nil
}
