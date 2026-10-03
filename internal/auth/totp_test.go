package auth

import (
	"database/sql"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyTOTPLimitRefills covers the second-factor allowance over time:
// five codes at once, then one more each time the refill interval passes.
// The clock is Auth's own, moved by hand.
func TestVerifyTOTPLimitRefills(t *testing.T) {
	t.Parallel()

	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: "ada"})
	require.NoError(t, err)

	secret := key.Secret()

	a := newTestAuth(t, map[string]*models.User{
		"ada": {
			ID:           42,
			Username:     "ada",
			PasswordHash: hashOf(t, "correct-password"),
			Role:         dbtype.RoleAdmin,
			TOTPSecret:   sql.NullString{String: secret, Valid: true},
			TOTPEnabled:  true,
		},
	})

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	// verify starts a fresh pending sign-in and tries code against it.
	verify := func(code string) error {
		t.Helper()

		sm := NewSession(testLogger())

		ctx, err := sm.Load(t.Context(), "")
		require.NoError(t, err)

		result, err := a.Login(ctx, sm, "ada", "correct-password", false)
		require.NoError(t, err)
		require.True(t, result.TOTPPending)

		_, err = a.VerifyTOTP(ctx, sm, code)

		return err
	}

	for range 4 {
		require.ErrorIs(t, verify("000000"), ErrInvalidTOTPCode)
	}

	require.ErrorIs(t, verify("000000"), ErrTOTPLocked, "the fifth wrong code spends the last attempt")

	valid, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, verify(valid), ErrTOTPLocked, "a correct code waits for the refill too")

	now = now.Add(totpRefill - time.Second)

	require.ErrorIs(t, verify(valid), ErrTOTPLocked)

	now = now.Add(time.Second)

	valid, err = totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	assert.NoError(t, verify(valid), "one attempt is back after the refill interval")
}
