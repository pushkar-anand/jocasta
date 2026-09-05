package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupPageRenders(t *testing.T) {
	t.Parallel()

	rec := get(t, empty(t), "/setup")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Set up admin account")
}

func TestSetupFormCreatesTheFirstAdminAndSignsIn(t *testing.T) {
	t.Parallel()

	a := unseededAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)

	form := url.Values{"username": {"ada"}, "password": {"correct-password-1"}}
	rec := requestAs(t, h, nil, http.MethodPost, "/setup", form.Encode())

	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))

	// Signed in already, not left to sign in separately with what was just
	// typed.
	cookies := rec.Result().Cookies()
	require.NotEmpty(t, cookies)

	overview := requestAs(t, h, cookies, http.MethodGet, "/", "")
	require.Equal(t, http.StatusOK, overview.Code)
}

// A form that does not validate is the visitor's to fix, so it comes back as
// the request package's own 422 rendered on a page, not the bare 500 an
// unmapped error falls through to.
func TestSetupFormRejectsTooShortInput(t *testing.T) {
	t.Parallel()

	a := unseededAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)

	form := url.Values{"username": {"ab"}, "password": {"short"}}
	rec := requestAs(t, h, nil, http.MethodPost, "/setup", form.Encode())

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Bad request")
	assert.NotContains(t, rec.Body.String(), "Internal Server Error")
}

func TestSetupFormRefusesOnceAnAccountExists(t *testing.T) {
	t.Parallel()

	h := empty(t) // testAuth seeds one account already.

	form := url.Values{"username": {"someone-else"}, "password": {"another-password-1"}}
	rec := requestAs(t, h, nil, http.MethodPost, "/setup", form.Encode())

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "already been completed")
}
