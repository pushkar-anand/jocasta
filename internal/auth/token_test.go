package auth

import (
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndVerifyToken(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	plaintext, token, err := a.CreateToken(t.Context(), 7, "laptop script", dbtype.TokenRead)
	require.NoError(t, err)
	assert.Equal(t, int64(7), token.UserID)
	assert.Equal(t, dbtype.TokenRead, token.Scope)

	// The plaintext is never stored -- what CreateToken returned is the only
	// copy there ever is.
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

	plaintext, token, err := a.CreateToken(t.Context(), 1, "revoked", dbtype.TokenReadWrite)
	require.NoError(t, err)

	require.NoError(t, a.RevokeToken(t.Context(), 1, token.ID))

	_, err = a.VerifyToken(t.Context(), plaintext)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestRevokeTokenIsScopedToItsOwner(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	plaintext, token, err := a.CreateToken(t.Context(), 1, "mine", dbtype.TokenRead)
	require.NoError(t, err)

	// A different user's id names no row of this one's, so nothing happens --
	// and the token still checks out.
	require.NoError(t, a.RevokeToken(t.Context(), 2, token.ID))

	_, err = a.VerifyToken(t.Context(), plaintext)
	assert.NoError(t, err)
}

func TestListTokens(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	_, _, err := a.CreateToken(t.Context(), 1, "a", dbtype.TokenRead)
	require.NoError(t, err)
	_, _, err = a.CreateToken(t.Context(), 1, "b", dbtype.TokenReadWrite)
	require.NoError(t, err)
	_, _, err = a.CreateToken(t.Context(), 2, "someone else's", dbtype.TokenRead)
	require.NoError(t, err)

	tokens, err := a.ListTokens(t.Context(), 1)
	require.NoError(t, err)
	assert.Len(t, tokens, 2)
}

func TestCreateTokenRejectsAnUnknownScope(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	_, _, err := a.CreateToken(t.Context(), 1, "bad scope", dbtype.TokenScope("admin"))
	assert.ErrorIs(t, err, ErrInvalidToken)
}
