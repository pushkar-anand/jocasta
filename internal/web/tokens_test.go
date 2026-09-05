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
}

func TestCreateAndRevokeToken(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"name": {"CI script"}, "scope": {"read_write"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "CI script")
	assert.Contains(t, body, "jct_", "the plaintext token is shown once, on this response")
	assert.Contains(t, body, "Read &amp; write")

	id := onlyTokenRowID(t, body)

	rec = requestAs(t, h, cookies, http.MethodDelete, "/settings/tokens/"+strconv.FormatInt(id, 10), "")
	require.Equal(t, http.StatusOK, rec.Code)

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

	assert.NotEqual(t, http.StatusOK, rec.Code)
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
