package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/auth"
)

// enrollTOTP drives Auth's enrollment methods directly (bypassing the HTTP
// layer) to leave username signed up for 2FA, and hands back the secret --
// so a test can compute a valid code with it -- and the recovery codes
// confirming enrollment minted.
func enrollTOTP(t *testing.T, a *auth.Auth, username string) (secret string, recoveryCodes []string) {
	t.Helper()

	ctx := t.Context()

	users, err := a.ListUsers(ctx)
	require.NoError(t, err)

	var userID int64

	for _, u := range users {
		if u.Username == username {
			userID = u.ID
		}
	}

	require.NotZero(t, userID, "seeded user not found")

	key, err := a.StartTOTPEnrollment(ctx, userID, username)
	require.NoError(t, err)

	code, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)

	codes, err := a.ConfirmTOTPEnrollment(ctx, userID, code)
	require.NoError(t, err)

	return key.Secret(), codes
}

// loginWith posts the sign-in form and hands back the response, so a test can
// tell a straight sign-in (redirects to /) from a pending one (redirects to
// /login/totp) by its Location.
func loginWith(t *testing.T, h http.Handler, username, password string) *httptest.ResponseRecorder {
	t.Helper()

	form := url.Values{"username": {username}, "password": {password}}

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// signInWithCode carries a 2FA-enabled account through both steps of sign-in
// -- password, then the current TOTP code for secret -- and hands back the
// cookies a fully signed-in session needs.
func signInWithCode(t *testing.T, h http.Handler, secret string) []*http.Cookie {
	t.Helper()

	pending := loginWith(t, h, testUsername, testPassword)
	require.Equal(t, http.StatusFound, pending.Code)
	require.Equal(t, "/login/totp", pending.Header().Get("Location"))

	cookies := pending.Result().Cookies()

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	form := url.Values{"code": {code}}
	verify := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())
	require.Equal(t, http.StatusFound, verify.Code)

	// establishSession renews the token, so the cookie the pending step set is
	// stale now -- sending both would let the request pick the wrong one.
	return verify.Result().Cookies()
}

func TestLoginRedirectsToTOTPWhenEnabled(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	enrollTOTP(t, a, testUsername)

	rec := loginWith(t, h, testUsername, testPassword)

	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/login/totp", rec.Header().Get("Location"))
}

// TestLoginTOTPWithNoPendingSessionBehavesLikeLogin covers a direct GET (or a
// stale/expired one) reaching /login/totp with nothing pending: it looks
// exactly like a bad /login attempt, the same error and page.
func TestLoginTOTPWithNoPendingSessionBehavesLikeLogin(t *testing.T) {
	t.Parallel()

	h := empty(t)

	rec := requestAs(t, h, nil, http.MethodGet, "/login/totp", "")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "Incorrect username or password.")
}

func TestLoginTOTPRejectsWrongCode(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	enrollTOTP(t, a, testUsername)

	cookies := loginWith(t, h, testUsername, testPassword).Result().Cookies()

	form := url.Values{"code": {"000000"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())

	require.Equal(t, http.StatusPreconditionRequired, rec.Code)
	assert.Contains(t, rec.Body.String(), "Invalid code.")
}

func TestLoginTOTPAcceptsCorrectCode(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	secret, _ := enrollTOTP(t, a, testUsername)

	cookies := signInWithCode(t, h, secret)

	// The second-factor cookie now carries a full session: a route behind it
	// is reachable without going through /login again.
	after := requestAs(t, h, cookies, http.MethodGet, "/settings/tokens", "")
	assert.Equal(t, http.StatusOK, after.Code)
}

func TestLoginTOTPAcceptsRecoveryCodeOnce(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	_, codes := enrollTOTP(t, a, testUsername)
	require.NotEmpty(t, codes)

	cookies := loginWith(t, h, testUsername, testPassword).Result().Cookies()

	form := url.Values{"code": {codes[0]}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))

	// Replaying the same code against a fresh pending sign-in is refused.
	cookies = loginWith(t, h, testUsername, testPassword).Result().Cookies()
	replay := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())
	assert.Equal(t, http.StatusPreconditionRequired, replay.Code)
}

// TestLoginTOTPExhaustedAttemptsEndsTheSession covers the narrow rate limit
// VerifyTOTP applies to a pending sign-in: repeated wrong codes destroy the
// session rather than leaving it guessable indefinitely.
func TestLoginTOTPExhaustedAttemptsEndsTheSession(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	enrollTOTP(t, a, testUsername)

	cookies := loginWith(t, h, testUsername, testPassword).Result().Cookies()

	form := url.Values{"code": {"000000"}}

	for range 4 {
		rec := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())
		require.Equal(t, http.StatusPreconditionRequired, rec.Code)
	}

	last := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())
	require.Equal(t, http.StatusUnauthorized, last.Code, "the fifth wrong code ends the pending session")

	// Nothing is pending any more, so the second-factor page itself now
	// behaves like a bad /login attempt too.
	after := requestAs(t, h, cookies, http.MethodGet, "/login/totp", "")
	assert.Equal(t, http.StatusUnauthorized, after.Code)
}
