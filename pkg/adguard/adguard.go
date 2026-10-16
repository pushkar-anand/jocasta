// Package adguard reads the query log of an AdGuard Home, over the API of its
// web interface.
//
// The API accepts the web interface login over HTTP Basic authentication.
// AdGuard Home has no read-only users. Any login that reads the log can also
// change settings.
//
// The package only reads. It never writes. Values stay strings, as the server
// renders them. So if a row is malformed, the caller loses only that row.
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

	// maxBody limits the size of a response. A page of a thousand log entries
	// is less than a megabyte. A device on this port that is not AdGuard Home
	// can send data forever.
	maxBody = 8 << 20

	// maxReason limits how much of the body of a refusal the client keeps as
	// its message.
	maxReason = 512
)

type (
	// Config says which AdGuard Home to read and how to reach it.
	Config struct {
		// Host is the server's address or name, without a port.
		Host string

		// Port defaults to 80, or 443 when SSL is set.
		Port int

		// User and Password are the web interface login. Leave User empty for
		// a server with no login.
		User     string
		Password string

		// SSL selects https. The server serves https after you set up
		// encryption in its settings. Insecure skips certificate verification.
		// Use it for a self-signed certificate.
		SSL      bool
		Insecure bool

		// Timeout limits each request on its own. The default is 10s. Only the
		// context of the caller limits a whole read.
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
		// OlderThan leaves out entries logged at or after it. Set it to the
		// last entry of the page before, to get the next page. Zero starts at
		// the newest entry.
		OlderThan time.Time

		// If Limit is zero, the server uses its default page size.
		Limit int
	}

	// LogPage is one page of the query log.
	LogPage struct {
		Data []LogEntry `json:"data"`
	}

	// LogEntry is one logged query, and how the server answered it.
	LogEntry struct {
		// Time is when the query arrived, in RFC 3339 with nanoseconds.
		Time string `json:"time"`

		// Client is the address that asked. If the server anonymises client
		// addresses, this address is anonymised.
		Client string `json:"client"`

		Question Question `json:"question"`

		// Answer is the answer section that the client got.
		Answer []Answer `json:"answer"`

		// Reason is why the server gave this answer, such as
		// NotFilteredNotFound, FilteredBlackList or Rewrite.
		Reason string `json:"reason"`

		// Status is the response code, such as NOERROR, NXDOMAIN or SERVFAIL.
		Status string `json:"status"`
	}

	// Question is the name and record type that a query asked for.
	Question struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}

	// Answer is one record of an answer section. Value is the data of the
	// record. For A and AAAA, it is the address.
	Answer struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
)

// ErrNoHost is a config that names no server.
var ErrNoHost = errors.New("adguard: no host configured")

// New builds a client for the server that cfg names. It does no I/O. So if
// the server is down at startup, the caller still starts and tries again
// later.
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

	return &AdGuard{
		client:   &http.Client{Transport: transport, Timeout: timeout},
		base:     base.String(),
		user:     cfg.User,
		password: cfg.Password,
	}, nil
}

// Addr is the URL that this client calls. Use it in a log line to name the
// server that answered.
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
// If the client cannot reach the server at all, the error is
// [ErrUnreachable]. If the certificate does not verify, it is [ErrTLS]. A 401
// is [ErrUnauthorized]. Any other refusal is an [*APIError] with the reason
// of the server.
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

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %s", ErrUnauthorized, path)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s: status %d", ErrUnexpected, path, resp.StatusCode)
	default:
		// The client keeps the body of a refusal as its message, in any
		// format.
		reason, _ := io.ReadAll(io.LimitReader(resp.Body, maxReason))

		return nil, &APIError{Path: path, Status: resp.StatusCode, Message: strings.TrimSpace(string(reason))}
	}

	var v T

	if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxBody), &v); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnexpected, path, err)
	}

	return &v, nil
}
