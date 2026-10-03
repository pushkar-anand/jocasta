package auth

import "errors"

// ErrInvalidCredentials is returned both for an unknown username and for a
// wrong password, so a caller cannot tell which by branching on the error.
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

// ErrTOTPLocked is returned when an account has used up its second-factor
// attempts. The pending sign-in has ended by then; another attempt is
// allowed once time has passed, after the password again.
var ErrTOTPLocked = errors.New("too many authentication codes")

// ErrInvalidEnrollmentCode is ErrInvalidTOTPCode's counterpart for
// ConfirmTOTPEnrollment. It is a distinct value because the pipeline maps a
// status and template per error, and this failure has to land back on the
// settings page.
var ErrInvalidEnrollmentCode = errors.New("invalid authentication code")

// ErrInvalidPassword is returned by DisableTOTP and RegenerateRecoveryCodes
// when the password confirming the action does not match. It is distinct from
// ErrInvalidCredentials, which is wired to the standalone sign-in page: both
// actions are reached only by a visitor already signed in, on the settings
// page, so the error lands back there.
var ErrInvalidPassword = errors.New("incorrect password")

// ErrNoTOTPEnrollment is returned when there is no unconfirmed secret to act
// on: enrollment was never started, or was already confirmed or cancelled.
var ErrNoTOTPEnrollment = errors.New("no totp enrollment in progress")

// ErrTOTPEnabled is returned when enrollment is started or confirmed for an
// account whose 2FA is already on. Changing authenticator means turning 2FA
// off first, which asks for the password.
var ErrTOTPEnabled = errors.New("totp already enabled")

// ErrSecondAdmin is returned when an account would be made admin while one
// already is. An instance has one admin, made at setup; every account added
// after it is a viewer or an editor.
var ErrSecondAdmin = errors.New("an admin already exists")
