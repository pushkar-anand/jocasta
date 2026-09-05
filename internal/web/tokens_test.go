package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signIn logs into h as the seeded test user (see testAuth) and returns the
// cookies the response set, for a later request to prove it is signed in
// with.
func signIn(t *testing.T, h http.Handler) []*http.Cookie {
	t.Helper()

	form := url.Values{"username": {testUsername}, "password": {testPassword}}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code, "login should redirect once signed in")

	return rec.Result().Cookies()
}

// follow issues a GET for the Location a prior response redirected to, carrying
// the same cookies plus any the redirecting response set -- the second half of
// a POST-redirect-GET.
func follow(t *testing.T, h http.Handler, cookies []*http.Cookie, rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()

	require.Equal(t, http.StatusSeeOther, rec.Code, "the POST should redirect to its result")

	loc := rec.Header().Get("Location")
	require.NotEmpty(t, loc)

	return requestAs(t, h, append(cookies, rec.Result().Cookies()...), http.MethodGet, loc, "")
}

// requestAs issues method/target through h carrying cookies, with an optional
// form-encoded body.
func requestAs(t *testing.T, h http.Handler, cookies []*http.Cookie, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var r *strings.Reader
	if body != "" {
		r = strings.NewReader(body)
	} else {
		r = strings.NewReader("")
	}

	req := httptest.NewRequestWithContext(t.Context(), method, target, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// Nothing in the web package itself redirects a signed-out request the way
// auth.Middleware does -- that gate lives at the server level -- so a request
// with no session reaching this handler surfaces as an error page instead.
func TestTokensPageRequiresASession(t *testing.T) {
	t.Parallel()

	rec := get(t, empty(t), "/settings/tokens")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestTokensPageListsNoneToStart(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	rec := requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No tokens yet.")
	assert.Contains(t, rec.Body.String(), `href="/settings/tokens" aria-current="page"`)
}

func TestCreateAndRevokeToken(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"name": {"CI script"}, "scope": {"read_write"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())
	rec = follow(t, h, cookies, rec)
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "CI script")
	assert.Contains(t, body, "jct_", "the plaintext is shown once, on the page the create redirects to")
	assert.Contains(t, body, "Read &amp; write")

	id := onlyTokenRowID(t, body)

	// The plaintext is a one-shot: a reload of the same page re-fetches it
	// without the secret, and without minting another token.
	reload := requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")
	require.Equal(t, http.StatusOK, reload.Code)
	assert.NotContains(t, reload.Body.String(), "jct_", "a reload does not show the token again")
	assert.Equal(t, id, onlyTokenRowID(t, reload.Body.String()), "and does not create a second token")

	// Revoking straight after the create, while the reveal is still on the
	// page: the response is the whole list region, showing the "none yet"
	// line rather than an empty table, and carrying no leftover plaintext.
	rec = requestAs(t, h, cookies, http.MethodDelete, "/settings/tokens/"+strconv.FormatInt(id, 10), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No tokens yet.")
	assert.NotContains(t, rec.Body.String(), "CI script")
	assert.NotContains(t, rec.Body.String(), "jct_")

	rec = requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No tokens yet.", "the revoked token should no longer be listed")
	assert.NotContains(t, rec.Body.String(), "CI script")
}

func TestCreateTokenRejectsAnUnknownScope(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"name": {"bad"}, "scope": {"admin"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Internal Server Error")
}

// The form's own required/maxlength attributes stop an empty name in a browser;
// a request that gets past them is answered on a page, not with a bare 500.
func TestCreateTokenRejectsAMissingName(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"scope": {"read"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Bad request")
}

// onlyTokenRowID pulls the id out of the one row's `id="token-row-N"` marker,
// failing the test if the page does not have exactly one.
func onlyTokenRowID(t *testing.T, body string) int64 {
	t.Helper()

	const marker = `id="token-row-`

	start := strings.Index(body, marker)
	require.NotEqual(t, -1, start, "expected a token row in the response")

	start += len(marker)
	end := strings.IndexByte(body[start:], '"')
	require.NotEqual(t, -1, end)

	id, err := strconv.ParseInt(body[start:start+end], 10, 64)
	require.NoError(t, err)

	require.Equal(t, -1, strings.Index(body[start+end:], marker), "expected only one token row")

	return id
}
