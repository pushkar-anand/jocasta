// Package routeros reads the state of a MikroTik router over the RouterOS v7
// REST API.
//
// The REST service sends JSON over HTTP on 80/443. So a client needs only the
// standard library. The binary API on 8728/8729 would need a dependency. The
// REST API exists only from RouterOS 7, and only when /ip/service has www or
// www-ssl enabled. So if a router answers a ping but refuses this API, nobody
// told it to serve the API.
//
// The package only reads. It never writes. The router is a source of facts
// about the network. A client that cannot change its configuration cannot
// break the network by a mistake. A POST here runs a console command that only
// reports, such as the Ethernet monitor.
//
// Values stay as the router renders them. RouterOS returns most fields as
// strings, also its booleans. [Bool] decodes these. Addresses and hardware
// addresses stay strings on purpose. If one row is malformed, the caller loses
// only that row. The caller decides what to do with it.
package routeros

import (
	"bytes"
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
	// basePath is the start of every REST endpoint. The router serves the
	// binary API and the REST API on different ports. So this path never
	// collides with other services on the host.
	basePath = "/rest"

	defaultPort    = 80
	defaultSSLPort = 443
	defaultTimeout = 10 * time.Second

	// maxBody limits the size of a response. A router does not send more than
	// a few hundred kilobytes of ARP table. A device on this port that is not
	// a router can send data forever.
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

		// SSL selects https. The router serves https as the www-ssl service.
		SSL bool

		// Insecure skips certificate verification. RouterOS makes a
		// self-signed certificate for www-ssl, unless you import one. So the
		// usual setup cannot connect without this setting.
		Insecure bool

		// Timeout limits each request on its own. The default is 10s. Only the
		// context of the caller limits a whole read.
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

// ErrNoHost is a config that names no router.
var ErrNoHost = errors.New("routeros: no host configured")

// New builds a client for the router that cfg names.
//
// It does no I/O. So if the router is down at startup, the server still starts
// and tries again later. To find out whether the credentials and the service
// work, call [RouterOS.Verify].
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
		// Clone copies the default TLS settings, but it can leave the TLS
		// config nil.
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

// Addr is the base URL that this client reads. Use it in a log line to name
// the router that answered.
func (r *RouterOS) Addr() string { return r.url.String() }

// get reads path and decodes the body into T.
//
// If get cannot reach the router at all, it returns [ErrUnreachable]. If the
// certificate does not verify, it returns [ErrTLS]. If the router answers with
// a status, get returns an [Error] with the reason from the router.
func (r *RouterOS) get[T any](ctx context.Context, path string) (*T, error) {
	return r.do[T](ctx, http.MethodGet, path, nil)
}

// post runs a console command with arguments. The REST API takes such a
// command as a POST, with the arguments as a JSON object. It returns errors as
// [RouterOS.get] does.
func (r *RouterOS) post[T any](ctx context.Context, path string, args map[string]string) (*T, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("request encode: %w", err)
	}

	return r.do[T](ctx, http.MethodPost, path, bytes.NewReader(body))
}

// do sends one request and decodes the answer.
func (r *RouterOS) do[T any](ctx context.Context, method, path string, payload io.Reader) (*T, error) {
	uri := r.url.JoinPath(path).String()

	req, err := http.NewRequestWithContext(ctx, method, uri, payload)
	if err != nil {
		return nil, fmt.Errorf("request create: %w", err)
	}

	req.SetBasicAuth(r.cfg.User, r.cfg.Password)
	req.Header.Set("Accept", "application/json")

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.client.Do(req)
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
// empty array. So an empty result with a nil error means an empty table.
func list[T any](ctx context.Context, r *RouterOS, path string) ([]T, error) {
	rows, err := r.get[[]T](ctx, path)
	if err != nil {
		return nil, err
	}

	return *rows, nil
}
