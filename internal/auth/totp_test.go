package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/jocasta/internal/db"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifyTOTPLimitRefills covers the second-factor limit over time: five
// codes at once, then one more after each refill interval. The clock is the
// clock of Auth, which the test moves by hand.
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

	// verify starts a new pending sign-in, and tries code against it.
	verify := func(code string) error {
		t.Helper()

		sm := NewSession(testLogger())

		ctx, err := sm.Load(t.Context(), "")
		require.NoError(t, err)

		result, err := a.Login(ctx, sm, testAddr, "ada", "correct-password", false)
		require.NoError(t, err)
		require.True(t, result.TOTPPending)

		_, err = a.VerifyTOTP(ctx, sm, code)

		return err
	}

	for range 4 {
		require.ErrorIs(t, verify("000000"), ErrInvalidTOTPCode)
	}

	require.ErrorIs(t, verify("000000"), ErrTOTPLocked, "the fifth wrong code spends the last attempt")

	valid, err := totp.GenerateCode(secret, now)
	require.NoError(t, err)
	require.ErrorIs(t, verify(valid), ErrTOTPLocked, "a correct code waits for the refill too")

	now = now.Add(totpRefill - time.Second)

	require.ErrorIs(t, verify(valid), ErrTOTPLocked)

	now = now.Add(time.Second)

	valid, err = totp.GenerateCode(secret, now)
	require.NoError(t, err)
	assert.NoError(t, verify(valid), "one attempt is back after the refill interval")
}

// TestVerifyTOTPAcceptsEachStepOnce covers a replayed code. After a code signs
// in, that code and every older code fail. This is true even while they are
// still in the window for the clock drift of an authenticator.
func TestVerifyTOTPAcceptsEachStepOnce(t *testing.T) {
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

	// verify starts a new pending sign-in, and tries the code for at against
	// it.
	verify := func(at time.Time) error {
		t.Helper()

		code, err := totp.GenerateCode(secret, at)
		require.NoError(t, err)

		sm := NewSession(testLogger())

		ctx, err := sm.Load(t.Context(), "")
		require.NoError(t, err)

		result, err := a.Login(ctx, sm, testAddr, "ada", "correct-password", false)
		require.NoError(t, err)
		require.True(t, result.TOTPPending)

		_, err = a.VerifyTOTP(ctx, sm, code)

		return err
	}

	require.NoError(t, verify(now), "the first use of a code signs in")
	require.ErrorIs(t, verify(now), ErrInvalidTOTPCode, "the same code a second time")

	require.NoError(t, verify(now.Add(30*time.Second)), "the next step's code is new")
	assert.ErrorIs(t, verify(now), ErrInvalidTOTPCode, "an older code after a newer one")
}

// newTOTPAuth returns an Auth with one account, ada, with 2FA on. It also
// returns the TOTP secret of the account.
func newTOTPAuth(t *testing.T) (*Auth, string) {
	t.Helper()

	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: "ada"})
	require.NoError(t, err)

	a := newTestAuth(t, map[string]*models.User{
		"ada": {
			ID:           42,
			Username:     "ada",
			PasswordHash: hashOf(t, "correct-password"),
			Role:         dbtype.RoleAdmin,
			TOTPSecret:   sql.NullString{String: key.Secret(), Valid: true},
			TOTPEnabled:  true,
		},
	})

	return a, key.Secret()
}

// verifyPending starts a new pending sign-in for ada, and tries code against
// it.
func verifyPending(t *testing.T, a *Auth, code string) error {
	t.Helper()

	sm := NewSession(testLogger())

	ctx, err := sm.Load(t.Context(), "")
	require.NoError(t, err)

	result, err := a.Login(ctx, sm, testAddr, "ada", "correct-password", false)
	require.NoError(t, err)
	require.True(t, result.TOTPPending)

	_, err = a.VerifyTOTP(ctx, sm, code)

	return err
}

// TestVerifyTOTPRecoveryCodeForms covers a recovery code typed as a person
// reads it from paper: in any case, with or without the dashes, or with spaces
// instead.
func TestVerifyTOTPRecoveryCodeForms(t *testing.T) {
	t.Parallel()

	forms := map[string]func(string) string{
		"lowercase":   strings.ToLower,
		"no dashes":   func(c string) string { return strings.ReplaceAll(c, "-", "") },
		"with spaces": func(c string) string { return strings.ToLower(strings.ReplaceAll(c, "-", " ")) },
	}

	for name, form := range forms {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			a, _ := newTOTPAuth(t)

			codes, err := a.regenerateRecoveryCodes(t.Context(), a.store, 42)
			require.NoError(t, err)

			typed := form(codes[0])

			require.NoError(t, verifyPending(t, a, typed), "%q redeems %q", typed, codes[0])
			assert.ErrorIs(t, verifyPending(t, a, codes[0]), ErrInvalidTOTPCode, "the code is used up")
		})
	}
}

// TestVerifyTOTPWrongShapeSpendsNoAttempt covers input that cannot be either
// kind of code. VerifyTOTP refuses it, and uses no attempt of the account. So
// a valid code still signs in after more such input than the limit.
func TestVerifyTOTPWrongShapeSpendsNoAttempt(t *testing.T) {
	t.Parallel()

	a, secret := newTOTPAuth(t)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	// One pending sign-in takes every try. So the password limit is used
	// once.
	sm := NewSession(testLogger())

	ctx, err := sm.Load(t.Context(), "")
	require.NoError(t, err)

	result, err := a.Login(ctx, sm, testAddr, "ada", "correct-password", false)
	require.NoError(t, err)
	require.True(t, result.TOTPPending)

	for _, code := range []string{"12345", "1234567", "12345a", "ABCD-EFGH-IJKL", "ABCD-EFGH-IJKL-MNO1"} {
		for range totpBurst {
			_, err := a.VerifyTOTP(ctx, sm, code)
			require.ErrorIs(t, err, ErrInvalidTOTPCode, "%q", code)
		}
	}

	valid, err := totp.GenerateCode(secret, now)
	require.NoError(t, err)

	_, err = a.VerifyTOTP(ctx, sm, valid)
	assert.NoError(t, err, "the pending sign-in still has every attempt")
}

// errInsertFailed is the error failingInserts returns.
var errInsertFailed = errors.New("insert failed")

// failingInserts is a store whose failOn-th CreateRecoveryCode fails. So a test
// can stop a batch of codes in the middle.
type failingInserts struct {
	store

	failOn int
	calls  int
}

func (f *failingInserts) CreateRecoveryCode(ctx context.Context, arg models.CreateRecoveryCodeParams) (*models.UserRecoveryCode, error) {
	f.calls++
	if f.calls == f.failOn {
		return nil, errInsertFailed
	}

	return f.store.CreateRecoveryCode(ctx, arg)
}

// newDBAuth returns an Auth over its own migrated database, with one account,
// ada, with 2FA off. It also returns the ID of that account.
func newDBAuth(t *testing.T) (*Auth, int64) {
	t.Helper()

	conn, err := db.New(&db.Config{Path: t.TempDir(), Name: "auth.db"})
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	q := models.New(conn)

	user, err := q.CreateUser(t.Context(), models.CreateUserParams{
		Username:     "ada",
		PasswordHash: hashOf(t, "correct-password"),
		Role:         dbtype.RoleAdmin,
	})
	require.NoError(t, err)

	a, err := New(conn, q, password.NewHasher())
	require.NoError(t, err)

	return a, user.ID
}

// confirmCode starts enrollment for ada, and returns a code that confirms it.
func confirmCode(t *testing.T, a *Auth, userID int64) string {
	t.Helper()

	key, err := a.StartTOTPEnrollment(t.Context(), userID, "ada")
	require.NoError(t, err)

	code, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)

	return code
}

// failFifthInsert makes the transactions of a fail when they store the fifth
// recovery code.
func failFifthInsert(a *Auth) {
	a.txStore = func(tx *sql.Tx) store {
		return &failingInserts{store: models.New(tx), failOn: 5}
	}
}

// TestRegenerateRecoveryCodesFailureKeepsTheOldCodes covers a replacement batch
// that fails in the middle. Nothing of it is kept, and the old codes still
// work.
func TestRegenerateRecoveryCodesFailureKeepsTheOldCodes(t *testing.T) {
	t.Parallel()

	a, userID := newDBAuth(t)

	old, err := a.ConfirmTOTPEnrollment(t.Context(), userID, confirmCode(t, a, userID))
	require.NoError(t, err)

	failFifthInsert(a)

	_, err = a.RegenerateRecoveryCodes(t.Context(), userID, "correct-password")
	require.ErrorIs(t, err, errInsertFailed)

	remaining, err := a.RemainingRecoveryCodes(t.Context(), userID)
	require.NoError(t, err)
	assert.Equal(t, int64(recoveryCodeCount), remaining)

	assert.NoError(t, verifyPending(t, a, old[0]), "an old code still signs in")
}

// TestConfirmTOTPEnrollmentFailureLeavesTOTPOff covers a first batch of
// recovery codes that fails in the middle. 2FA stays off, with no codes
// stored.
func TestConfirmTOTPEnrollmentFailureLeavesTOTPOff(t *testing.T) {
	t.Parallel()

	a, userID := newDBAuth(t)
	code := confirmCode(t, a, userID)

	failFifthInsert(a)

	_, err := a.ConfirmTOTPEnrollment(t.Context(), userID, code)
	require.ErrorIs(t, err, errInsertFailed)

	enabled, enrolling, _, err := a.TOTPStatus(t.Context(), userID)
	require.NoError(t, err)
	assert.False(t, enabled)
	assert.True(t, enrolling, "the enrollment can still be confirmed")

	remaining, err := a.RemainingRecoveryCodes(t.Context(), userID)
	require.NoError(t, err)
	assert.Zero(t, remaining)
}
