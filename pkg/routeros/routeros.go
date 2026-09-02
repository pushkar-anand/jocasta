package routeros

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
)

type (
	Config struct {
		Host     string
		Port     int
		User     string
		Password string
		SSL      bool
		Insecure bool
	}

	RouterOS struct {
		cfg    *Config
		client *http.Client
		logger *slog.Logger
		url    *url.URL
	}
)

func New(
	cfg *Config,
	log *slog.Logger,
) (*RouterOS, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if cfg.Insecure {
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	client := &http.Client{
		Transport: http.DefaultTransport,
	}

	scheme := "http"
	if cfg.SSL {
		scheme = "https"
	}

	u := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:   "/rest",
	}

	r := &RouterOS{
		cfg:    cfg,
		client: client,
		logger: log,
		url:    u,
	}

	err := r.verifyConnection(context.Background())
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *RouterOS) get[T any](ctx context.Context, path string) (*T, error) {
	uri := r.url.JoinPath(path).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("request create: %w", err)
	}

	req.SetBasicAuth(r.cfg.User, r.cfg.Password)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request send: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	var t T

	err = json.UnmarshalRead(resp.Body, &t)
	if err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return &t, nil
}
