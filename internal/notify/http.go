package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// post sends body to target as JSON, for the providers whose service takes an
// HTTP request. An error names the target's host and no more, since a URL can
// hold a token.
func post(ctx context.Context, target string, header http.Header, body []byte) error {
	u, err := url.Parse(target)
	if err != nil {
		return errors.New("the url does not parse")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request to %s: %w", u.Host, errors.Unwrap(err))
	}

	for k, vs := range header {
		req.Header[k] = vs
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// A *url.Error spells out the whole URL; keep only what went wrong.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}

		return fmt.Errorf("could not reach %s: %w", u.Host, err)
	}

	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered %s", u.Host, resp.Status)
	}

	return nil
}

// parseURL returns raw parsed, or an error unless it is an absolute http or
// https URL.
func parseURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("a url is required")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("the url does not parse")
	}

	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("url %q is not an http or https address", u.Redacted())
	}

	return u, nil
}
