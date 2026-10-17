package auth

import "errors"

// ErrInvalidCredentials is returned for an unknown username and for a wrong
// password. So a caller cannot tell which from the error.
var ErrInvalidCredentials = errors.New("invalid user or credentials")

// ErrLoginLocked is returned when an account used up its password attempts,
// whether the password matched or not. Unknown usernames share one limit, so
// they lock in the same way. After some time, another attempt is allowed.
var ErrLoginLocked = errors.New("too many sign-in attempts")

// ErrInvalidToken is returned for an API token that matches no row. The token
// was never made, was mistyped, or was revoked.
var ErrInvalidToken = errors.New("invalid token")

// ErrUsernameTaken is returned when the username of a new account is the same
// as an existing username.
var ErrUsernameTaken = errors.New("username already taken")

// ErrSetupComplete is returned when a request reaches the one-time setup of
// the first account, but an account already exists.
var ErrSetupComplete = errors.New("setup already completed")

// ErrForbidden is returned when a signed-in user without the admin role
// reaches a route that only an admin can use.
var ErrForbidden = errors.New("forbidden")

// ErrInvalidTOTPCode is returned when the code of a pending sign-in matches no
// valid TOTP value and no unused recovery code, and attempts remain.
var ErrInvalidTOTPCode = errors.New("invalid authentication code")

// ErrTOTPLocked is returned when an account used up its second-factor
// attempts. The pending sign-in has then ended. After some time, another
// attempt is allowed, after the password again.
var ErrTOTPLocked = errors.New("too many authentication codes")

// ErrInvalidEnrollmentCode is the equivalent of ErrInvalidTOTPCode for
// ConfirmTOTPEnrollment. It is a separate value, because the pipeline maps a
// status and template for each error. This failure must go back to the
// settings page.
var ErrInvalidEnrollmentCode = errors.New("invalid authentication code")

// ErrInvalidPassword is returned by DisableTOTP and RegenerateRecoveryCodes
// when the password for the action does not match. It is separate from
// ErrInvalidCredentials, which goes to the sign-in page. Only a signed-in
// visitor reaches these two actions, on the settings page. So the error goes
// back there.
var ErrInvalidPassword = errors.New("incorrect password")

// ErrNoTOTPEnrollment is returned when there is no unconfirmed secret.
// Enrollment never started, or it was already confirmed or cancelled.
var ErrNoTOTPEnrollment = errors.New("no totp enrollment in progress")

// ErrTOTPEnabled is returned when enrollment starts, or is confirmed, for an
// account whose 2FA is already on. To change the authenticator, first turn
// 2FA off. This asks for the password.
var ErrTOTPEnabled = errors.New("totp already enabled")

// ErrSecondAdmin is returned when an account would become admin while an admin
// already exists. An instance has one admin, made at setup. Every account
// added after it is a viewer or an editor.
var ErrSecondAdmin = errors.New("an admin already exists")
