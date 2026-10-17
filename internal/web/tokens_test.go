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

// signIn logs into h as the seeded test user (see testAuth). It returns the
// cookies of the response. A later request uses them to prove its sign-in.
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

// follow sends a GET for the Location of the redirect of an earlier response.
// It sends the same cookies, and any cookies of the redirect response. This is
// the second half of a POST-redirect-GET.
func follow(t *testing.T, h http.Handler, cookies []*http.Cookie, rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()

	require.Equal(t, http.StatusSeeOther, rec.Code, "the POST should redirect to its result")

	loc := rec.Header().Get("Location")
	require.NotEmpty(t, loc)

	return requestAs(t, h, append(cookies, rec.Result().Cookies()...), http.MethodGet, loc, "")
}

// requestAs sends method/target through h with cookies, and an optional
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

// Nothing in the web package redirects a signed-out request as auth.Middleware
// does, because that gate is at the server level. So a request with no session
// that reaches this handler gets an error page.
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

// The list is the page. The create form is a dialog from it.
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

// The account menu of the topbar also names the signed-in account on the
// tokens page.
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

	// A revoke asks first, in a page dialog whose button names the action.
	assert.NotContains(t, body, "hx-confirm")
	assert.Contains(t, body, `data-open="revoke-dialog-`+strconv.FormatInt(id, 10)+`"`)
	assert.Contains(t, body, ">Revoke token</button>")

	// The plaintext shows only once. A second load of the page shows it
	// without the secret, and makes no other token.
	reload := requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")
	require.Equal(t, http.StatusOK, reload.Code)
	assert.NotContains(t, reload.Body.String(), "jct_", "a reload does not show the token again")
	assert.Equal(t, id, onlyTokenRowID(t, reload.Body.String()), "and does not create a second token")

	// A revoke just after the create, while the plaintext is still on the
	// page. The response is the whole list region. It shows the "none yet"
	// line, and has no old plaintext.
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

// The required/maxlength attributes of the form stop an empty name in a
// browser. The handler answers a request that gets past them on a page.
func TestCreateTokenRejectsAMissingName(t *testing.T) {
	t.Parallel()

	h := empty(t)
	cookies := signIn(t, h)

	form := url.Values{"scope": {"read"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/settings/tokens", form.Encode())

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Request could not be processed")
}

// onlyTokenRowID gets the id from the `id="token-row-N"` marker of the one row.
// It fails the test if the page does not have exactly one.
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

// Tokens created at the same time, as by a script that makes several, keep one
// order from load to load: newest first, by creation and then by id.
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

	// Each name shows again in the revoke dialog of its row. The table comes
	// first.
	got := regexp.MustCompile(`token-\d\d`).FindAllString(body, -1)
	require.GreaterOrEqual(t, len(got), len(want))
	assert.Equal(t, want, got[:len(want)])
}

// The plaintext of a new token shows on the page that answers the create. It
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

// The create dialog offers an expiry. Never is the first choice.
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

// Each choice sets the expiry that far from now. Never, or a form with no
// choice, leaves the token without an expiry.
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

// The list tells when each token expires: never, on a date, or that it already
// expired. An expired token stays in the list until a revoke.
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
