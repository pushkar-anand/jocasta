package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
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

	// Signed in already, with no separate sign-in step.
	cookies := rec.Result().Cookies()
	require.NotEmpty(t, cookies)

	overview := requestAs(t, h, cookies, http.MethodGet, "/", "")
	require.Equal(t, http.StatusOK, overview.Code)
}

// A form that does not validate is the visitor's to fix, so it comes back as
// the request package's own 422 rendered on a page.
func TestSetupFormRejectsTooShortInput(t *testing.T) {
	t.Parallel()

	a := unseededAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)

	form := url.Values{"username": {"ab"}, "password": {"short"}}
	rec := requestAs(t, h, nil, http.MethodPost, "/setup", form.Encode())

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "Request could not be processed")
	assert.NotContains(t, rec.Body.String(), "Internal Server Error")
}

// TestSetupFormCreatesOneAdminUnderParallelPosts covers setup submitted from
// several places at once, each with its own username. Exactly one becomes the
// admin; the others are told setup is done.
func TestSetupFormCreatesOneAdminUnderParallelPosts(t *testing.T) {
	t.Parallel()

	a := unseededAuth(t)
	h := newWebHandlerWithAuth(t, testStore(t), a)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		statuses []int
	)

	for i := range 10 {
		wg.Go(func() {
			form := url.Values{"username": {fmt.Sprintf("admin-%d", i)}, "password": {"correct-password-1"}}
			rec := requestAs(t, h, nil, http.MethodPost, "/setup", form.Encode())

			mu.Lock()
			defer mu.Unlock()

			statuses = append(statuses, rec.Code)
		})
	}

	wg.Wait()

	slices.Sort(statuses)

	want := append([]int{http.StatusFound}, slices.Repeat([]int{http.StatusConflict}, 9)...)
	assert.Equal(t, want, statuses)

	users, err := a.ListUsers(t.Context())
	require.NoError(t, err)
	assert.Len(t, users, 1)
}

func TestSetupFormRefusesOnceAnAccountExists(t *testing.T) {
	t.Parallel()

	h := empty(t) // testAuth seeds one account already.

	form := url.Values{"username": {"someone-else"}, "password": {"another-password-1"}}
	rec := requestAs(t, h, nil, http.MethodPost, "/setup", form.Encode())

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "admin account already exists")
}
