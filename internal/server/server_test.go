package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// freePort reserves a port and releases it again. There is a window in which
// something else could take it, but nothing else in this test binary binds,
// and Start needs the port up front because it does not report the one it got.
func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	return ln.Addr().(*net.TCPAddr).Port
}

// waitForServer blocks until the address accepts connections.
func waitForServer(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
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
func startServer(t *testing.T) string {
	t.Helper()

	port := freePort(t)

	errCh := make(chan error, 1)

	// t.Context is cancelled just before the cleanups below run, so the test
	// ending is what stops the server.
	ctx := t.Context()

	go func() {
		errCh <- Start(ctx, &Config{Addr: "127.0.0.1", Port: port, Logger: testLogger()})
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

		require.Equal(t, http.StatusOK, res.StatusCode)

		var body map[string]string
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		assert.Contains(t, body, "version")
	})

	t.Run("the api prefix is stripped", func(t *testing.T) {
		// Without StripPrefix this would reach the API handler as /api/livez
		// and 404; with it, /livez outside the prefix falls through to the web
		// handler, which serves the index page for any unmatched path.
		res := get(t, baseURL+"/livez")

		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
	})

	t.Run("web handler serves the root", func(t *testing.T) {
		res := get(t, baseURL+"/")

		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.Contains(t, res.Header.Get("Content-Type"), "text/html")

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "Hello World!")
	})

	t.Run("static assets are served", func(t *testing.T) {
		res := get(t, baseURL+"/static/style.css")

		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
}

func TestStartFailsOnPortInUse(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	err = Start(t.Context(), &Config{
		Addr:   "127.0.0.1",
		Port:   ln.Addr().(*net.TCPAddr).Port,
		Logger: testLogger(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "error binding")
}
