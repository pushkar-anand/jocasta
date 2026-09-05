package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testTokenMiddleware wraps a handler that reports whether it was reached, so
// a test can tell a request refused by the gate from one that passed through.
func testTokenMiddleware(t *testing.T, a *Auth, bypass ...*regexp.Regexp) (http.Handler, *bool) {
	t.Helper()

	jw := response.NewJSONWriter(slog.New(slog.DiscardHandler))

	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	})

	return NewTokenMiddleware(jw, a, bypass...)(next), &reached
}

// problemBody decodes a response body as the problem document the API's
// JSONWriter builds, so a test can assert on it the way a caller would.
func problemBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	return body
}

func TestTokenMiddlewareRejectsAMissingToken(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	h, reached := testTokenMiddleware(t, a)

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *reached)
	assert.Equal(t, `Bearer realm="jocasta"`, rec.Header().Get("WWW-Authenticate"))
	assert.Equal(t, "application/problem+json; charset=utf-8", rec.Header().Get("Content-Type"))

	body := problemBody(t, rec)
	assert.Equal(t, float64(http.StatusUnauthorized), body["status"])
	assert.Equal(t, "missing or invalid API token", body["detail"])
}

func TestTokenMiddlewareRejectsAnInvalidToken(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	h, reached := testTokenMiddleware(t, a)

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	req.Header.Set(authHeaderName, "Bearer jct_notarealtoken")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *reached)
}

func TestTokenMiddlewareRejectsAWriteFromAReadOnlyToken(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	plaintext, _, err := a.CreateToken(t.Context(), 1, "read-only", dbtype.TokenRead)
	require.NoError(t, err)

	h, reached := testTokenMiddleware(t, a)

	req := httptest.NewRequest(http.MethodPatch, "/devices/1", nil)
	req.Header.Set(authHeaderName, "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, *reached)

	body := problemBody(t, rec)
	assert.Equal(t, float64(http.StatusForbidden), body["status"])
	assert.Equal(t, "this token is read-only", body["detail"])
}

func TestTokenMiddlewareAllowsAValidToken(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	plaintext, _, err := a.CreateToken(t.Context(), 1, "full access", dbtype.TokenReadWrite)
	require.NoError(t, err)

	h, reached := testTokenMiddleware(t, a)

	req := httptest.NewRequest(http.MethodPatch, "/devices/1", nil)
	req.Header.Set(authHeaderName, "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.True(t, *reached)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestTokenMiddlewareBypassesNamedPaths(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	h, reached := testTokenMiddleware(t, a, regexp.MustCompile(`^/livez$`))

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.True(t, *reached)
	assert.Equal(t, http.StatusOK, rec.Code)
}
