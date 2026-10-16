package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/auth"
)

// enrollTOTP calls the enrollment methods of Auth directly, without the HTTP
// layer. It leaves username enrolled in 2FA. It returns the secret, so a test
// can compute a valid code. It also returns the recovery codes of the
// confirmation.
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

// loginWith posts the sign-in form, and returns the response. So a test can
// tell a direct sign-in (redirects to /) from a pending sign-in (redirects to
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

// signInWithCode takes an account with 2FA through both steps of sign-in
// (password, then the current TOTP code for secret). It returns the cookies
// that a fully signed-in session needs.
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

	// establishSession renews the token. So the cookie of the pending step is
	// now old. If the test sent both, the request could pick the wrong one.
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

// TestLoginTOTPWithNoPendingSessionBehavesLikeLogin covers a direct GET (or an
// old or expired GET) to /login/totp with nothing pending. It looks exactly
// like a bad /login attempt, with the same error and page.
func TestLoginTOTPWithNoPendingSessionBehavesLikeLogin(t *testing.T) {
	t.Parallel()

	h := empty(t)

	rec := requestAs(t, h, nil, http.MethodGet, "/login/totp", "")

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "That username and password do not match.")
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
	assert.Contains(t, rec.Body.String(), "That code did not work.")
}

func TestLoginTOTPAcceptsCorrectCode(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	secret, _ := enrollTOTP(t, a, testUsername)

	cookies := signInWithCode(t, h, secret)

	// The second-factor cookie now has a full session. A route behind it is
	// reachable without /login again.
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

	// The handler refuses the same code again on a new pending sign-in.
	cookies = loginWith(t, h, testUsername, testPassword).Result().Cookies()
	replay := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())
	assert.Equal(t, http.StatusPreconditionRequired, replay.Code)
}

// TestLoginTOTPPageOffersBothCodeKinds covers the two forms of the
// second-factor page. The default asks phones for a numeric keypad. The
// recovery form drops it, so a person can type a code with letters and
// dashes.
func TestLoginTOTPPageOffersBothCodeKinds(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	enrollTOTP(t, a, testUsername)

	cookies := loginWith(t, h, testUsername, testPassword).Result().Cookies()

	code := requestAs(t, h, cookies, http.MethodGet, "/login/totp", "")
	require.Equal(t, http.StatusOK, code.Code)
	assert.Contains(t, code.Body.String(), `inputmode="numeric"`)
	assert.Contains(t, code.Body.String(), `autocomplete="one-time-code"`)
	assert.Contains(t, code.Body.String(), `href="/login/totp?method=recovery"`)
	assert.NotContains(t, code.Body.String(), `name="method"`)

	recovery := requestAs(t, h, cookies, http.MethodGet, "/login/totp?method=recovery", "")
	require.Equal(t, http.StatusOK, recovery.Code)
	assert.Contains(t, recovery.Body.String(), "Recovery code")
	assert.NotContains(t, recovery.Body.String(), `inputmode="numeric"`)
	assert.NotContains(t, recovery.Body.String(), `autocomplete="one-time-code"`)
	assert.Contains(t, recovery.Body.String(), `name="method" value="recovery"`)
	assert.Contains(t, recovery.Body.String(), `href="/login/totp"`)
}

// TestLoginTOTPRecoveryFormKeepsItsModeOnError covers a recovery code that did
// not work. The page comes back in its recovery form. So the visitor keeps the
// full keyboard for the next try.
func TestLoginTOTPRecoveryFormKeepsItsModeOnError(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	enrollTOTP(t, a, testUsername)

	cookies := loginWith(t, h, testUsername, testPassword).Result().Cookies()

	form := url.Values{"code": {"ZZZZ-ZZZZ-ZZZZ-ZZZZ"}, "method": {"recovery"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())

	require.Equal(t, http.StatusPreconditionRequired, rec.Code)
	assert.Contains(t, rec.Body.String(), "That code did not work.")
	assert.Contains(t, rec.Body.String(), `name="method" value="recovery"`)
	assert.NotContains(t, rec.Body.String(), `inputmode="numeric"`)
}

// TestLoginTOTPRecoveryFormAcceptsRecoveryCode covers a recovery code posted
// from the recovery form, with its method field.
func TestLoginTOTPRecoveryFormAcceptsRecoveryCode(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	_, codes := enrollTOTP(t, a, testUsername)
	require.NotEmpty(t, codes)

	cookies := loginWith(t, h, testUsername, testPassword).Result().Cookies()

	form := url.Values{"code": {codes[0]}, "method": {"recovery"}}
	rec := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form.Encode())

	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
}

// TestLoginTOTPExhaustedAttemptsEndsTheSession covers the strict rate limit of
// VerifyTOTP on a pending sign-in. Repeated wrong codes end the session. So
// nobody can guess at it without end.
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
	require.Equal(t, http.StatusTooManyRequests, last.Code, "the fifth wrong code ends the pending session")
	assert.Contains(t, last.Body.String(), "Too many codes did not work. Wait a few minutes, then try again.")

	// Nothing is pending now. So the second-factor page itself now works as a
	// bad /login attempt.
	after := requestAs(t, h, cookies, http.MethodGet, "/login/totp", "")
	assert.Equal(t, http.StatusUnauthorized, after.Code)
}

// TestLoginTOTPParallelGuessesShareOneLimit covers wrong codes, all sent at
// once against one pending sign-in. They use the same limit as codes sent one
// after another. So no more than four get another try. The handler then
// refuses the correct code.
func TestLoginTOTPParallelGuessesShareOneLimit(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	secret, _ := enrollTOTP(t, a, testUsername)

	cookies := loginWith(t, h, testUsername, testPassword).Result().Cookies()
	form := url.Values{"code": {"000000"}}.Encode()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		statuses []int
	)

	for range 20 {
		wg.Go(func() {
			rec := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form)

			mu.Lock()
			defer mu.Unlock()

			statuses = append(statuses, rec.Code)
		})
	}

	wg.Wait()

	retries := 0

	for _, s := range statuses {
		switch s {
		case http.StatusPreconditionRequired:
			retries++
		case http.StatusTooManyRequests, http.StatusUnauthorized:
		default:
			t.Errorf("unexpected status %d", s)
		}
	}

	assert.LessOrEqual(t, retries, 4, "wrong codes that were offered another try")

	pending := loginWith(t, h, testUsername, testPassword).Result().Cookies()
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	rec := requestAs(t, h, pending, http.MethodPost, "/login/totp", url.Values{"code": {code}}.Encode())
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

// TestLoginTOTPFreshSignInKeepsTheLimit covers a new start with the password.
// The wrong codes from before still count against the account.
func TestLoginTOTPFreshSignInKeepsTheLimit(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	secret, _ := enrollTOTP(t, a, testUsername)

	form := url.Values{"code": {"000000"}}.Encode()

	cookies := loginWith(t, h, testUsername, testPassword).Result().Cookies()
	for range 4 {
		rec := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form)
		require.Equal(t, http.StatusPreconditionRequired, rec.Code)
	}

	cookies = loginWith(t, h, testUsername, testPassword).Result().Cookies()
	rec := requestAs(t, h, cookies, http.MethodPost, "/login/totp", form)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	cookies = loginWith(t, h, testUsername, testPassword).Result().Cookies()
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	rec = requestAs(t, h, cookies, http.MethodPost, "/login/totp", url.Values{"code": {code}}.Encode())
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}
