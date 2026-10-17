// Package technitium reads the query log of a Technitium DNS Server over its
// HTTP API.
//
// The server itself keeps no query log. An installed DNS app that logs
// queries keeps one, such as Query Logs (Sqlite). The API pages through the
// log by app name and class path. In a cluster, the app on each node logs to
// the disk of that node. So a client reads one node.
//
// The package only reads. It never writes. Values stay strings, as the server
// renders them. So if a row is malformed, the caller loses only that row.
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

	// maxBody limits the size of a response. A page of a thousand log entries
	// is a few hundred kilobytes. A device on this port that is not a
	// Technitium server can send data forever.
	maxBody = 8 << 20

	// startLayout is the format of the start of a log query. The server parses
	// it as UTC and stores timestamps to 100ns. So seven digits give the
	// timestamp of an entry exactly.
	startLayout = "2006-01-02T15:04:05.0000000Z"
)

type (
	// Config says which server to read and how to reach it.
	Config struct {
		// Host is the server's address or name, without a port.
		Host string

		// Port defaults to 5380, or 53443 when SSL is set.
		Port int

		// Token is an API token, from Create API Token in the user menu. A
		// user with only Logs: View can make every call in this package.
		Token string

		// SSL selects https. The web service serves https after you enable it
		// in Settings.
		SSL bool

		// Insecure skips certificate verification, for a server with a
		// self-signed certificate.
		Insecure bool

		// Timeout limits each request on its own. The default is 10s. Only the
		// context of the caller limits a whole read.
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

		// Start leaves out entries logged before it. Zero reads from the
		// oldest entry.
		Start time.Time

		// Page counts from 1. If PerPage is zero, the server uses its default
		// of 25.
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

		// Answer is the answer section as one string. Each record is its type
		// and data, and ", " joins the records: "A 192.0.2.1, AAAA
		// 2001:db8::1". It is empty when the answer had no records.
		Answer string `json:"answer"`
	}
)

// ErrNoHost is a config that names no server.
var ErrNoHost = errors.New("technitium: no host configured")

// New builds a client for the server that cfg names. It does no I/O. So if
// the server is down at startup, the caller still starts and tries again
// later.
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

	return &Technitium{
		client: &http.Client{Transport: transport, Timeout: timeout},
		base:   base.String(),
		token:  cfg.Token,
	}, nil
}

// Addr is the URL that this client calls. Use it in a log line to name the
// server that answered.
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

// QueryLogs returns one page of the query log that q names.
//
// The server pages by offset over the entries logged since q.Start. So the
// pages before the last do not move while the server logs more. A page after
// the last returns the last page again. So a caller stops at TotalPages.
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

// envelope is the answer of every call. When Status is "ok", Response holds
// the data of the call.
type envelope struct {
	Status       string         `json:"status"`
	ErrorMessage string         `json:"errorMessage"`
	Response     jsontext.Value `json:"response"`
}

// get calls path with query and decodes the response into T.
//
// If get cannot reach the server at all, it returns [ErrUnreachable]. If the
// certificate does not verify, it returns [ErrTLS]. If the server refuses the
// token, it returns [ErrUnauthorized]. The server answers every refusal with
// HTTP 200. So only the status in the body tells the cause.
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
		// A cancelled context means that the caller stopped. If get called it
		// unreachable, the poller would try again during a shutdown.
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
