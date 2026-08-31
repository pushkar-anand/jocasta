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

	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/scanner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	return NewHandler(testLogger(), store)
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

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	res, body := get(t, seeded(t), "/livez")

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))

	// The value is whatever the build stamped in, which is empty for a test
	// binary, so only the shape of the payload is worth asserting.
	require.Contains(t, body, "version")
	assert.Len(t, body, 1)
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

func TestListDevices(t *testing.T) {
	t.Parallel()

	res, body := get(t, seeded(t), "/devices")

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, float64(2), body["count"])

	devices := list(t, body, "devices")
	require.Len(t, devices, 2)

	device, ok := devices[0].(map[string]any)
	require.True(t, ok)

	// The flattened view is what reaches the wire: an absent column is left out
	// entirely rather than arriving as {"String":"","Valid":false}.
	assert.Contains(t, device, "id")
	assert.Contains(t, device, "online")
	assert.Contains(t, device, "current_addresses")
	assert.NotContains(t, device, "label")
	assert.NotContains(t, device, "notes")
}

func TestListDevicesFilters(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{"unfiltered", "/devices", 2},
		{"by hostname", "/devices?q=nas", 1},
		{"by address", "/devices?q=192.0.2.10", 1},
		{"matching nothing", "/devices?q=absent", 0},
		{"online", "/devices?status=online", 2},
		{"offline", "/devices?status=offline", 0},
		{"sorted", "/devices?sort=address", 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, body := get(t, h, tc.target)

			require.Equal(t, http.StatusOK, res.StatusCode)
			assert.Equal(t, float64(tc.want), body["count"])
			assert.Len(t, list(t, body, "devices"), tc.want)
		})
	}
}

// A filter that cannot be honoured is reported, since returning the unfiltered
// list would look like the filter matched everything.
func TestListDevicesRejectsUnknownFilterValues(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	tests := []struct{ target, field string }{
		{"/devices?status=onlin", "status"},
		{"/devices?sort=vendor", "sort"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			res, body := get(t, h, tc.target)

			// The request parsed and was understood, so it is unprocessable
			// rather than malformed.
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			assert.Equal(t, float64(http.StatusUnprocessableEntity), body["status"])

			// The problem names the parameter it is about, so a client is told
			// which one to fix.
			assert.Contains(t, problemContext(t, body), tc.field)
		})
	}
}

func TestGetDevice(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	_, listed := get(t, h, "/devices")
	first, ok := list(t, listed, "devices")[0].(map[string]any)
	require.True(t, ok)

	id, ok := first["id"].(float64)
	require.True(t, ok)

	res, body := get(t, h, "/devices/"+itoa(id))

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, id, body["id"])

	// Only the detail response carries the address history.
	assert.NotEmpty(t, list(t, body, "addresses"))
}

func TestGetDeviceUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	res, body := get(t, seeded(t), "/devices/4040")

	require.Equal(t, http.StatusNotFound, res.StatusCode)
	assert.Equal(t, float64(http.StatusNotFound), body["status"])
}

func TestGetDeviceRejectsNonNumericID(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	for _, target := range []string{"/devices/abc", "/devices/0", "/devices/-1"} {
		t.Run(target, func(t *testing.T) {
			res, _ := get(t, h, target)
			assert.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}

func TestDeviceEvents(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	_, listed := get(t, h, "/devices")
	first, ok := list(t, listed, "devices")[0].(map[string]any)
	require.True(t, ok)

	id, ok := first["id"].(float64)
	require.True(t, ok)

	res, body := get(t, h, "/devices/"+itoa(id)+"/events")

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.NotEmpty(t, list(t, body, "events"))
}

// The device is looked up first, so history for a device that does not exist is
// a 404 rather than an empty list that reads as "nothing ever happened".
func TestDeviceEventsUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	res, _ := get(t, seeded(t), "/devices/4040/events")
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestListEventsAndScans(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	res, body := get(t, h, "/events")
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.NotEmpty(t, list(t, body, "events"))

	res, body = get(t, h, "/scans")
	require.Equal(t, http.StatusOK, res.StatusCode)

	scans := list(t, body, "scans")
	require.Len(t, scans, 1)

	scan, ok := scans[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-sweep", scan["source"])
	assert.Equal(t, prefix, scan["network"])
	assert.Equal(t, "OK", scan["status"])
}

func TestPagingIsBounded(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	res, body := get(t, h, "/events?limit=1")
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Len(t, list(t, body, "events"), 1)

	// A limit that is not a number never becomes one, so it is malformed;
	// a limit that is a number but out of range is understood and refused.
	res, _ = get(t, h, "/events?limit=0.5")
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)

	tests := []struct{ target, field string }{
		{"/events?limit=-1", "limit"},
		{"/events?limit=99999", "limit"},
		{"/events?offset=-1", "offset"},
		{"/scans?limit=99999", "limit"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			res, body := get(t, h, tc.target)

			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			assert.Contains(t, problemContext(t, body), tc.field)
		})
	}
}

func TestStatsAndGroups(t *testing.T) {
	t.Parallel()

	h := seeded(t)

	res, body := get(t, h, "/stats")
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, float64(2), body["total"])
	assert.Equal(t, float64(2), body["online"])
	assert.Equal(t, float64(0), body["offline"])

	res, body = get(t, h, "/groups")
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Nothing has been grouped, so the key is present and holds nothing.
	require.Contains(t, body, "groups")
	assert.Empty(t, body["groups"])
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
