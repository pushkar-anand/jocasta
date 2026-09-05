package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signInAs is signIn for a credential other than the seeded test user, for a
// test that needs to sign in as an account it created itself.
func signInAs(t *testing.T, h http.Handler, username, password string) []*http.Cookie {
	t.Helper()

	form := url.Values{"username": {username}, "password": {password}}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code, "login should redirect once signed in")

	return rec.Result().Cookies()
}

// Nothing in the web package itself redirects a signed-out request the way
// the session middleware does -- that gate lives at the server level -- so a
// request with no session reaching this handler surfaces as an error page
// instead, the same as TestTokensPageRequiresASession.
func TestUsersPageRequiresASession(t *testing.T) {
	t.Parallel()

	rec := get(t, empty(t), "/settings/users")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUsersPageForbidsANonAdmin(t *testing.T) {
	t.Parallel()

	a := testAuth(t)

	_, err := a.CreateUser(t.Context(), "reader", "reader-password-1", dbtype.RoleRead)
	require.NoError(t, err)

	h := newWebHandlerWithAuth(t, testStore(t), a)
	cookies := signInAs(t, h, "reader", "reader-password-1")

	rec := requestAs(t, h, cookies, http.MethodGet, "/settings/users", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// The topbar offers the Users link only to an account that /settings/users
// would let in; everyone still gets the links that are theirs.
func TestTopbarHidesUsersLinkFromNonAdmins(t *testing.T) {
	t.Parallel()

	a := testAuth(t)

	_, err := a.CreateUser(t.Context(), "reader", "reader-password-1", dbtype.RoleRead)
	require.NoError(t, err)

	h := newWebHandlerWithAuth(t, testStore(t), a)

	adminBody := requestAs(t, h, signIn(t, h), http.MethodGet, "/", "").Body.String()
	assert.Contains(t, adminBody, `href="/settings/users"`)

	readerBody := requestAs(t, h, signInAs(t, h, "reader", "reader-password-1"), http.MethodGet, "/", "").Body.String()
	assert.NotContains(t, readerBody, `href="/settings/users"`)
	assert.Contains(t, readerBody, `href="/settings/tokens"`, "the links that are theirs stay")
	assert.Contains(t, readerBody, `action="/logout"`)
}

func TestUsersPageListsTheSeededAdmin(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	rec := requestAs(t, h, cookies, http.MethodGet, "/settings/users", "")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, testUsername)
	assert.Contains(t, body, "Admin")

	// The topbar marks the page being shown.
	assert.Contains(t, body, `href="/settings/users" aria-current="page"`)
}

func TestCreateUserAsAdmin(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"username": {"reader"}, "password": {"reader-password-1"}, "role": {"read"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/users", form.Encode())
	rec = follow(t, h, cookies, rec)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "reader")
	assert.Contains(t, body, "Read")

	// The account created is usable, not just listed.
	readerCookies := signInAs(t, h, "reader", "reader-password-1")
	assert.NotEmpty(t, readerCookies)
}

func TestCreateUserRejectsADuplicateUsername(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"username": {testUsername}, "password": {"another-password-1"}, "role": {"read"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/users", form.Encode())
	rec = follow(t, h, cookies, rec)

	require.Equal(t, http.StatusOK, rec.Code, "a refused create lands back on the list, not an error")
	assert.Contains(t, rec.Body.String(), "already taken")

	// The reason is a one-shot: reloading the list does not keep showing it.
	reload := requestAs(t, h, cookies, http.MethodGet, "/settings/users", "")
	assert.NotContains(t, reload.Body.String(), "already taken", "the message is shown once")
}
