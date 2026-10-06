package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
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
// the same cookies plus any the redirecting response set: the second half of
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
// auth.Middleware does, since that gate lives at the server level, so a
// request with no session reaching this handler surfaces as an error page.
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

// The list is the page; the create form is a dialog reached from it.
func TestTokensPageLeadsWithTheList(t *testing.T) {
	t.Parallel()

	h := empty(t)
	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/tokens", "").Body.String()

	list := strings.Index(body, `id="token-list"`)
	dialog := strings.Index(body, `<dialog class="modal"`)

	require.Positive(t, list)
	require.Positive(t, dialog)
	assert.Less(t, list, dialog, "the list region comes before the create dialog")
	assert.Contains(t, body, "<legend>Permission</legend>")
}

// The topbar account menu names the signed-in account on the tokens page too.
func TestTokensPageShowsTheSignedInAccount(t *testing.T) {
	t.Parallel()

	h := empty(t)
	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/tokens", "").Body.String()

	assert.Contains(t, body, `<span class="usermenu__name">`+testUsername+`</span>`)
	assert.Contains(t, body, "Tokens belong to "+testUsername)
}

func TestCreateAndRevokeToken(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"name": {"CI script"}, "scope": {"read_write"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "CI script created", "the completion state names what was made")
	assert.Contains(t, body, "jct_", "the plaintext is shown once, on the page that answers the create")
	assert.Contains(t, body, "Editor", "read_write renders as the Editor label")
	assert.Contains(t, body, `data-copy="#token-plaintext"`, "the completion state offers a Copy control")
	assert.Contains(t, body, "Authorization: Bearer", "and a runnable bearer-token example")

	id := onlyTokenRowID(t, body)

	// Revoking asks first, in a page dialog whose button names the action.
	assert.NotContains(t, body, "hx-confirm")
	assert.Contains(t, body, `data-open="revoke-dialog-`+strconv.FormatInt(id, 10)+`"`)
	assert.Contains(t, body, ">Revoke token</button>")

	// The plaintext is a one-shot: loading the page again shows it without
	// the secret, and without minting another token.
	reload := requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")
	require.Equal(t, http.StatusOK, reload.Code)
	assert.NotContains(t, reload.Body.String(), "jct_", "a reload does not show the token again")
	assert.Equal(t, id, onlyTokenRowID(t, reload.Body.String()), "and does not create a second token")

	// Revoking straight after the create, while the reveal is still on the
	// page: the response is the whole list region, showing the "none yet"
	// line and carrying no leftover plaintext.
	rec = requestAs(t, h, cookies, http.MethodDelete, "/settings/tokens/"+strconv.FormatInt(id, 10), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No tokens yet.")
	assert.Contains(t, rec.Body.String(), `role="status"`, "the revoke is announced")
	assert.Contains(t, rec.Body.String(), "Token revoked.")
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
// a request that gets past them is answered on a page.
func TestCreateTokenRejectsAMissingName(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"scope": {"read"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Request could not be processed")
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

// Tokens created in the same moment, as a script minting several does, keep
// one order from load to load: newest first, by creation and then by id.
func TestTokensListInAFixedOrder(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)

	users, err := a.ListUsers(t.Context())
	require.NoError(t, err)

	want := make([]string, 0, 30)

	for i := range 30 {
		name := fmt.Sprintf("token-%02d", i)
		_, _, err := a.CreateToken(t.Context(), users[0].ID, name, dbtype.TokenRead, time.Time{})
		require.NoError(t, err)

		want = append([]string{name}, want...)
	}

	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/tokens", "").Body.String()

	// Each name appears again in its row's revoke dialog; the table comes first.
	got := regexp.MustCompile(`token-\d\d`).FindAllString(body, -1)
	require.GreaterOrEqual(t, len(got), len(want))
	assert.Equal(t, want, got[:len(want)])
}

// A new token's plaintext is shown on the page that answers the create, and
// never reaches the session, which the server keeps in its database.
func TestCreatedTokenStaysOutOfTheSessionStore(t *testing.T) {
	t.Parallel()

	h, conn := sessionStoreHandler(t, testAuth(t))
	cookies := signIn(t, h)

	form := url.Values{"name": {"CI script"}, "scope": {"read"}}
	page := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())
	require.Equal(t, http.StatusOK, page.Code)

	stored := storedSessions(t, conn)

	plaintext := regexp.MustCompile(`id="token-plaintext">([^<]+)<`).FindStringSubmatch(page.Body.String())
	require.Len(t, plaintext, 2, "the page that answers the create shows the token")

	for _, data := range stored {
		assert.False(t, strings.Contains(string(data), plaintext[1]), "the session store holds the token")
	}

	reload := requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")
	assert.NotContains(t, reload.Body.String(), plaintext[1], "a reload does not show the token again")
}

// The create dialog offers an expiry, with Never chosen to begin with.
func TestTokenDialogOffersAnExpiry(t *testing.T) {
	t.Parallel()

	h := empty(t)
	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/tokens", "").Body.String()

	assert.Contains(t, body, `<select class="input" name="expires">`)
	assert.Contains(t, body, `<option value="never" selected>Never</option>`)
	assert.Contains(t, body, `<option value="30d">30 days</option>`)
	assert.Contains(t, body, `<option value="90d">90 days</option>`)
	assert.Contains(t, body, `<option value="1y">1 year</option>`)
}

// Each choice sets the expiry that far from now; Never, or a form with no
// choice, leaves the token without one.
func TestCreateTokenSetsTheChosenExpiry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expires string
		years   int
		days    int
	}{
		{expires: ""},
		{expires: "never"},
		{expires: "30d", days: 30},
		{expires: "90d", days: 90},
		{expires: "1y", years: 1},
	}

	for _, tt := range tests {
		t.Run(tt.expires, func(t *testing.T) {
			t.Parallel()

			a := testAuth(t)
			store := testStore(t)
			h := newWebHandlerWithAuth(t, store, a)
			cookies := signIn(t, h)

			form := url.Values{"name": {"CI script"}, "scope": {"read"}}
			if tt.expires != "" {
				form.Set("expires", tt.expires)
			}

			rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())
			require.Equal(t, http.StatusOK, rec.Code)

			users, err := a.ListUsers(t.Context())
			require.NoError(t, err)

			tokens, err := a.ListTokens(t.Context(), users[0].ID)
			require.NoError(t, err)
			require.Len(t, tokens, 1)

			got := tokens[0].ExpiresAt
			if tt.years == 0 && tt.days == 0 {
				assert.False(t, got.Valid, "the token never expires")
				return
			}

			require.True(t, got.Valid)
			assert.WithinDuration(t, store.Now().AddDate(tt.years, 0, tt.days), got.Time.Time, time.Minute)
		})
	}
}

func TestCreateTokenRejectsAnUnknownExpiry(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"name": {"CI script"}, "scope": {"read"}, "expires": {"7d"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// The list says when each token expires: never, on a date, or that it
// already has. An expired token stays listed until it is revoked.
func TestTokensListShowsExpiry(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	store := testStore(t)
	h := newWebHandlerWithAuth(t, store, a)

	users, err := a.ListUsers(t.Context())
	require.NoError(t, err)

	later := time.Date(2099, time.January, 1, 12, 0, 0, 0, time.UTC)

	for name, expiresAt := range map[string]time.Time{
		"grafana": {},
		"backup":  store.Now().Add(-time.Hour),
		"ci":      later,
	} {
		_, _, err := a.CreateToken(t.Context(), users[0].ID, name, dbtype.TokenRead, expiresAt)
		require.NoError(t, err)
	}

	body := requestAs(t, h, signIn(t, h), http.MethodGet, "/settings/tokens", "").Body.String()

	assert.Contains(t, body, `<th scope="col">Expires</th>`)
	assert.Regexp(t, `>grafana</td>(?s:.*?)<td class="dim">Never</td>`, body)
	assert.Regexp(t, `>backup</td>(?s:.*?)<span class="chip chip--warn chip--label">Expired</span>`, body)
	assert.Regexp(t, `>ci</td>(?s:.*?)<time datetime="`+later.Local().Format(time.DateOnly)+`">`+later.Local().Format("2 Jan 2006")+`</time>`, body)
}
