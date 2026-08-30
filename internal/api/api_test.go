package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewHandler(testLogger()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))

	// The value is whatever the build stamped in, which is empty for a test
	// binary, so only the shape of the payload is worth asserting.
	require.Contains(t, body, "version")
	assert.Len(t, body, 1)
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	NewHandler(testLogger()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
