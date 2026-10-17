package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// totpIssuer labels every enrolled account in an authenticator app. So a
// visitor with more than one jocasta instance can tell the entries apart.
const totpIssuer = "jocasta"

// totpBurst and totpRefill set the second-factor codes of each account: first
// totpBurst at once, then one more every totpRefill. A 6-digit TOTP code has
// only about 1e6 values, in a validity window of about 30-90s. This is much
// weaker than any allowed password. So guesses must stay slow. The limit
// belongs to the account. So a new sign-in with the password starts with what
// the last sign-in left.
const (
	totpBurst  = 5
	totpRefill = 3 * time.Minute
)

// totpPeriod, totpDigits and totpAlgorithm set how codes are made. They are the
// defaults of RFC 6238. Some authenticator apps support only these settings,
// and ignore other values in a QR code. totpSkew accepts a code up to that
// number of steps before or after the current step, for clock drift.
const (
	totpPeriod    = 30 * time.Second
	totpDigits    = otp.DigitsSix
	totpAlgorithm = otp.AlgorithmSHA1
	totpSkew      = 1
)

// totpSteps records the last accepted TOTP step of each account. So a code that
// signed in once is refused for the rest of its validity window. It is in
// memory, so a restart forgets it. A code used just before a restart then
// works one more time, until its window closes. A totpSteps is safe for
// concurrent use. Its zero value is ready to use.
type totpSteps struct {
	mu   sync.Mutex
	last map[int64]uint64
}

// accept reports whether code is the code of userID for the step at now, or one
// step before or after. The step must be later than every step accepted
// before. accept records a code that it accepts. So the same code fails the
// next time.
func (s *totpSteps) accept(userID int64, secret, code string, now time.Time) bool {
	current := uint64(now.Unix() / int64(totpPeriod/time.Second)) //nolint:gosec // a clock set before 1970 matches no authenticator anyway.

	// Check every candidate step, with no early return. So the time does not
	// tell which step matched. Later steps overwrite earlier steps. So the
	// newest match stays.
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

	// The check against the last step and the record of this step happen
	// under one lock. So two requests with the same code cannot both pass.
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
// regeneration makes.
const recoveryCodeCount = 10

// totpManager is what Auth needs from the store for two-factor
// authentication. It enrolls, confirms and checks it. It also makes and uses
// the recovery codes for it.
type totpManager interface {
	SetUserTOTPSecret(ctx context.Context, arg models.SetUserTOTPSecretParams) (int64, error)
	EnableUserTOTP(ctx context.Context, arg models.EnableUserTOTPParams) (int64, error)
	DisableUserTOTP(ctx context.Context, id int64) error

	CreateRecoveryCode(ctx context.Context, arg models.CreateRecoveryCodeParams) (*models.UserRecoveryCode, error)
	DeleteRecoveryCodesByUser(ctx context.Context, userID int64) error
	RedeemRecoveryCode(ctx context.Context, arg models.RedeemRecoveryCodeParams) (*models.UserRecoveryCode, error)
	CountUnusedRecoveryCodesByUser(ctx context.Context, userID int64) (int64, error)
}

// StartTOTPEnrollment makes a new, unconfirmed secret for userID and stores it.
// It overwrites a secret from an earlier attempt that did not finish. After
// ConfirmTOTPEnrollment turned 2FA on, it returns ErrTOTPEnabled, and does not
// change the secret.
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

// PendingTOTPKey builds the otp.Key again for the stored, unconfirmed secret of
// userID. So the QR route can render it, and does not keep the *otp.Key
// between requests. It returns ErrNoTOTPEnrollment when there is no secret, or
// 2FA is already on.
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

// ConfirmTOTPEnrollment validates code against the secret that
// StartTOTPEnrollment stored. Only on success, it sets totp_enabled and makes
// a new set of recovery codes. If an enrollment stops before this point, 2FA
// stays off. The returned codes are plaintext. Only this call makes them, and
// only their hashes are kept. When 2FA is already on, it returns
// ErrTOTPEnabled, and makes nothing.
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

	// 2FA goes on in the same transaction that stores the codes. So it is
	// never on without them.
	var codes []string

	err = a.inTx(ctx, func(q store) error {
		n, err := q.EnableUserTOTP(ctx, models.EnableUserTOTPParams{
			TOTPConfirmedAt: dbtype.NewNullTime(a.now()),
			ID:              userID,
		})
		if err != nil {
			return fmt.Errorf("enable totp: %w", err)
		}

		// A different confirmation turned 2FA on between the read above and
		// this write, and made the codes.
		if n == 0 {
			return ErrTOTPEnabled
		}

		codes, err = a.regenerateRecoveryCodes(ctx, q, userID)

		return err
	})
	if err != nil {
		return nil, err
	}

	return codes, nil
}

// DisableTOTP needs the current password. This credential would still sign
// this account in without the second factor. It removes the TOTP secret and
// every recovery code. So to turn 2FA on again later starts a new
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

// RegenerateRecoveryCodes needs the current password, as DisableTOTP does. A
// new batch makes every old code invalid. An attacker who saw an old code
// could still have it.
func (a *Auth) RegenerateRecoveryCodes(ctx context.Context, userID int64, password string) ([]string, error) {
	user, err := a.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user %d: %w", userID, err)
	}

	if err := a.hasher.Compare(password, user.PasswordHash); err != nil {
		return nil, ErrInvalidPassword
	}

	var codes []string

	err = a.inTx(ctx, func(q store) error {
		codes, err = a.regenerateRecoveryCodes(ctx, q, userID)
		return err
	})
	if err != nil {
		return nil, err
	}

	return codes, nil
}

// RemainingRecoveryCodes reports how many recovery codes of userID are still
// unused, for the "N codes left" line of the settings page.
func (a *Auth) RemainingRecoveryCodes(ctx context.Context, userID int64) (int64, error) {
	return a.store.CountUnusedRecoveryCodesByUser(ctx, userID)
}

// TOTPStatus reports the current 2FA state of userID for the settings page.
// Secret is the manual-entry key of the pending enrollment. It is empty unless
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

// VerifyTOTP completes a sign-in that Login left to wait for a second factor.
// It checks code first as a TOTP value, then as an unused recovery code. One
// field covers both, because a person types a backup code into the same box.
// Every check runs against the pending user of the session. So the client
// cannot choose the user that this call verifies.
//
// Each code, right or wrong, uses one attempt of the account (see totpBurst).
// With none left, VerifyTOTP checks nothing and returns ErrTOTPLocked. A wrong
// code that uses the last attempt also ends the pending sign-in with
// ErrTOTPLocked. Input that cannot be either kind of code cannot match (see
// normaliseCode). So VerifyTOTP returns ErrInvalidTOTPCode, and uses no
// attempt.
func (a *Auth) VerifyTOTP(ctx context.Context, sm *Session, code string) (*models.User, error) {
	d, ok := sm.s.Current(ctx)
	if !ok || d.PendingUserID == 0 {
		return nil, ErrInvalidCredentials
	}

	code, ok = normaliseCode(code)
	if !ok {
		return nil, ErrInvalidTOTPCode
	}

	// Use the attempt before the check. So requests sent together cannot all
	// be checked before one of them is counted.
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

// endPendingSignIn discards a pending sign-in that has no attempts left. So the
// next try starts from the password. It returns ErrTOTPLocked, or the error
// that stopped the end of the session.
func (a *Auth) endPendingSignIn(ctx context.Context, sm *Session) error {
	if err := sm.Logout(ctx); err != nil {
		return err
	}

	return ErrTOTPLocked
}

// checkTOTPOrRecoveryCode reports whether code matches the authenticator of the
// user, or an unused recovery code. Each works for one sign-in. The step of a
// matching TOTP code is recorded (see totpSteps). A matching recovery code is
// used up.
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

// normaliseCode returns code in the form for the check. It also reports
// whether code has the shape of either kind of code. It removes spaces and
// dashes, and makes letters uppercase. Six digits are a TOTP value. Sixteen
// base32 characters are a recovery code. normaliseCode returns a recovery code
// with a dash every four characters, as generateRecoveryCode writes it.
func normaliseCode(code string) (string, bool) {
	code = strings.ToUpper(strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}

		return r
	}, code))

	switch {
	case len(code) == 6 && strings.Trim(code, "0123456789") == "":
		return code, true
	case len(code) == 16 && strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") == "":
		return code[0:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:16], true
	}

	return "", false
}

// regenerateRecoveryCodes replaces the recovery codes of the user through q. It
// returns their plaintext values. Only hashes are stored. The caller binds q
// to a transaction. So the old codes go only if every new code is stored.
func (a *Auth) regenerateRecoveryCodes(ctx context.Context, q store, userID int64) ([]string, error) {
	if err := q.DeleteRecoveryCodesByUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("clear recovery codes: %w", err)
	}

	codes := make([]string, recoveryCodeCount)
	for i := range codes {
		plaintext, err := generateRecoveryCode()
		if err != nil {
			return nil, fmt.Errorf("generate recovery code: %w", err)
		}

		if _, err := q.CreateRecoveryCode(ctx, models.CreateRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hashToken(plaintext),
		}); err != nil {
			return nil, fmt.Errorf("store recovery code: %w", err)
		}

		codes[i] = plaintext
	}

	return codes, nil
}

// generateRecoveryCode returns one single-use code, grouped as a card number
// is. People copy it by hand as often as they paste it.
func generateRecoveryCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)

	return fmt.Sprintf("%s-%s-%s-%s", s[0:4], s[4:8], s[8:12], s[12:16]), nil
}
