package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"

	"github.com/pushkar-anand/build-with-go/http/request"
	"github.com/pushkar-anand/build-with-go/validator"
	"github.com/pushkar-anand/jocasta/internal/db"
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
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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

	t.Cleanup(func() { _ = conn.Conn.Close() })

	return inventory.New(conn.Conn, testLogger())
}

// seeded returns a handler over an inventory holding two swept devices, which
// is enough for every read the API offers.
func seeded(t *testing.T) http.Handler {
	t.Helper()

	store := testStore(t)

	hosts := []scanner.Host{
		{Addr: netip.MustParseAddr("192.0.2.10"), MAC: macA, Hostname: "printer.local"},
		{Addr: netip.MustParseAddr("192.0.2.11"), MAC: macB, Hostname: "nas.local"},
	}

	_, err := store.RecordSweep(t.Context(), "test-sweep", netip.MustParsePrefix(prefix), hosts)
	require.NoError(t, err)

	return NewHandler(testLogger(), testReader(t), store)
}

// get issues a request and decodes the response body as a JSON object.
func get(t *testing.T, h http.Handler, target string) (*http.Response, map[string]any) {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })

	var body map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))

	return res, body
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
	seeded(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// The routes name their method, so a write to a read-only resource is turned
// away by the router rather than reaching a handler.
func TestWriteMethodIsNotAllowed(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	seeded(t).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/devices", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
