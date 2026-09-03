package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/hosts"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures every test in the package shares live here, alongside the tests
// for the router api.go builds. The handlers themselves are tested in the file
// named after the one that defines them.

// Addresses come from RFC 5737 and hardware addresses from RFC 7042, both
// reserved for documentation, so nothing here names a real device.
const (
	prefix = "192.0.2.0/24"

	macA = "00:00:5e:00:53:01"
	macB = "00:00:5e:00:53:02"
)

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testReader builds the request reader the server wires in, so the tests read
// and validate a query the way the running service does.
func testReader(t *testing.T) *request.Reader {
	t.Helper()

	v, err := validator.New()
	require.NoError(t, err)

	return request.NewReader(testLogger(), v)
}

// testStore opens an inventory over a migrated database scoped to the test.
func testStore(t *testing.T) *inventory.Store {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	return inventory.New(conn, testLogger())
}

// seeded returns a handler over an inventory holding two swept devices, which
// is enough for every read the API offers.
func seeded(t *testing.T) http.Handler {
	t.Helper()

	store := testStore(t)

	swept := []scanner.Host{
		host("192.0.2.10", macA, "printer.local"),
		host("192.0.2.11", macB, "nas.local"),
	}

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), swept)
	require.NoError(t, err)

	return NewHandler(testLogger(), testReader(t), store)
}

// get issues a request, decodes the response body, and closes it. It returns the
// status, headers, and decoded body, so callers never hold an open response.
func get(t *testing.T, h http.Handler, target string) (int, http.Header, map[string]any) {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))

	res := rec.Result()

	var body map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.NoError(t, res.Body.Close())

	return res.StatusCode, res.Header, body
}

// list pulls a named array out of a decoded response.
func list(t *testing.T, body map[string]any, key string) []any {
	t.Helper()

	require.Contains(t, body, key)

	items, ok := body[key].([]any)
	require.True(t, ok, "%s should be an array, got %T", key, body[key])

	return items
}

// problemContext returns the per-field problems a validation failure carries.
// They are nested rather than merged into the document, so a field named after
// a standard member -- "status" -- does not overwrite it.
func problemContext(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	require.Contains(t, body, "context")

	fields, ok := body["context"].(map[string]any)
	require.True(t, ok, "context should be an object, got %T", body["context"])

	return fields
}

// itoa renders an id that arrived as a JSON number.
func itoa(f float64) string {
	return strconv.FormatInt(int64(f), 10)
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	seeded(t).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nope", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// The routes name their method, so a write to a read-only resource is turned
// away by the router rather than reaching a handler.
func TestWriteMethodIsNotAllowed(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	seeded(t).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/devices", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// patchJSON sends a JSON body, decodes the response, and closes it. Like get, it
// returns the status, headers, and decoded body.
func patchJSON(t *testing.T, h http.Handler, target, body string) (int, http.Header, map[string]any) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	res := rec.Result()

	var decoded map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&decoded))
	require.NoError(t, res.Body.Close())

	return res.StatusCode, res.Header, decoded
}

// The read routes name their method, so a write to one is refused.
//
// The refusal comes from the router rather than a handler, so it is plain text
// rather than a problem document -- which is why this does not decode it.
func TestUpdateIsOnlyAllowedOnTheDeviceItself(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/devices", "/devices/1/events", "/stats"} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, target, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		})
	}
}

// host builds a swept host the way a sweep does. A malformed argument is a
// broken test.
func host(ip, mac, hostname string) scanner.Host {
	h, err := hosts.BuildHost(context.Background(), hosts.HostInput{IP: ip, MAC: mac, Hostname: hostname})
	if err != nil {
		panic(err)
	}

	return scanner.Host{Host: h}
}
