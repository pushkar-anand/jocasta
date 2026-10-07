package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// totpIssuer labels every enrolled account in an authenticator app, so a
// visitor with more than one jocasta instance can tell their entries apart.
const totpIssuer = "jocasta"

// totpBurst and totpRefill set each account's allowance of second-factor
// codes: totpBurst at once, then one more every totpRefill. A 6-digit TOTP
// code is only ~1e6 possibilities across a ~30-90s validity window, far
// weaker than any password is allowed to be, so guessing one has to stay
// slow. The allowance belongs to the account, so a fresh sign-in with the
// password starts with what the last one left.
const (
	totpBurst  = 5
	totpRefill = 3 * time.Minute
)

// totpPeriod, totpDigits and totpAlgorithm are how codes are made. They are
// RFC 6238's defaults, and the only settings some authenticator apps support:
// those apps ignore any others a QR code asks for. totpSkew accepts a code up
// to that many steps either side of the current one, to allow for clock drift.
const (
	totpPeriod    = 30 * time.Second
	totpDigits    = otp.DigitsSix
	totpAlgorithm = otp.AlgorithmSHA1
	totpSkew      = 1
)

// totpSteps records the last TOTP step accepted for each account, so a code
// that has signed in once is refused for the rest of its validity window. It
// lives in memory, so a restart forgets it and a code used just before one
// works once more until its window closes. A totpSteps is safe for concurrent
// use, and its zero value is ready to use.
type totpSteps struct {
	mu   sync.Mutex
	last map[int64]uint64
}

// accept reports whether code is userID's code for the step at now or one
// either side of it, and from a later step than any accepted before. A code
// it accepts is recorded, so the same code is refused next time.
func (s *totpSteps) accept(userID int64, secret, code string, now time.Time) bool {
	current := uint64(now.Unix() / int64(totpPeriod/time.Second)) //nolint:gosec // a clock set before 1970 matches no authenticator anyway.

	// Every candidate step is checked, with no early return, so the time
	// taken does not say which step matched. Later steps overwrite earlier
	// ones, leaving the newest match.
	var (
		matched bool
		step    uint64
	)

	for c := current - totpSkew; c <= current+totpSkew; c++ {
		ok, err := hotp.ValidateCustom(code, c, secret, hotp.ValidateOpts{
			Digits:    totpDigits,
			Algorithm: totpAlgorithm,
		})
		if err == nil && ok {
			matched, step = true, c
		}
	}

	if !matched {
		return false
	}

	// The check against the last step and the record of this one happen
	// under one lock, so two requests carrying the same code cannot both
	// pass.
	s.mu.Lock()
	defer s.mu.Unlock()

	if last, seen := s.last[userID]; seen && step <= last {
		return false
	}

	if s.last == nil {
		s.last = make(map[int64]uint64)
	}

	s.last[userID] = step

	return true
}

// recoveryCodeCount is how many single-use codes one enrollment or
// regeneration mints.
const recoveryCodeCount = 10

// totpManager is what Auth needs from the store to enroll, confirm, and
// check two-factor authentication, and to issue and redeem the recovery
// codes that back it.
type totpManager interface {
	SetUserTOTPSecret(ctx context.Context, arg models.SetUserTOTPSecretParams) (int64, error)
	EnableUserTOTP(ctx context.Context, arg models.EnableUserTOTPParams) (int64, error)
	DisableUserTOTP(ctx context.Context, id int64) error

	CreateRecoveryCode(ctx context.Context, arg models.CreateRecoveryCodeParams) (*models.UserRecoveryCode, error)
	DeleteRecoveryCodesByUser(ctx context.Context, userID int64) error
	RedeemRecoveryCode(ctx context.Context, arg models.RedeemRecoveryCodeParams) (*models.UserRecoveryCode, error)
	CountUnusedRecoveryCodesByUser(ctx context.Context, userID int64) (int64, error)
}

// StartTOTPEnrollment generates a new, unconfirmed secret for userID and
// stores it, overwriting any secret an earlier, abandoned attempt left.
// Once ConfirmTOTPEnrollment has turned 2FA on, it returns ErrTOTPEnabled and
// leaves the secret as it is.
func (a *Auth) StartTOTPEnrollment(ctx context.Context, userID int64, username string) (*otp.Key, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: username,
		Period:      uint(totpPeriod / time.Second),
		Digits:      totpDigits,
		Algorithm:   totpAlgorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("generate totp key: %w", err)
	}

	n, err := a.store.SetUserTOTPSecret(ctx, models.SetUserTOTPSecretParams{
		TOTPSecret: sql.NullString{String: key.Secret(), Valid: true},
		ID:         userID,
	})
	if err != nil {
		return nil, fmt.Errorf("store totp secret: %w", err)
	}

	if n == 0 {
		return nil, ErrTOTPEnabled
	}

	return key, nil
}

// PendingTOTPKey rebuilds the otp.Key for userID's stored-but-unconfirmed
// secret, so the QR route can render it without keeping the *otp.Key itself
// around between requests. It returns ErrNoTOTPEnrollment when there is no
// secret or 2FA is already on.
func (a *Auth) PendingTOTPKey(ctx context.Context, userID int64) (*otp.Key, error) {
	user, err := a.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user %d: %w", userID, err)
	}

	if user.TOTPEnabled || !user.TOTPSecret.Valid {
		return nil, ErrNoTOTPEnrollment
	}

	uri := fmt.Sprintf(
		"otpauth://totp/%s:%s?secret=%s&issuer=%s",
		url.QueryEscape(totpIssuer), url.QueryEscape(user.Username),
		user.TOTPSecret.String, url.QueryEscape(totpIssuer),
	)

	return otp.NewKeyFromURL(uri)
}

// ConfirmTOTPEnrollment validates code against the secret StartTOTPEnrollment
// stored, and only on success flips totp_enabled and mints a fresh set of
// recovery codes; an enrollment abandoned before this point leaves 2FA
// off. The returned codes are plaintext, and this is the only call that ever
// produces them; only their hashes are kept. It returns ErrTOTPEnabled, and
// mints nothing, when 2FA is already on.
func (a *Auth) ConfirmTOTPEnrollment(ctx context.Context, userID int64, code string) ([]string, error) {
	user, err := a.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user %d: %w", userID, err)
	}

	if user.TOTPEnabled {
		return nil, ErrTOTPEnabled
	}

	if !user.TOTPSecret.Valid || !totp.Validate(code, user.TOTPSecret.String) {
		return nil, ErrInvalidEnrollmentCode
	}

	n, err := a.store.EnableUserTOTP(ctx, models.EnableUserTOTPParams{
		TOTPConfirmedAt: dbtype.NewNullTime(a.now()),
		ID:              userID,
	})
	if err != nil {
		return nil, fmt.Errorf("enable totp: %w", err)
	}

	// Another confirmation turned 2FA on between the read above and this
	// write, and minted the codes.
	if n == 0 {
		return nil, ErrTOTPEnabled
	}

	return a.regenerateRecoveryCodes(ctx, userID)
}

// DisableTOTP requires the current password, the credential that would still
// sign this account in without the second factor. It removes both the TOTP
// secret and every recovery code, so re-enabling later starts a fresh
// enrollment.
func (a *Auth) DisableTOTP(ctx context.Context, userID int64, password string) error {
	user, err := a.store.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("user %d: %w", userID, err)
	}

	if err := a.hasher.Compare(password, user.PasswordHash); err != nil {
		return ErrInvalidPassword
	}

	if err := a.store.DisableUserTOTP(ctx, userID); err != nil {
		return fmt.Errorf("disable totp: %w", err)
	}

	return a.store.DeleteRecoveryCodesByUser(ctx, userID)
}

// RegenerateRecoveryCodes requires the current password, the same as
// DisableTOTP, since a fresh batch invalidates every code an attacker who
// saw an old one might still be holding.
func (a *Auth) RegenerateRecoveryCodes(ctx context.Context, userID int64, password string) ([]string, error) {
	user, err := a.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user %d: %w", userID, err)
	}

	if err := a.hasher.Compare(password, user.PasswordHash); err != nil {
		return nil, ErrInvalidPassword
	}

	return a.regenerateRecoveryCodes(ctx, userID)
}

// RemainingRecoveryCodes reports how many of userID's recovery codes are
// still unused, for the settings page's "N codes left" line.
func (a *Auth) RemainingRecoveryCodes(ctx context.Context, userID int64) (int64, error) {
	return a.store.CountUnusedRecoveryCodesByUser(ctx, userID)
}

// TOTPStatus reports userID's current 2FA state for the settings page.
// Secret is the pending enrollment's manual-entry key, and empty unless
// enrolling is true.
func (a *Auth) TOTPStatus(ctx context.Context, userID int64) (enabled, enrolling bool, secret string, err error) {
	user, err := a.store.GetUserByID(ctx, userID)
	if err != nil {
		return false, false, "", fmt.Errorf("user %d: %w", userID, err)
	}

	if user.TOTPEnabled || !user.TOTPSecret.Valid {
		return user.TOTPEnabled, false, "", nil
	}

	return false, true, user.TOTPSecret.String, nil
}

// VerifyTOTP completes a sign-in Login left pending on a second factor. code
// is checked as a TOTP value first, then as an unused recovery code: one
// field covers both, since reaching for a backup code means typing it into
// the same box. Every check runs against the session's own pending user, so
// nothing about who this verifies is client-controlled.
//
// Each code, right or wrong, spends one of the account's attempts (see
// totpBurst). With none left, VerifyTOTP checks nothing and returns
// ErrTOTPLocked. A wrong code that spends the last one ends the pending
// sign-in with ErrTOTPLocked too.
func (a *Auth) VerifyTOTP(ctx context.Context, sm *Session, code string) (*models.User, error) {
	d, ok := sm.s.Current(ctx)
	if !ok || d.PendingUserID == 0 {
		return nil, ErrInvalidCredentials
	}

	// Spent before the check, so requests sent together cannot all be
	// checked before any of them is counted.
	allowed, last := a.totpAttempts.spend(d.PendingUserID, a.now())
	if !allowed {
		return nil, a.endPendingSignIn(ctx, sm)
	}

	user, err := a.store.GetUserByID(ctx, d.PendingUserID)
	if err != nil {
		return nil, fmt.Errorf("pending user %d: %w", d.PendingUserID, err)
	}

	matched, err := a.checkTOTPOrRecoveryCode(ctx, user, code)
	if err != nil {
		return nil, err
	}

	if !matched {
		if last {
			return nil, a.endPendingSignIn(ctx, sm)
		}

		return nil, ErrInvalidTOTPCode
	}

	if err := a.establishSession(ctx, sm, user); err != nil {
		return nil, err
	}

	return user, nil
}

// endPendingSignIn discards a pending sign-in that has run out of attempts,
// so trying again starts from the password. It returns ErrTOTPLocked, or the
// error that stopped the session from ending.
func (a *Auth) endPendingSignIn(ctx context.Context, sm *Session) error {
	if err := sm.Logout(ctx); err != nil {
		return err
	}

	return ErrTOTPLocked
}

// checkTOTPOrRecoveryCode reports whether code matches the user's authenticator
// or an unused recovery code. Either is good for one sign-in: a matching TOTP
// code's step is recorded (see totpSteps), and a matching recovery code is
// consumed.
func (a *Auth) checkTOTPOrRecoveryCode(ctx context.Context, user *models.User, code string) (bool, error) {
	if user.TOTPSecret.Valid && a.totpSteps.accept(user.ID, user.TOTPSecret.String, code, a.now()) {
		return true, nil
	}

	_, err := a.store.RedeemRecoveryCode(ctx, models.RedeemRecoveryCodeParams{
		UsedAt:   dbtype.NewNullTime(a.now()),
		UserID:   user.ID,
		CodeHash: hashToken(code),
	})

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("redeem recovery code: %w", err)
	}

	return true, nil
}

// regenerateRecoveryCodes replaces the user's recovery codes and returns their
// plaintext values. Only hashes are stored. A failure after deletion leaves
// the old codes unusable and may leave only part of the replacement stored.
func (a *Auth) regenerateRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	if err := a.store.DeleteRecoveryCodesByUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("clear recovery codes: %w", err)
	}

	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		plaintext, err := generateRecoveryCode()
		if err != nil {
			return nil, fmt.Errorf("generate recovery code: %w", err)
		}

		if _, err := a.store.CreateRecoveryCode(ctx, models.CreateRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hashToken(plaintext),
		}); err != nil {
			return nil, fmt.Errorf("store recovery code: %w", err)
		}

		codes[i] = plaintext
	}

	return codes, nil
}

// generateRecoveryCode returns one single-use code, grouped the way a card
// number is, because it is copied by hand as often as pasted.
func generateRecoveryCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)

	return fmt.Sprintf("%s-%s-%s-%s", s[0:4], s[4:8], s[8:12], s[12:16]), nil
}
