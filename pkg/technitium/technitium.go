// Package technitium reads a Technitium DNS Server's query log over its HTTP
// API.
//
// The server keeps no query log of its own. An installed DNS app that logs
// queries, such as Query Logs (Sqlite), keeps one, and the API pages through
// it by app name and class path. In a cluster each node's app logs to that
// node's own disk, so a client reads one node.
//
// Everything here reads; nothing writes. Values arrive as the server renders
// them and stay strings, so a malformed row costs the caller only that row.
package technitium

import (
	"context"
	"crypto/tls"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultPort    = 5380
	defaultSSLPort = 53443
	defaultTimeout = 10 * time.Second

	// maxBody caps a response. A page of a thousand log entries is a few
	// hundred kilobytes, and a device answering on this port that is not a
	// Technitium server can stream forever.
	maxBody = 8 << 20

	// startLayout is how a log query's start is sent. The server parses it as
	// UTC and stores timestamps to 100ns, so seven digits name an entry's
	// timestamp exactly.
	startLayout = "2006-01-02T15:04:05.0000000Z"
)

type (
	// Config says which server to read and how to reach it.
	Config struct {
		// Host is the server's address or name, without a port.
		Host string

		// Port defaults to 5380, or 53443 when SSL is set.
		Port int

		// Token is an API token, from the user menu's Create API Token. A
		// user granted Logs: View alone can make every call here.
		Token string

		// SSL selects https, which the web service serves once it is enabled
		// in Settings.
		SSL bool

		// Insecure skips certificate verification, for a server with a
		// self-signed certificate.
		Insecure bool

		// Timeout bounds each request on its own, defaulting to 10s, so a
		// caller's context stays the only thing that bounds a whole read.
		Timeout time.Duration
	}

	// Technitium is a read-only client for one server. It is safe for
	// concurrent use.
	Technitium struct {
		client *http.Client
		base   string
		token  string
	}

	// App is one installed DNS app and the classes it provides.
	App struct {
		Name    string   `json:"name"`
		Version string   `json:"version"`
		DNSApps []DNSApp `json:"dnsApps"`
	}

	// DNSApp is one class an app provides. IsQueryLogger marks a class whose
	// log [Technitium.QueryLogs] can read.
	DNSApp struct {
		ClassPath     string `json:"classPath"`
		IsQueryLogger bool   `json:"isQueryLogger"`
	}

	// LogQuery picks a page of one app's query log, oldest first.
	LogQuery struct {
		App       string
		ClassPath string

		// Start drops entries logged before it. Zero reads from the oldest.
		Start time.Time

		// Page counts from 1. PerPage is the server's default of 25 when zero.
		Page    int
		PerPage int
	}

	// LogPage is one page of a query log.
	LogPage struct {
		PageNumber   int64      `json:"pageNumber"`
		TotalPages   int64      `json:"totalPages"`
		TotalEntries int64      `json:"totalEntries"`
		Entries      []LogEntry `json:"entries"`
	}

	// LogEntry is one logged query and how the server answered it.
	LogEntry struct {
		Timestamp       string `json:"timestamp"`
		ClientIPAddress string `json:"clientIpAddress"`
		Protocol        string `json:"protocol"`

		// ResponseType is how the server answered: Authoritative, Recursive,
		// Cached, Blocked, UpstreamBlocked or CacheBlocked.
		ResponseType string `json:"responseType"`

		// RCode is the response code as the server names it, such as NoError,
		// NxDomain or ServerFailure.
		RCode string `json:"rcode"`

		QName  string `json:"qname"`
		QType  string `json:"qtype"`
		QClass string `json:"qclass"`

		// Answer is the answer section as one string, each record its type
		// and data, joined by ", ": "A 192.0.2.1, AAAA 2001:db8::1". It is
		// empty when the answer had no records.
		Answer string `json:"answer"`
	}
)

// ErrNoHost is a config naming no server.
var ErrNoHost = errors.New("technitium: no host configured")

// New builds a client for the server cfg names. It performs no I/O, so a
// server that is down at startup is retried later and the caller still
// starts.
func New(cfg *Config) (*Technitium, error) {
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

	return &Technitium{
		client: &http.Client{Transport: transport, Timeout: timeout},
		base:   base.String(),
		token:  cfg.Token,
	}, nil
}

// Addr is the URL this client calls, useful in a log line naming which server
// answered.
func (t *Technitium) Addr() string { return t.base }

// Apps returns the installed DNS apps.
func (t *Technitium) Apps(ctx context.Context) ([]App, error) {
	res, err := get[struct {
		Apps []App `json:"apps"`
	}](ctx, t, "/api/apps/list", nil)
	if err != nil {
		return nil, err
	}

	return res.Apps, nil
}

// QueryLogs returns one page of the query log q names.
//
// The server pages by offset over the entries logged since q.Start, so the
// pages up to the last stay put while it logs more. A page past the last
// answers with the last page again, so a caller stops at TotalPages.
func (t *Technitium) QueryLogs(ctx context.Context, q LogQuery) (*LogPage, error) {
	v := url.Values{
		"name":            {q.App},
		"classPath":       {q.ClassPath},
		"pageNumber":      {strconv.Itoa(max(q.Page, 1))},
		"descendingOrder": {"false"},
	}

	if q.PerPage > 0 {
		v.Set("entriesPerPage", strconv.Itoa(q.PerPage))
	}

	if !q.Start.IsZero() {
		v.Set("start", q.Start.UTC().Format(startLayout))
	}

	return get[LogPage](ctx, t, "/api/logs/query", v)
}

// envelope is what every call answers with. Response holds the call's own
// data when Status is "ok".
type envelope struct {
	Status       string         `json:"status"`
	ErrorMessage string         `json:"errorMessage"`
	Response     jsontext.Value `json:"response"`
}

// get calls path with query and decodes the response into T.
//
// A server that cannot be reached at all is [ErrUnreachable], one whose
// certificate does not verify is [ErrTLS], and one that refuses the token is
// [ErrUnauthorized]. The server answers every refusal with HTTP 200, so the
// status in the body is what says which.
func get[T any](ctx context.Context, t *Technitium, path string, query url.Values) (*T, error) {
	u := t.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("request create: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
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

	var env envelope

	if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxBody), &env); err != nil {
		return nil, fmt.Errorf("%w: %s: status %d, %w", ErrUnexpected, path, resp.StatusCode, err)
	}

	switch env.Status {
	case "ok":
	case "invalid-token":
		return nil, fmt.Errorf("%w: %s", ErrUnauthorized, path)
	case "error":
		return nil, &APIError{Path: path, Message: env.ErrorMessage}
	default:
		return nil, fmt.Errorf("%w: %s answered status %q", ErrUnexpected, path, env.Status)
	}

	var v T

	if len(env.Response) == 0 {
		return &v, nil
	}

	if err := json.Unmarshal(env.Response, &v); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnexpected, path, err)
	}

	return &v, nil
}
