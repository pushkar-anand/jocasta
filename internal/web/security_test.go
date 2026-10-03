package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurityEnrollAndConfirmShowsRecoveryCodesOnce(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	cookies := signIn(t, h)

	enroll := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/enroll", "")
	require.Equal(t, http.StatusSeeOther, enroll.Code)

	page := requestAs(t, h, cookies, http.MethodGet, "/settings/security", "")
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), "totp-qr.png")

	users, err := a.ListUsers(t.Context())
	require.NoError(t, err)

	var userID int64

	for _, u := range users {
		if u.Username == testUsername {
			userID = u.ID
		}
	}

	require.NotZero(t, userID)

	key, err := a.PendingTOTPKey(t.Context(), userID)
	require.NoError(t, err)

	code, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)

	confirm := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/confirm",
		url.Values{"code": {code}}.Encode())
	require.Equal(t, http.StatusSeeOther, confirm.Code)

	reveal := requestAs(t, h, cookies, http.MethodGet, "/settings/security", "")
	require.Equal(t, http.StatusOK, reveal.Code)
	body := reveal.Body.String()
	assert.Contains(t, body, "Two-factor authentication is on.")
	assert.Contains(t, body, "10 recovery codes remaining")

	// The recovery codes are a one-shot flash: gone on the next load.
	again := requestAs(t, h, cookies, http.MethodGet, "/settings/security", "")
	require.Equal(t, http.StatusOK, again.Code)
	assert.NotContains(t, again.Body.String(), "Recovery codes")
}

// TestSecurityConfirmationErrorsKeepTheirForms covers failed enrollment and
// recovery-code replacement, including field hints and unchanged credentials.
func TestSecurityConfirmationErrorsKeepTheirForms(t *testing.T) {
	t.Parallel()

	t.Run("enrollment code", func(t *testing.T) {
		a := testAuth(t)
		h := newWebHandlerWithAuth(t, testStore(t), a)
		cookies := signIn(t, h)
		require.Equal(t, http.StatusSeeOther, requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/enroll", "").Code)

		for _, code := range []string{"short", "abcdef"} {
			rec := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/confirm", url.Values{"code": {code}}.Encode())
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			assert.Contains(t, rec.Body.String(), "6-digit code your authenticator app shows now")
			assert.Contains(t, rec.Body.String(), `aria-describedby="confirm-error"`)
			assert.Contains(t, rec.Body.String(), "totp-qr.png")
		}
	})

	t.Run("recovery code password", func(t *testing.T) {
		a := testAuth(t)
		h := newWebHandlerWithAuth(t, testStore(t), a)
		secret, _ := enrollTOTP(t, a, testUsername)
		cookies := signInWithCode(t, h, secret)

		for _, password := range []string{"short", "not-the-password"} {
			form := url.Values{"username": {testUsername}, "password": {password}}.Encode()
			rec := requestAs(t, h, cookies, http.MethodPost, "/settings/security/recovery-codes/regenerate", form)
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			assert.Contains(t, rec.Body.String(), `aria-labelledby="regenerate-dialog-title" open`)
			assert.Contains(t, rec.Body.String(), `aria-describedby="regenerate-error"`)
			assert.Contains(t, rec.Body.String(), "10 recovery codes remaining")
			assert.NotContains(t, rec.Body.String(), `value="`+password+`"`)
		}
	})
}

func TestSecurityDisableRequiresPassword(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	secret, _ := enrollTOTP(t, a, testUsername)
	cookies := signInWithCode(t, h, secret)

	wrong := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/disable",
		url.Values{"password": {"not-the-password"}}.Encode())
	require.Equal(t, http.StatusUnprocessableEntity, wrong.Code)
	assert.Contains(t, wrong.Body.String(), "That password did not match.")
	assert.Contains(t, wrong.Body.String(), `aria-labelledby="disable-dialog-title" open`)
	assert.Contains(t, wrong.Body.String(), `aria-describedby="disable-error"`)
	assert.NotContains(t, wrong.Body.String(), "not-the-password")

	// The dialog names the account for password managers, and the form
	// posts it along with the password.
	assert.Contains(t, wrong.Body.String(),
		`<input type="text" id="disable-username" name="username" autocomplete="username"`)

	right := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/disable",
		url.Values{"username": {testUsername}, "password": {testPassword}}.Encode())
	require.Equal(t, http.StatusSeeOther, right.Code)

	// 2FA is off again: a fresh sign-in goes straight through.
	fresh := loginWith(t, h, testUsername, testPassword)
	require.Equal(t, http.StatusFound, fresh.Code)
	assert.Equal(t, "/", fresh.Header().Get("Location"))
}

// TestSecurityQRRouteOnlyServesAPendingEnrollment covers the QR image route
// having nothing to show without an unconfirmed secret: before enrollment
// starts, and again once it has been confirmed.
func TestSecurityQRRouteOnlyServesAPendingEnrollment(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	cookies := signIn(t, h)

	before := requestAs(t, h, cookies, http.MethodGet, "/settings/security/totp-qr.png", "")
	assert.Equal(t, http.StatusNotFound, before.Code)

	requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/enroll", "")

	during := requestAs(t, h, cookies, http.MethodGet, "/settings/security/totp-qr.png", "")
	require.Equal(t, http.StatusOK, during.Code)
	assert.Equal(t, "image/png", during.Header().Get("Content-Type"))
	assert.True(t, strings.HasPrefix(during.Body.String(), "\x89PNG"))
}

// TestSecurityKeepsTheSecretOnceTOTPIsOn covers a signed-in session reaching
// enrollment while 2FA is already on, as a stale tab resubmitting would. The
// secret stays out of reach and unchanged, and the recovery codes stay valid.
func TestSecurityKeepsTheSecretOnceTOTPIsOn(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	secret, recoveryCodes := enrollTOTP(t, a, testUsername)
	cookies := signInWithCode(t, h, secret)

	enroll := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/enroll", "")
	require.Equal(t, http.StatusSeeOther, enroll.Code)
	assert.Equal(t, "/settings/security", enroll.Header().Get("Location"))

	qr := requestAs(t, h, cookies, http.MethodGet, "/settings/security/totp-qr.png", "")
	assert.Equal(t, http.StatusNotFound, qr.Code)

	page := requestAs(t, h, cookies, http.MethodGet, "/settings/security", "")
	require.Equal(t, http.StatusOK, page.Code)
	assert.NotContains(t, page.Body.String(), secret)
	assert.Contains(t, page.Body.String(),
		"To use a different authenticator, turn two-factor authentication off and set it up again.")

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	confirm := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/confirm",
		url.Values{"code": {code}}.Encode())
	require.Equal(t, http.StatusSeeOther, confirm.Code)
	assert.Equal(t, "/settings/security", confirm.Header().Get("Location"))

	// No new codes were minted, so the page shows none, and an original
	// code still completes a sign-in.
	after := requestAs(t, h, cookies, http.MethodGet, "/settings/security", "")
	require.Equal(t, http.StatusOK, after.Code)
	assert.NotContains(t, after.Body.String(), "Recovery codes")

	pending := loginWith(t, h, testUsername, testPassword).Result().Cookies()
	redeem := requestAs(t, h, pending, http.MethodPost, "/login/totp",
		url.Values{"code": {recoveryCodes[0]}}.Encode())
	assert.Equal(t, http.StatusFound, redeem.Code)

	signInWithCode(t, h, secret)
}
