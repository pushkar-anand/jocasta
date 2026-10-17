package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file has the fixtures that all tests in the package share. It also has
// the tests for the handler that mcp.go builds. The tests of each tool are in
// the file with the name of the file that defines the tool.

// Addresses come from RFC 5737, and hardware addresses from RFC 7042. Both are
// for documentation. So nothing here names a real device.
const (
	prefix = "192.0.2.0/24"

	macA = "00:00:5e:00:53:01"
	macB = "00:00:5e:00:53:02"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testJSONWriter builds the writer that the server gives the MCP handler. This
// is the writer of the JSON API.
func testJSONWriter() *response.JSONWriter {
	return response.NewJSONWriter(testLogger())
}

// testStore opens an inventory over a migrated database for the test only.
func testStore(t *testing.T) *inventory.Store {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return inventory.New(conn, testLogger())
}

// seededStore returns an inventory with two swept devices.
func seededStore(t *testing.T) *inventory.Store {
	t.Helper()

	store := testStore(t)

	swept := []scanner.Host{
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	}

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), swept)
	require.NoError(t, err)

	return store
}

func host(ip, mac, hostname string) scanner.Host {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{IP: ip, MAC: mac, Hostname: hostname})
	if err != nil {
		panic(err)
	}

	return scanner.Host{Host: h}
}

// tokens holds one API token of each scope. Both work on the Auth that
// testAuth returns.
type tokens struct {
	read, readWrite string
}

// testAuth builds an Auth over its own migrated database. It has one user,
// with one token of each scope.
func testAuth(t *testing.T) (*auth.Auth, tokens) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "auth.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	q := models.New(conn)

	user, err := q.CreateUser(t.Context(), models.CreateUserParams{
		Username:     "agent-owner",
		PasswordHash: "unused",
		Role:         dbtype.RoleAdmin,
	})
	require.NoError(t, err)

	a, err := auth.New(conn, q, password.NewHasher())
	require.NoError(t, err)

	read, _, err := a.CreateToken(t.Context(), user.ID, "read", dbtype.TokenRead, time.Time{})
	require.NoError(t, err)

	readWrite, _, err := a.CreateToken(t.Context(), user.ID, "read-write", dbtype.TokenReadWrite, time.Time{})
	require.NoError(t, err)

	return a, tokens{read: read, readWrite: readWrite}
}

// bearer adds a token to every request that a client sends.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)

	return b.next.RoundTrip(r)
}

// connectHTTP connects an MCP client to url over Streamable HTTP, with token.
// It returns the session, which closes when the test ends.
func connectHTTP(t *testing.T, url, token string) *mcpsdk.ClientSession {
	t.Helper()

	transport := &mcpsdk.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}},
	}

	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client"}, nil).
		Connect(t.Context(), transport, nil)
	require.NoError(t, err)

	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

// toolNames lists the tools that a session gets.
func toolNames(t *testing.T, cs *mcpsdk.ClientSession) []string {
	t.Helper()

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)

	names := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}

	return names
}

// postWithToken sends a bare JSON-RPC request, for the cases that a real
// client does not send. It returns the status and the decoded body.
func postWithToken(t *testing.T, url, token string) (int, map[string]any) {
	t.Helper()

	payload := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(payload))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = res.Body.Close() }()

	var body map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))

	return res.StatusCode, body
}

func TestHandlerRequiresAToken(t *testing.T) {
	t.Parallel()

	a, _ := testAuth(t)

	srv := httptest.NewServer(NewHandler(testLogger(), testJSONWriter(), a, testStore(t)))
	t.Cleanup(srv.Close)

	// The refusal is the problem document of the JSON API, exactly.
	for name, token := range map[string]string{
		"none":                   "",
		"not one jocasta issued": "jct_not-a-real-token",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			status, body := postWithToken(t, srv.URL, token)
			assert.Equal(t, http.StatusUnauthorized, status)
			assert.Equal(t, float64(http.StatusUnauthorized), body["status"])
			assert.Equal(t, "API token not recognised. Send an active token in the Authorization header as Bearer <token>.", body["detail"])
		})
	}
}

// A read-scoped token can use the read tools from end to end over HTTP.
func TestHandlerServesAReadToken(t *testing.T) {
	t.Parallel()

	a, tok := testAuth(t)

	srv := httptest.NewServer(NewHandler(testLogger(), testJSONWriter(), a, seededStore(t)))
	t.Cleanup(srv.Close)

	cs := connectHTTP(t, srv.URL, tok.read)

	assert.Contains(t, toolNames(t, cs), "list_devices")

	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "list_devices"})
	require.NoError(t, err)
	require.False(t, res.IsError, "list_devices failed: %v", res.Content)

	assert.Equal(t, 2, decodeDevices(t, res).Count)
}

// The scope of a token sets which tools it sees. A read-scoped token never
// gets a tool that writes. A read_write token gets both.
func TestHandlerOffersWriteToolsOnlyToAReadWriteToken(t *testing.T) {
	t.Parallel()

	a, tok := testAuth(t)

	ts := []tool{
		{register: fakeTool("reader")},
		{writes: true, register: fakeTool("writer")},
	}

	srv := httptest.NewServer(newHandler(testLogger(), testJSONWriter(), a, ts))
	t.Cleanup(srv.Close)

	t.Run("read", func(t *testing.T) {
		t.Parallel()

		assert.ElementsMatch(t, []string{"reader"}, toolNames(t, connectHTTP(t, srv.URL, tok.read)))
	})

	t.Run("read_write", func(t *testing.T) {
		t.Parallel()

		assert.ElementsMatch(t, []string{"reader", "writer"}, toolNames(t, connectHTTP(t, srv.URL, tok.readWrite)))
	})
}

// fakeTool registers a tool that does nothing, for tests about which tools a
// token gets.
func fakeTool(name string) func(*mcpsdk.Server, *slog.Logger) {
	return func(s *mcpsdk.Server, _ *slog.Logger) {
		mcpsdk.AddTool(s, &mcpsdk.Tool{Name: name}, func(
			context.Context, *mcpsdk.CallToolRequest, struct{},
		) (*mcpsdk.CallToolResult, any, error) {
			return nil, nil, nil
		})
	}
}

// The real tool list does not give its write tool to a read-scoped token.
func TestHandlerKeepsCurationFromAReadToken(t *testing.T) {
	t.Parallel()

	a, tok := testAuth(t)

	srv := httptest.NewServer(NewHandler(testLogger(), testJSONWriter(), a, seededStore(t)))
	t.Cleanup(srv.Close)

	assert.NotContains(t, toolNames(t, connectHTTP(t, srv.URL, tok.read)), "update_device_curation")
	assert.Contains(t, toolNames(t, connectHTTP(t, srv.URL, tok.readWrite)), "update_device_curation")
	assert.NotContains(t, toolNames(t, connectHTTP(t, srv.URL, tok.read)), "watch_device")
	assert.Contains(t, toolNames(t, connectHTTP(t, srv.URL, tok.readWrite)), "watch_device")
}
