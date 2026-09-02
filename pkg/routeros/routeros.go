// Package routeros reads a MikroTik router's state over the RouterOS v7 REST
// API.
//
// The REST service speaks JSON over HTTP on 80/443 and needs no dependency
// beyond the standard library, unlike the binary API on 8728/8729. It only
// exists from RouterOS 7, and only when /ip/service has www or www-ssl
// enabled, so a router that answers a ping and refuses this is a router that
// has not been told to serve it.
//
// Everything here reads; nothing writes. The router is a source of facts about
// the network, and a client that cannot change its configuration cannot break
// the network by being wrong.
//
// Values arrive as the router renders them. RouterOS returns most fields as
// strings, including its booleans, which [Bool] absorbs. Addresses and
// hardware addresses stay strings on purpose: a router with one malformed row
// should cost the caller that row, not the whole table, and that decision
// belongs to whoever is reading the table rather than to the decoder.
package routeros

import (
	"context"
	"crypto/tls"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// basePath prefixes every REST endpoint. The router serves the binary API
	// and the REST API on different ports, so this never collides with
	// anything else the host runs.
	basePath = "/rest"

	defaultPort    = 80
	defaultSSLPort = 443
	defaultTimeout = 10 * time.Second

	// maxBody caps a response. A router will not send more than a few hundred
	// kilobytes of ARP table, and a device answering on this port that is not
	// a router can stream forever.
	maxBody = 8 << 20
)

type (
	// Config says which router to read and how to reach it.
	Config struct {
		// Host is the router's address or name, without a port.
		Host string

		// Port defaults to 80, or 443 when SSL is set.
		Port int

		User     string
		Password string

		// SSL selects https, which the router serves as the www-ssl service.
		SSL bool

		// Insecure skips certificate verification. RouterOS generates a
		// self-signed certificate for www-ssl unless one is imported, so
		// without this the common setup cannot connect at all.
		Insecure bool

		// Timeout bounds one request, defaulting to 10s. It is per request
		// rather than per client so a caller's context stays the only thing
		// that bounds a whole read.
		Timeout time.Duration
	}

	// RouterOS is a read-only client for one router. It is safe for concurrent
	// use.
	RouterOS struct {
		cfg    *Config
		client *http.Client
		logger *slog.Logger
		url    *url.URL
	}
)

// ErrNoHost is a config naming no router.
var ErrNoHost = errors.New("routeros: no host configured")

// New builds a client for the router cfg names.
//
// It performs no I/O: a router that is down at startup is a router to retry,
// not a reason to refuse to start. Call [RouterOS.Verify] to find out whether
// the credentials and the service are actually good.
func New(
	cfg *Config,
	log *slog.Logger,
) (*RouterOS, error) {
	if cfg == nil || cfg.Host == "" {
		return nil, ErrNoHost
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

	r := &RouterOS{
		cfg: &c,
		client: &http.Client{
			Transport: transport,
			Timeout:   c.Timeout,
		},
		logger: log,
		url: &url.URL{
			Scheme: scheme,
			Host:   net.JoinHostPort(c.Host, strconv.Itoa(port)),
			Path:   basePath,
		},
	}

	return r, nil
}

// Addr is the base URL this client reads, useful in a log line naming which
// router answered.
func (r *RouterOS) Addr() string { return r.url.String() }

// get reads path and decodes the body into T.
//
// A router that cannot be reached at all is [ErrUnreachable], one whose
// certificate does not verify is [ErrTLS], and one that answers with a status
// is an [Error] carrying whatever the router said about why.
func (r *RouterOS) get[T any](ctx context.Context, path string) (*T, error) {
	uri := r.url.JoinPath(path).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("request create: %w", err)
	}

	req.SetBasicAuth(r.cfg.User, r.cfg.Password)
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		// A cancelled context is the caller giving up, not the router being
		// absent, and calling it unreachable would have the poller retry
		// during a shutdown.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("request send: %w", err)
		}

		var certErr *tls.CertificateVerificationError
		if errors.As(err, &certErr) {
			return nil, fmt.Errorf("%w: %w", ErrTLS, err)
		}

		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body := io.LimitReader(resp.Body, maxBody)

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp.StatusCode, body)
	}

	var t T

	err = json.UnmarshalRead(body, &t)
	if err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}

	return &t, nil
}

// list reads a collection endpoint. RouterOS renders an empty table as an
// empty array, so a nil slice back means the table is empty and not that the
// read failed.
func list[T any](ctx context.Context, r *RouterOS, path string) ([]T, error) {
	rows, err := r.get[[]T](ctx, path)
	if err != nil {
		return nil, err
	}

	return *rows, nil
}
