package auth

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSessionMiddleware wraps a handler that reports whether it was reached,
// so a test can tell a request the gate turned away from one that passed
// through.
func testSessionMiddleware(t *testing.T, a *Auth, always, signIn []*regexp.Regexp) (*Session, http.Handler, *bool) {
	t.Helper()

	sm := NewSession()

	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	})

	return sm, NewSessionMiddleware(sm, a, always, signIn)(next), &reached
}

var loginBypass = []*regexp.Regexp{regexp.MustCompile(`^/login$`)}

func TestSessionMiddlewareRedirectsToSetupWhileNoAccountExists(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	_, h, reached := testSessionMiddleware(t, a, nil, loginBypass)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, setupPath, rec.Header().Get("Location"))
	assert.False(t, *reached)
}

func TestSessionMiddlewareAllowsSetupWhileNoAccountExists(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	_, h, reached := testSessionMiddleware(t, a, nil, loginBypass)

	req := httptest.NewRequest(http.MethodGet, setupPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.True(t, *reached)
}

func TestSessionMiddlewareRedirectsSetupToLoginOnceAnAccountExists(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{"ada": {ID: 1, Username: "ada"}})
	_, h, reached := testSessionMiddleware(t, a, nil, loginBypass)

	req := httptest.NewRequest(http.MethodGet, setupPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
	assert.False(t, *reached)
}

func TestSessionMiddlewareBypassesAlwaysPathsRegardlessOfSetup(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	always := []*regexp.Regexp{regexp.MustCompile(`^/static/.*$`)}
	_, h, reached := testSessionMiddleware(t, a, always, loginBypass)

	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.True(t, *reached, "a static asset is served even while setup is required")
}

func TestSessionMiddlewareRedirectsToLoginWithoutASession(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{"ada": {ID: 1, Username: "ada"}})
	sm, h, reached := testSessionMiddleware(t, a, nil, loginBypass)

	ctx, err := sm.Load(t.Context(), "")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
	assert.False(t, *reached)
}

func TestSessionMiddlewareAllowsASignedInVisitor(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{"ada": {ID: 1, Username: "ada"}})
	sm, h, reached := testSessionMiddleware(t, a, nil, loginBypass)

	ctx, err := sm.Load(t.Context(), "")
	require.NoError(t, err)
	sm.sm.Put(ctx, sessionUserKey, int64(1))

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.True(t, *reached)
}
