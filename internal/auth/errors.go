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
