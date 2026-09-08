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

// The route is behind the admin gate, which reads the session role directly:
// a request with no session carries the empty role, which is not admin, so it
// gets the forbidden page. (The server-level middleware would have redirected
// it to sign in before it ever reached here.)
func TestUsersPageRequiresASession(t *testing.T) {
	t.Parallel()

	rec := get(t, empty(t), "/settings/users")

	assert.Equal(t, http.StatusForbidden, rec.Code)
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
	assert.Contains(t, body, `chip--label">Viewer</span>`, "the new account lists as a Viewer")

	// The create is confirmed once, with the name and role and how to sign in.
	assert.Contains(t, body, `class="notice notice--ok"`)
	assert.Contains(t, body, "reader added as Viewer")
	assert.Contains(t, body, "sign in at /login")

	// The confirmation is a one-shot: a reload does not keep showing it.
	reload := requestAs(t, h, cookies, http.MethodGet, "/settings/users", "").Body.String()
	assert.NotContains(t, reload, "reader added as Viewer")

	// The account created is usable, not just listed.
	readerCookies := signInAs(t, h, "reader", "reader-password-1")
	assert.NotEmpty(t, readerCookies)
}

func TestCreateUserRejectsADuplicateUsername(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"username": {testUsername}, "password": {"another-password-1"}, "role": {"read_write"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/users", form.Encode())
	rec = follow(t, h, cookies, rec)

	require.Equal(t, http.StatusOK, rec.Code, "a refused create lands back on the list, not an error")
	assert.Contains(t, rec.Body.String(), "already taken")
	assert.Contains(t, rec.Body.String(), `value="`+testUsername+`" autofocus`)
	assert.Contains(t, rec.Body.String(), `aria-describedby="username-help username-error"`)
	assert.Contains(t, rec.Body.String(), `aria-invalid="true"`)
	assert.Contains(t, rec.Body.String(), `name="role" value="read_write" checked`)
	assert.NotContains(t, rec.Body.String(), `name="role" value="read" checked`)
	assert.NotContains(t, rec.Body.String(), "another-password-1", "passwords must never be redisplayed")

	// The reason is a one-shot: reloading the list does not keep showing it.
	reload := requestAs(t, h, cookies, http.MethodGet, "/settings/users", "")
	assert.NotContains(t, reload.Body.String(), "already taken", "the message is shown once")
	assert.NotContains(t, reload.Body.String(), `aria-invalid="true"`)
	assert.Contains(t, reload.Body.String(), `name="role" value="read" checked`)
}

// A rejected create reopens the form so its error and the kept values are seen
// without a click, even with no client script running.
func TestCreateUserReopensTheDialogOnError(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"username": {testUsername}, "password": {"another-password-1"}, "role": {"read"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/users", form.Encode())
	body := follow(t, h, cookies, rec).Body.String()

	assert.Contains(t, body, `<dialog class="modal" id="user-dialog" aria-labelledby="user-dialog-title" open>`)

	reload := requestAs(t, h, cookies, http.MethodGet, "/settings/users", "").Body.String()
	assert.NotContains(t, reload, `id="user-dialog" aria-labelledby="user-dialog-title" open`)
}

// The list is the page; the form is a dialog reached from it.
func TestUsersPageLeadsWithTheList(t *testing.T) {
	t.Parallel()

	h := empty(t)
	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/users", "").Body.String()

	table := strings.Index(body, "<table>")
	dialog := strings.Index(body, "<dialog")
	require.Positive(t, table)
	require.Positive(t, dialog)
	assert.Less(t, table, dialog, "the table comes before the add-user dialog")
}

func TestUsersPageMarksTheSignedInRow(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	_, err := a.CreateUser(t.Context(), "someone-else", "another-password-1", dbtype.RoleRead)
	require.NoError(t, err)

	h := newWebHandlerWithAuth(t, testStore(t), a)
	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/users", "").Body.String()

	// Exactly one row -- the seeded admin's -- carries the "You" marker.
	assert.Equal(t, 1, strings.Count(body, ">You</span>"))
	admin := strings.Index(body, testUsername)
	other := strings.Index(body, "someone-else")
	you := strings.Index(body, "You</span>")
	assert.Less(t, admin, you)
	assert.Less(t, you, other, "the marker is on the admin's row, not the other account's")
}

func TestUsersPageExplainsRoles(t *testing.T) {
	t.Parallel()

	h := empty(t)
	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/users", "").Body.String()

	assert.Contains(t, body, "<legend>Role</legend>")
	assert.Contains(t, body, `name="role" value="read"`)
	assert.Contains(t, body, `name="role" value="read_write"`)
	assert.Contains(t, body, "Reads the inventory")
	assert.Contains(t, body, "edits device labels and groups")
}

// The topbar account menu names the signed-in account on an ordinary page.
func TestTopbarShowsTheSignedInAccount(t *testing.T) {
	t.Parallel()

	h := empty(t)
	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/users", "").Body.String()

	assert.Contains(t, body, `<span class="usermenu__name">`+testUsername+`</span>`)
	assert.Contains(t, body, `<span class="usermenu__idname">`+testUsername+`</span>`)
}
