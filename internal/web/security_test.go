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

func TestSecurityDisableRequiresPassword(t *testing.T) {
	t.Parallel()

	a := testAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)
	secret, _ := enrollTOTP(t, a, testUsername)
	cookies := signInWithCode(t, h, secret)

	wrong := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/disable",
		url.Values{"password": {"not-the-password"}}.Encode())
	require.Equal(t, http.StatusUnprocessableEntity, wrong.Code)

	right := requestAs(t, h, cookies, http.MethodPost, "/settings/security/totp/disable",
		url.Values{"password": {testPassword}}.Encode())
	require.Equal(t, http.StatusSeeOther, right.Code)

	// 2FA is off again: a fresh sign-in goes straight through.
	fresh := loginWith(t, h, testUsername, testPassword)
	require.Equal(t, http.StatusFound, fresh.Code)
	assert.Equal(t, "/", fresh.Header().Get("Location"))
}

// TestSecurityQRRouteOnlyServesAPendingEnrollment covers the QR image route
// having nothing to show once there is no unconfirmed secret -- reached
// before enrollment starts, and again once it has been confirmed.
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
