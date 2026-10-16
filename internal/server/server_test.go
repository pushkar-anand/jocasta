package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testUsername and testPassword name the one account in every test server. So
// a test that needs a signed-in view does not need its own credential.
const (
	testUsername = "jocasta-test"
	testPassword = "test-password-1"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testStore opens a migrated database in a directory for the test only. It
// returns the inventory over it. These tests assert on the HTTP surface. So
// the store uses the default online window.
func testStore(t *testing.T) *inventory.Store {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return inventory.New(conn, testLogger())
}

// testConn opens a migrated database for the session store, the part of Start
// that takes a database directly. These tests assert only on the HTTP surface.
// So it does not need to be the DB of the store or of auth.
func testConn(t *testing.T) *sql.DB {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "server.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// testAuth builds an Auth over its own migrated database, separate from the
// store of a test. The database has the one account for test sign-ins, and
// one read-write API token for test API calls.
func testAuth(t *testing.T) (*auth.Auth, string) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "auth.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	q := models.New(conn)

	hash, err := password.NewHasher().Hash(testPassword)
	require.NoError(t, err)

	user, err := q.CreateUser(t.Context(), models.CreateUserParams{
		Username:     testUsername,
		PasswordHash: hash,
		Role:         dbtype.RoleAdmin,
	})
	require.NoError(t, err)

	a, err := auth.New(conn, q, password.NewHasher())
	require.NoError(t, err)

	apiToken, _, err := a.CreateToken(t.Context(), user.ID, "test token", dbtype.TokenReadWrite, time.Time{})
	require.NoError(t, err)

	return a, apiToken
}

// freePort reserves a port and releases it again. During a short time, a
// different process could take it. But nothing else in this test binary binds.
// Start needs the port first, because it does not report the port that it
// got.
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

// testValidator builds the validator that main gives the server.
func testValidator(t *testing.T) *validator.Validator {
	t.Helper()

	v, err := validator.New()
	require.NoError(t, err)

	return v
}

// startServer runs Start in the background. It returns the base URL, and a
// read-write API token that works on it. When the test ends, the server stops,
// and the test asserts its shutdown.
func startServer(t *testing.T) (string, string) {
	t.Helper()

	port := freePort(t)

	// Open these here, outside the goroutine below. These helpers register
	// cleanups. t.Cleanup must not run from a different goroutine.
	conn := testConn(t)
	store := testStore(t)
	a, apiToken := testAuth(t)

	errCh := make(chan error, 1)

	// t.Context is cancelled just before the cleanups below run. So the end of
	// the test stops the server.
	ctx := t.Context()

	go func() {
		errCh <- Start(ctx, &Config{Addr: "127.0.0.1", Port: port, Logger: testLogger()}, conn, store, testValidator(t), a)
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

	return "http://" + addr, apiToken
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()

	return getWith(t, http.DefaultClient, url)
}

func getWith(t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)

	res, err := client.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

// loginClient signs in as the test user. It returns a client with the session
// cookie, for a test that needs the signed-in view. Every other client here
// goes to the sign-in page.
func loginClient(t *testing.T, baseURL string) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)

	client := &http.Client{Jar: jar}

	form := url.Values{"username": {testUsername}, "password": {testPassword}}

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, baseURL+"/login", strings.NewReader(form.Encode()),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := client.Do(req)
	require.NoError(t, err)

	defer func() { _ = res.Body.Close() }()

	require.Equal(t, http.StatusOK, res.StatusCode, "login should redirect through to the signed-in root")

	return client
}

// TestStartRoutes covers the mux that Start builds. The API is under /api. The
// web handler serves everything else.
func TestStartRoutes(t *testing.T) {
	baseURL, _ := startServer(t)

	t.Run("api is mounted under /api", func(t *testing.T) {
		res := get(t, baseURL+"/api/livez")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusOK, res.StatusCode)

		var body map[string]string
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		assert.Contains(t, body, "version")
	})

	// The web handler needs a session. So a signed-out request would reach
	// the login redirect, whether the route exists or not. To tell this from a
	// real 404, the test needs a signed-in client.
	client := loginClient(t, baseURL)

	t.Run("the api prefix is stripped", func(t *testing.T) {
		// Without StripPrefix, this would reach the API handler as
		// /api/livez. With it, /livez outside the prefix is not an API route.
		// It goes to the web handler. The web handler does not know the path,
		// and tells this in HTML.
		res := getWith(t, client, baseURL+"/livez")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusNotFound, res.StatusCode)
		assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
	})

	t.Run("web handler serves the root", func(t *testing.T) {
		res := getWith(t, client, baseURL+"/")
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

	t.Run("a signed-out visitor is sent to sign in", func(t *testing.T) {
		anon := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		res := getWith(t, anon, baseURL+"/")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusFound, res.StatusCode)
		assert.Equal(t, "/login", res.Header.Get("Location"))

		// The auth gate sends the redirect before the mux. So this case is the
		// most likely to lose the headers around it.
		assert.Equal(t, csp, res.Header.Get("Content-Security-Policy"))
		assert.NotEmpty(t, res.Header.Get("X-Request-Id"))
	})

	// The headers apply to the whole mux. A stylesheet or the vendored htmx
	// needs this, because neither goes through a template.
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

	// Because of the policy, htmx is vendored and no markup has an inline
	// style or script. So the page must load only from this origin.
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

	a, _ := testAuth(t)

	err = Start(t.Context(), &Config{
		Addr:   "127.0.0.1",
		Port:   ln.Addr().(*net.TCPAddr).Port,
		Logger: testLogger(),
	}, testConn(t), testStore(t), testValidator(t), a)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "error binding")
}

// patchWith sends a state-changing request with the headers that a browser
// would send from the given site. It also sends a valid token. So the token
// gate never answers, and the test exercises sameOrigin.
func patchWith(t *testing.T, url, apiToken string, headers map[string]string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, url, strings.NewReader("label=x"))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

// The server refuses a state-changing request that a browser says came from a
// different site, with any API token. sameOrigin guards the web UI, which uses
// the session cookie. It also runs before the routes of the API. So a forged
// cross-site write never reaches the token check.
func TestCrossOriginWriteIsRefused(t *testing.T) {
	baseURL, apiToken := startServer(t)

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
			// curl and similar tools send neither header. If the server
			// refused them, every script for the JSON API would break.
			name:    "not a browser at all",
			headers: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := patchWith(t, baseURL+"/api/devices/1", apiToken, tc.headers)
			defer func() { _ = res.Body.Close() }()

			if tc.refused {
				assert.Equal(t, http.StatusForbidden, res.StatusCode)

				return
			}

			// The request reaches the handler. An empty inventory has no such
			// device. In both cases, sameOrigin allowed it.
			assert.NotEqual(t, http.StatusForbidden, res.StatusCode)
		})
	}
}

// The server never refuses reads, from any site. When nothing changes, there
// is nothing to forge.
func TestCrossOriginReadIsAllowed(t *testing.T) {
	baseURL, apiToken := startServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/stats", nil)
	require.NoError(t, err)

	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	assert.Equal(t, http.StatusOK, res.StatusCode)
}

// startServerWithOrigins is startServer with an explicit CORS allow-list.
func startServerWithOrigins(t *testing.T, origins []string) string {
	t.Helper()

	return startServerWith(t, Config{CORSAllowedOrigins: origins})
}

// startServerWith is startServer with the settings in cfg. It fills in the
// address, port and logger.
func startServerWith(t *testing.T, cfg Config) string {
	t.Helper()

	port := freePort(t)
	conn := testConn(t)
	store := testStore(t)
	a, _ := testAuth(t)

	cfg.Addr = "127.0.0.1"
	cfg.Port = port
	cfg.Logger = testLogger()

	errCh := make(chan error, 1)
	ctx := t.Context()

	go func() {
		errCh <- Start(ctx, &cfg, conn, store, testValidator(t), a)
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

// A cross-origin read from a site in the CORS allow-list gets the header that a
// browser needs. Then the calling page can read the response.
func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	baseURL := startServerWithOrigins(t, []string{"https://dashboard.example"})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/stats", nil)
	require.NoError(t, err)

	req.Header.Set("Origin", "https://dashboard.example")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	assert.Equal(t, "https://dashboard.example", res.Header.Get("Access-Control-Allow-Origin"))
}

// A site that is not on the allow-list gets no such header. So the browser does
// not let the calling page read the response, although the server answered.
func TestCORSRefusesUnlistedOrigin(t *testing.T) {
	baseURL := startServerWithOrigins(t, []string{"https://dashboard.example"})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/stats", nil)
	require.NoError(t, err)

	req.Header.Set("Origin", "https://evil.example")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	assert.Empty(t, res.Header.Get("Access-Control-Allow-Origin"))
}

// The server answers a preflight for the state-changing route before the mux.
// It answers only when the preflight asks about a method that the API has.
func TestCORSPreflight(t *testing.T) {
	baseURL := startServerWithOrigins(t, []string{"https://dashboard.example"})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, baseURL+"/api/devices/1", nil)
	require.NoError(t, err)

	req.Header.Set("Origin", "https://dashboard.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPatch)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	assert.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Equal(t, "https://dashboard.example", res.Header.Get("Access-Control-Allow-Origin"))
	assert.Contains(t, res.Header.Get("Access-Control-Allow-Methods"), http.MethodPatch)
}

// With CORSAllowedOrigins unset, no response has CORS headers. So only the
// pages of the server can read it. This is also true for the address on which
// the server listens. A browser never loads an origin from a bind address,
// such as 0.0.0.0.
func TestCORSSendsNoHeadersWithoutOrigins(t *testing.T) {
	baseURL, _ := startServer(t)

	for _, origin := range []string{"https://evil.example", baseURL} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/stats", nil)
		require.NoError(t, err)

		req.Header.Set("Origin", origin)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		_ = res.Body.Close()

		assert.Empty(t, res.Header.Get("Access-Control-Allow-Origin"), origin)
	}
}

// The server refuses a PATCH body larger than maxRequestBodyBytes before it
// decodes all of it. So a body with no limit never reaches memory.
func TestRequestBodyTooLargeIsRejected(t *testing.T) {
	baseURL, apiToken := startServer(t)

	oversized := strings.Repeat("a", maxRequestBodyBytes+1)
	body := `{"notes":"` + oversized + `"}`

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, baseURL+"/api/devices/1", strings.NewReader(body))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Authorization", "Bearer "+apiToken)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	assert.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
}

// postMCP sends an MCP request with no token and returns the response.
func postMCP(t *testing.T, baseURL string) *http.Response {
	t.Helper()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/mcp", strings.NewReader(body))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

// With MCP off, /mcp tells this in a problem document.
func TestMCPDisabledSaysSo(t *testing.T) {
	baseURL, _ := startServer(t)

	res := postMCP(t, baseURL)
	defer func() { _ = res.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "application/problem+json")

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Contains(t, string(b), "server.mcp.enabled")
}

// With MCP on, /mcp reaches the MCP handler, which needs a token.
func TestMCPEnabledIsMounted(t *testing.T) {
	baseURL := startServerWith(t, Config{MCPEnabled: true})

	res := postMCP(t, baseURL)
	defer func() { _ = res.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
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

// The access log records the address of the connection, or the client that a
// trusted proxy forwarded. The server ignores a forwarding header from any
// other sender.
func TestAccessLogRecordsTheClientAddress(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}

	for _, tc := range []struct {
		name    string
		proxies []netip.Prefix
		remote  string
		want    string
	}{
		{name: "no proxies", remote: "198.51.100.9:4711", want: "198.51.100.9"},
		{name: "trusted proxy", proxies: trusted, remote: "192.0.2.1:4711", want: "203.0.113.7"},
		{name: "untrusted sender", proxies: trusted, remote: "198.51.100.9:4711", want: "198.51.100.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			a, _ := testAuth(t)
			h := Handler(&Config{
				Logger:         slog.New(slog.NewJSONHandler(&buf, nil)),
				TrustedProxies: tc.proxies,
			}, testConn(t), testStore(t), testValidator(t), a)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", "203.0.113.7")
			h.ServeHTTP(httptest.NewRecorder(), req)

			var line struct {
				Msg      string `json:"msg"`
				RemoteIP string `json:"remote_ip"`
			}

			for l := range strings.Lines(buf.String()) {
				require.NoError(t, json.Unmarshal([]byte(l), &line))

				if line.Msg == "HTTP Request" {
					break
				}
			}

			assert.Equal(t, "HTTP Request", line.Msg)
			assert.Equal(t, tc.want, line.RemoteIP)
		})
	}
}
