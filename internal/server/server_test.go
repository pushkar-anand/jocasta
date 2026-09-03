package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testStore opens a migrated database in a directory scoped to the test and
// returns the inventory over it. These tests assert on the HTTP surface rather
// than on presence, so the store takes the default online window.
func testStore(t *testing.T) *inventory.Store {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return inventory.New(conn, testLogger())
}

// freePort reserves a port and releases it again. There is a window in which
// something else could take it, but nothing else in this test binary binds,
// and Start needs the port up front because it does not report the one it got.
func freePort(t *testing.T) int {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	return ln.Addr().(*net.TCPAddr).Port
}

// waitForServer blocks until the address accepts connections.
func waitForServer(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(t.Context(), "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("server did not start listening on %s", addr)
}

// startServer runs Start in the background and returns the base URL. The server
// is stopped, and its shutdown asserted, when the test ends.
// testValidator builds the validator main hands the server.
func testValidator(t *testing.T) *validator.Validator {
	t.Helper()

	v, err := validator.New()
	require.NoError(t, err)

	return v
}

func startServer(t *testing.T) string {
	t.Helper()

	port := freePort(t)

	// Opened here rather than in the goroutine below: testDB registers a
	// cleanup, and t.Cleanup must not be called from another goroutine.
	store := testStore(t)

	errCh := make(chan error, 1)

	// t.Context is cancelled just before the cleanups below run, so the test
	// ending is what stops the server.
	ctx := t.Context()

	go func() {
		errCh <- Start(ctx, &Config{Addr: "127.0.0.1", Port: port, Logger: testLogger()}, store, testValidator(t))
	}()

	t.Cleanup(func() {
		select {
		case err := <-errCh:
			assert.NoError(t, err, "Start should return cleanly once the context is cancelled")
		case <-time.After(10 * time.Second):
			t.Error("Start did not return after the context was cancelled")
		}
	})

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	waitForServer(t, addr)

	return "http://" + addr
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

// TestStartRoutes covers the mux Start builds: the API under /api, and
// everything else served by the web handler.
func TestStartRoutes(t *testing.T) {
	baseURL := startServer(t)

	t.Run("api is mounted under /api", func(t *testing.T) {
		res := get(t, baseURL+"/api/livez")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusOK, res.StatusCode)

		var body map[string]string
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		assert.Contains(t, body, "version")
	})

	t.Run("the api prefix is stripped", func(t *testing.T) {
		// Without StripPrefix this would reach the API handler as /api/livez.
		// With it, /livez outside the prefix is not an API route at all: it
		// falls through to the web handler, which does not know the path and
		// says so in HTML rather than answering with the API's JSON.
		res := get(t, baseURL+"/livez")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusNotFound, res.StatusCode)
		assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
	})

	t.Run("web handler serves the root", func(t *testing.T) {
		res := get(t, baseURL+"/")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.Contains(t, res.Header.Get("Content-Type"), "text/html")

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "jocasta")
	})

	t.Run("static assets are served", func(t *testing.T) {
		res := get(t, baseURL+"/static/style.css")
		defer func() { _ = res.Body.Close() }()

		assert.Equal(t, http.StatusOK, res.StatusCode)
	})

	// The headers are set for the whole mux rather than by the renderer, which
	// is what a stylesheet or the vendored htmx needs: neither passes through a
	// template.
	t.Run("security headers reach every response", func(t *testing.T) {
		for _, path := range []string{"/", "/static/style.css", "/static/js/htmx.min.js", "/api/livez"} {
			t.Run(path, func(t *testing.T) {
				res := get(t, baseURL+path)
				defer func() { _ = res.Body.Close() }()

				assert.Equal(t, csp, res.Header.Get("Content-Security-Policy"))
				assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
				assert.Equal(t, "DENY", res.Header.Get("X-Frame-Options"))
				assert.Equal(t, "no-referrer", res.Header.Get("Referrer-Policy"))
			})
		}
	})

	// The policy is the reason htmx is vendored and no markup carries an inline
	// style or script, so the page has to load only from this origin.
	t.Run("the policy admits only this origin", func(t *testing.T) {
		assert.Contains(t, csp, "default-src 'self'")
		assert.NotContains(t, csp, "unsafe-inline")
		assert.NotContains(t, csp, "unsafe-eval")
	})

	t.Run("responses carry a request id", func(t *testing.T) {
		res := get(t, baseURL+"/")
		defer func() { _ = res.Body.Close() }()

		assert.NotEmpty(t, res.Header.Get("X-Request-Id"))
	})
}

func TestStartFailsOnPortInUse(t *testing.T) {
	t.Parallel()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	err = Start(t.Context(), &Config{
		Addr:   "127.0.0.1",
		Port:   ln.Addr().(*net.TCPAddr).Port,
		Logger: testLogger(),
	}, testStore(t), testValidator(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "error binding")
}

// patchWith sends a state-changing request carrying the headers a browser would
// send from the given site.
func patchWith(t *testing.T, url string, headers map[string]string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, url, strings.NewReader("label=x"))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

// A state-changing request that a browser says came from another site is turned
// away. This is not a CSRF token and cannot be one until there is a session to
// bind it to; it refuses the shape of the attack in advance.
func TestCrossOriginWriteIsRefused(t *testing.T) {
	baseURL := startServer(t)

	tests := []struct {
		name    string
		headers map[string]string
		refused bool
	}{
		{
			name:    "a browser on this site",
			headers: map[string]string{"Sec-Fetch-Site": "same-origin"},
		},
		{
			name:    "a typed address or a bookmark",
			headers: map[string]string{"Sec-Fetch-Site": "none"},
		},
		{
			name:    "a browser on another site",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
			refused: true,
		},
		{
			name:    "another host on the same domain",
			headers: map[string]string{"Sec-Fetch-Site": "same-site"},
			refused: true,
		},
		{
			name:    "an older browser reporting its origin",
			headers: map[string]string{"Origin": "https://evil.example"},
			refused: true,
		},
		{
			// curl and the like send neither header, and refusing those would
			// break every script the JSON API exists for.
			name:    "not a browser at all",
			headers: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := patchWith(t, baseURL+"/api/devices/1", tc.headers)
			defer func() { _ = res.Body.Close() }()

			if tc.refused {
				assert.Equal(t, http.StatusForbidden, res.StatusCode)

				return
			}

			// Allowed through to the handler, which has no such device in an
			// empty inventory. Either way it is not the guard refusing it.
			assert.NotEqual(t, http.StatusForbidden, res.StatusCode)
		})
	}
}

// Reads are never refused, whatever site they came from: there is nothing to
// forge when nothing changes.
func TestCrossOriginReadIsAllowed(t *testing.T) {
	baseURL := startServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/stats", nil)
	require.NoError(t, err)

	req.Header.Set("Sec-Fetch-Site", "cross-site")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	assert.Equal(t, http.StatusOK, res.StatusCode)
}

func TestSafeMethod(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace} {
		assert.True(t, safeMethod(method), method)
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		assert.False(t, safeMethod(method), method)
	}
}
