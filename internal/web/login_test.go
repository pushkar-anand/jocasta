package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoginFormRejectsWrongPassword covers auth.ErrInvalidCredentials reaching
// the client through the same status-mapper and error-page-data path as any
// other handler error, rather than the handler rendering the failure itself.
func TestLoginFormRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	h := empty(t)

	form := url.Values{"username": {testUsername}, "password": {"wrong-password"}}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "Incorrect username or password.")
}

// TestLoginPageRedirectsASignedInVisitor covers /login sitting outside
// auth.Middleware's gate: reaching the handler while signed in is possible,
// so the handler is what has to send that visitor on.
func TestLoginPageRedirectsASignedInVisitor(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	rec := requestAs(t, h, cookies, http.MethodGet, "/login", "")

	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
}
