package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/pushkar-anand/build-with-go/ctxval"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoginFormRejectsWrongPassword covers auth.ErrInvalidCredentials reaching
// the client through the same status-mapper and error-page-data path as any
// other handler error.
func TestLoginFormRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	h := empty(t)

	form := url.Values{"username": {testUsername}, "password": {"wrong-password"}, "remember_me": {"true"}}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "That username and password do not match.")
	assert.Contains(t, rec.Body.String(), `value="`+testUsername+`"`)
	assert.Contains(t, rec.Body.String(), `name="remember_me" value="true" checked`)
	assert.Contains(t, rec.Body.String(), `aria-describedby="login-error"`)
	assert.NotContains(t, rec.Body.String(), "wrong-password")

	// The username is kept, so the cursor goes where the retry starts: the
	// password.
	assert.Regexp(t, `<input[^>]*id="login-password"[^>]*autofocus`, rec.Body.String())
	assert.NotRegexp(t, `<input[^>]*id="login-username"[^>]*autofocus`, rec.Body.String())
}

// TestLoginFormLocksAfterTooManyPasswords covers auth.ErrLoginLocked reaching
// the sign-in page as a 429 that says to wait, with the username kept.
func TestLoginFormLocksAfterTooManyPasswords(t *testing.T) {
	t.Parallel()

	h := empty(t)

	for range 10 {
		rec := loginWith(t, h, testUsername, "wrong-password")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}

	rec := loginWith(t, h, testUsername, testPassword)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Contains(t, rec.Body.String(), "Too many sign-in attempts. Wait a minute, then try again.")
	assert.Contains(t, rec.Body.String(), `value="`+testUsername+`"`)
}

// TestLoginFormLimitsEachClientAddress covers the sign-in page counting
// attempts by the client address in the request's context: one address that
// has tried too many accounts is refused, and another still signs in.
func TestLoginFormLimitsEachClientAddress(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	names := []string{testUsername}

	for _, name := range []string{"grace", "alan", "linus"} {
		_, err := a.CreateUser(t.Context(), name, testPassword, dbtype.RoleRead)
		require.NoError(t, err)

		names = append(names, name)
	}

	h := newWebHandlerWithAuth(t, testStore(t), a)
	from := netip.MustParseAddr("198.51.100.7")

	// 30 spread over four accounts stays inside each account's 10.
	for i := range 30 {
		rec := loginFrom(t, h, from, names[i%len(names)], "wrong-password")
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}

	rec := loginFrom(t, h, from, testUsername, testPassword)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	rec = loginFrom(t, h, netip.MustParseAddr("198.51.100.8"), testUsername, testPassword)
	assert.Equal(t, http.StatusFound, rec.Code)
}

// loginFrom posts the sign-in form as loginWith does, from the client address
// the server's resolver would have placed in the request's context.
func loginFrom(t *testing.T, h http.Handler, addr netip.Addr, username, password string) *httptest.ResponseRecorder {
	t.Helper()

	form := url.Values{"username": {username}, "password": {password}}

	ctx := ctxval.WithClientAddr(t.Context(), addr)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// Signing out is a POST, since a link would let another site spend the
// session cookie, and it ends the session.
func TestLogoutIsAPostThatEndsTheSession(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	rec := requestAs(t, h, cookies, http.MethodPost, "/logout", "")
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))

	// The session is gone: a page that needs one no longer finds it.
	after := requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")
	assert.Equal(t, http.StatusInternalServerError, after.Code)
}

// Only POST signs out: a GET /logout matches no route and leaves the session
// alone.
func TestLogoutByGetDoesNothing(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	rec := requestAs(t, h, cookies, http.MethodGet, "/logout", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	stillIn := requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")
	assert.Equal(t, http.StatusOK, stillIn.Code)
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
