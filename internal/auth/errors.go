package auth

import "errors"

// ErrInvalidCredentials is returned for a username that has no match and for
// one whose password doesn't match it alike, so a caller can't tell which by
// branching on the error.
var ErrInvalidCredentials = errors.New("invalid user or credentials")

// ErrInvalidToken is returned for an API token that answers for no row,
// whether it was never issued, was mistyped, or has since been revoked.
var ErrInvalidToken = errors.New("invalid token")

// ErrUsernameTaken is returned when a new account's username collides with an
// existing one.
var ErrUsernameTaken = errors.New("username already taken")

// ErrSetupComplete is returned when the one-time first-account setup is
// reached after an account already exists.
var ErrSetupComplete = errors.New("setup already completed")

// ErrForbidden is returned when a signed-in user without the admin role
// reaches a route only an admin may use.
var ErrForbidden = errors.New("forbidden")

// ErrInvalidTOTPCode is returned when a pending sign-in's code matches
// neither a valid TOTP value nor an unused recovery code, while attempts
// remain.
var ErrInvalidTOTPCode = errors.New("invalid authentication code")

// ErrInvalidEnrollmentCode is ErrInvalidTOTPCode's counterpart for
// ConfirmTOTPEnrollment -- a distinct value because the pipeline maps a
// status, and template, per error, and this failure has to land back on the
// settings page rather than the sign-in one.
var ErrInvalidEnrollmentCode = errors.New("invalid authentication code")

// ErrInvalidPassword is returned by DisableTOTP and RegenerateRecoveryCodes
// when the password confirming the action doesn't match. It is distinct from
// ErrInvalidCredentials, which is wired to the standalone sign-in page --
// these two are reached only by a visitor already signed in, on the settings
// page, so they need to land back there instead.
var ErrInvalidPassword = errors.New("incorrect password")

// ErrNoTOTPEnrollment is returned when there is no unconfirmed secret to act
// on -- enrollment was never started, or was already confirmed or cancelled.
var ErrNoTOTPEnrollment = errors.New("no totp enrollment in progress")
