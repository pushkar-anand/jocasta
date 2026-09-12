package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// totpIssuer labels every enrolled account in an authenticator app, so a
// visitor with more than one jocasta instance can tell their entries apart.
const totpIssuer = "jocasta"

// totpMaxAttempts caps consecutive wrong codes against one pending sign-in.
// A 6-digit TOTP code is only ~1e6 possibilities across a ~30-90s validity
// window, materially weaker than a password is ever allowed to be -- so this
// pending session earns the one rate-limit-shaped check in a codebase that
// otherwise has none. It bounds guesswork against the second factor only:
// the account itself stays exactly as reachable as before, by trying the
// password again from a fresh sign-in.
const totpMaxAttempts = 5

// recoveryCodeCount is how many single-use codes one enrollment or
// regeneration mints.
const recoveryCodeCount = 10

// totpManager is what Auth needs from the store to enroll, confirm, and
// check two-factor authentication, and to issue and redeem the recovery
// codes that back it.
type totpManager interface {
	SetUserTOTPSecret(ctx context.Context, arg models.SetUserTOTPSecretParams) error
	EnableUserTOTP(ctx context.Context, arg models.EnableUserTOTPParams) error
	DisableUserTOTP(ctx context.Context, id int64) error

	CreateRecoveryCode(ctx context.Context, arg models.CreateRecoveryCodeParams) (*models.UserRecoveryCode, error)
	DeleteRecoveryCodesByUser(ctx context.Context, userID int64) error
	RedeemRecoveryCode(ctx context.Context, arg models.RedeemRecoveryCodeParams) (*models.UserRecoveryCode, error)
	CountUnusedRecoveryCodesByUser(ctx context.Context, userID int64) (int64, error)
}

// StartTOTPEnrollment generates a new, unconfirmed secret for userID and
// stores it, overwriting any secret an earlier, abandoned attempt left --
// restarting enrollment is always safe up until ConfirmTOTPEnrollment flips
// totp_enabled.
func (a *Auth) StartTOTPEnrollment(ctx context.Context, userID int64, username string) (*otp.Key, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: username})
	if err != nil {
		return nil, fmt.Errorf("generate totp key: %w", err)
	}

	if err := a.totp.SetUserTOTPSecret(ctx, models.SetUserTOTPSecretParams{
		TOTPSecret: sql.NullString{String: key.Secret(), Valid: true},
		ID:         userID,
	}); err != nil {
		return nil, fmt.Errorf("store totp secret: %w", err)
	}

	return key, nil
}

// PendingTOTPKey rebuilds the otp.Key for userID's stored-but-unconfirmed
// secret, so the QR route can render it without keeping the *otp.Key itself
// around between requests.
func (a *Auth) PendingTOTPKey(ctx context.Context, userID int64) (*otp.Key, error) {
	user, err := a.q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user %d: %w", userID, err)
	}

	if !user.TOTPSecret.Valid {
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
// recovery codes -- an enrollment abandoned before this point leaves 2FA
// off. The returned codes are plaintext, and this is the only call that ever
// produces them; only their hashes are kept.
func (a *Auth) ConfirmTOTPEnrollment(ctx context.Context, userID int64, code string) ([]string, error) {
	user, err := a.q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user %d: %w", userID, err)
	}

	if !user.TOTPSecret.Valid || !totp.Validate(code, user.TOTPSecret.String) {
		return nil, ErrInvalidEnrollmentCode
	}

	if err := a.totp.EnableUserTOTP(ctx, models.EnableUserTOTPParams{
		TOTPConfirmedAt: dbtype.NewNullTime(a.now()),
		ID:              userID,
	}); err != nil {
		return nil, fmt.Errorf("enable totp: %w", err)
	}

	return a.regenerateRecoveryCodes(ctx, userID)
}

// DisableTOTP requires the current password -- proof of the same credential
// that would still sign this account in without the second factor -- and
// removes both the TOTP secret and every recovery code, so re-enabling later
// is an enrollment from scratch, not a reactivation of stale state.
func (a *Auth) DisableTOTP(ctx context.Context, userID int64, password string) error {
	user, err := a.q.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("user %d: %w", userID, err)
	}

	if err := a.hasher.Compare(password, user.PasswordHash); err != nil {
		return ErrInvalidPassword
	}

	if err := a.totp.DisableUserTOTP(ctx, userID); err != nil {
		return fmt.Errorf("disable totp: %w", err)
	}

	return a.totp.DeleteRecoveryCodesByUser(ctx, userID)
}

// RegenerateRecoveryCodes requires the current password, the same as
// DisableTOTP, since a fresh batch invalidates every code an attacker who
// saw an old one might still be holding.
func (a *Auth) RegenerateRecoveryCodes(ctx context.Context, userID int64, password string) ([]string, error) {
	user, err := a.q.GetUserByID(ctx, userID)
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
	return a.totp.CountUnusedRecoveryCodesByUser(ctx, userID)
}

// TOTPStatus reports userID's current 2FA state for the settings page.
// Secret is the pending enrollment's manual-entry key, meaningful only while
// enrolling is true.
func (a *Auth) TOTPStatus(ctx context.Context, userID int64) (enabled, enrolling bool, secret string, err error) {
	user, err := a.q.GetUserByID(ctx, userID)
	if err != nil {
		return false, false, "", fmt.Errorf("user %d: %w", userID, err)
	}

	return user.TOTPEnabled, !user.TOTPEnabled && user.TOTPSecret.Valid, user.TOTPSecret.String, nil
}

// VerifyTOTP completes a sign-in Login left pending on a second factor. code
// is checked as a TOTP value first, then as an unused recovery code -- one
// field covers both, since reaching for a backup code means typing it into
// the same box. Every check runs against the session's own pending user,
// never anything the request claims, so nothing about who this verifies is
// client-controlled.
func (a *Auth) VerifyTOTP(ctx context.Context, sm *Session, code string) (*models.User, error) {
	d, ok := sm.s.Current(ctx)
	if !ok || d.PendingUserID == 0 {
		return nil, ErrInvalidCredentials
	}

	user, err := a.q.GetUserByID(ctx, d.PendingUserID)
	if err != nil {
		return nil, fmt.Errorf("pending user %d: %w", d.PendingUserID, err)
	}

	matched, err := a.checkTOTPOrRecoveryCode(ctx, user, code)
	if err != nil {
		return nil, err
	}

	if !matched {
		attempts := d.PendingAttempts + 1
		if attempts >= totpMaxAttempts {
			if err := sm.Logout(ctx); err != nil {
				return nil, err
			}

			return nil, ErrInvalidCredentials
		}

		sm.s.Update(ctx, func(d *Data) { d.PendingAttempts = attempts })

		return nil, ErrInvalidTOTPCode
	}

	if err := a.establishSession(ctx, sm, user); err != nil {
		return nil, err
	}

	return user, nil
}

func (a *Auth) checkTOTPOrRecoveryCode(ctx context.Context, user *models.User, code string) (bool, error) {
	if user.TOTPSecret.Valid && totp.Validate(code, user.TOTPSecret.String) {
		return true, nil
	}

	_, err := a.totp.RedeemRecoveryCode(ctx, models.RedeemRecoveryCodeParams{
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

func (a *Auth) regenerateRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	if err := a.totp.DeleteRecoveryCodesByUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("clear recovery codes: %w", err)
	}

	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		plaintext, err := generateRecoveryCode()
		if err != nil {
			return nil, fmt.Errorf("generate recovery code: %w", err)
		}

		if _, err := a.totp.CreateRecoveryCode(ctx, models.CreateRecoveryCodeParams{
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
// number is -- it's copied by hand as often as pasted.
func generateRecoveryCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)

	return fmt.Sprintf("%s-%s-%s-%s", s[0:4], s[4:8], s[8:12], s[12:16]), nil
}
