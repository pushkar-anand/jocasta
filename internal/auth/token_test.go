package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/models"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndVerifyToken(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	plaintext, token, err := a.CreateToken(t.Context(), 7, "laptop script", dbtype.TokenRead, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, int64(7), token.UserID)
	assert.Equal(t, dbtype.TokenRead, token.Scope)

	// The plaintext is never stored: what CreateToken returned is the only
	// copy.
	assert.NotEqual(t, plaintext, token.TokenHash)

	got, err := a.VerifyToken(t.Context(), plaintext)
	require.NoError(t, err)
	assert.Equal(t, token.ID, got.ID)
}

func TestVerifyTokenRejectsWhatIsNotOneOfOurs(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	tests := []string{
		"",
		"whatever-a-caller-sends",
		"Bearer jct_notactuallyvalid",
	}

	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			_, err := a.VerifyToken(t.Context(), in)
			assert.ErrorIs(t, err, ErrInvalidToken)
		})
	}
}

func TestVerifyTokenRejectsARevokedToken(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	plaintext, token, err := a.CreateToken(t.Context(), 1, "revoked", dbtype.TokenReadWrite, time.Time{})
	require.NoError(t, err)

	require.NoError(t, a.RevokeToken(t.Context(), 1, token.ID))

	_, err = a.VerifyToken(t.Context(), plaintext)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestRevokeTokenIsScopedToItsOwner(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	plaintext, token, err := a.CreateToken(t.Context(), 1, "mine", dbtype.TokenRead, time.Time{})
	require.NoError(t, err)

	// A different user's id names no row of this one's, so nothing happens
	// and the token still checks out.
	require.NoError(t, a.RevokeToken(t.Context(), 2, token.ID))

	_, err = a.VerifyToken(t.Context(), plaintext)
	assert.NoError(t, err)
}

func TestListTokens(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	_, _, err := a.CreateToken(t.Context(), 1, "a", dbtype.TokenRead, time.Time{})
	require.NoError(t, err)
	_, _, err = a.CreateToken(t.Context(), 1, "b", dbtype.TokenReadWrite, time.Time{})
	require.NoError(t, err)
	_, _, err = a.CreateToken(t.Context(), 2, "someone else's", dbtype.TokenRead, time.Time{})
	require.NoError(t, err)

	tokens, err := a.ListTokens(t.Context(), 1)
	require.NoError(t, err)
	assert.Len(t, tokens, 2)
}

func TestCreateTokenRejectsAnUnknownScope(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	_, _, err := a.CreateToken(t.Context(), 1, "bad scope", dbtype.TokenScope("admin"), time.Time{})
	assert.ErrorIs(t, err, ErrInvalidToken)
}

// dbAuth builds an Auth over a migrated database holding one account, for a
// test whose subject is a query's own WHERE clause, which the fake store
// only imitates. It returns the account's id.
func dbAuth(t *testing.T) (*Auth, int64) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "auth.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	a, err := New(models.New(conn), password.NewHasher())
	require.NoError(t, err)

	user, err := a.CreateUser(t.Context(), "host-a", "placeholder-password", dbtype.RoleRead)
	require.NoError(t, err)

	return a, user.ID
}

// An expired token is refused the way a revoked one is; a token with no
// expiry, or one that has not reached it, works.
func TestTokenMiddlewareRefusesAnExpiredToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		expiresAt time.Duration // from now; 0 means the token never expires.
		want      int
	}{
		{name: "never expires", want: http.StatusOK},
		{name: "expires later", expiresAt: time.Hour, want: http.StatusOK},
		{name: "expired", expiresAt: -time.Hour, want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, userID := dbAuth(t)

			var expiresAt time.Time
			if tt.expiresAt != 0 {
				expiresAt = time.Now().Add(tt.expiresAt)
			}

			plaintext, token, err := a.CreateToken(t.Context(), userID, "script", dbtype.TokenRead, expiresAt)
			require.NoError(t, err)
			assert.Equal(t, tt.expiresAt != 0, token.ExpiresAt.Valid)

			h, reached := testTokenMiddleware(t, a)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/devices", nil)
			req.Header.Set(authHeaderName, "Bearer "+plaintext)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.want, rec.Code)
			assert.Equal(t, tt.want == http.StatusOK, *reached)
		})
	}
}
