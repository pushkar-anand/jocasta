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

// client sends every notification. It never follows a redirect. A redirect
// would send the headers of a destination, such as its token, to a different
// address.
var client = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// post sends body to target, for the providers whose service accepts an HTTP
// request. It sends JSON, unless header names a different Content-Type. An
// error names only the host of the target, because a URL can contain a
// token.
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

	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		// A *url.Error contains the whole URL. Keep only the problem.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}

		return fmt.Errorf("could not reach %s: %w", u.Host, err)
	}

	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		return fmt.Errorf("%s answered %s. Set the url to the address it redirects to", u.Host, resp.Status)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered %s", u.Host, resp.Status)
	}

	return nil
}

// parseURL returns raw, parsed. If raw is not an absolute http or https URL,
// it returns an error.
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
