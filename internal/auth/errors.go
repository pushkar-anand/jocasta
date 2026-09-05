package auth

import "errors"

// ErrInvalidCredentials is returned for a username that has no match and for
// one whose password doesn't match it alike, so a caller can't tell which by
// branching on the error.
var ErrInvalidCredentials = errors.New("invalid user or credentials")

// ErrInvalidToken is returned for an API token that answers for no row,
// whether it was never issued, was mistyped, or has since been revoked.
var ErrInvalidToken = errors.New("invalid token")
