package pihole

import (
	"errors"
	"fmt"
)

// Sentinels a caller acts on differently. The distinction that matters is
// whether the next attempt could go better: a Pi-hole behind a firewall may
// come back, a password that is wrong is wrong until someone edits the config.
var (
	// ErrUnreachable is a Pi-hole that could not be contacted at all: a
	// refused connection, a timeout, a name that does not resolve. Worth
	// retrying.
	ErrUnreachable = errors.New("pihole: server unreachable")

	// ErrUnauthorized is a Pi-hole that refused the password, or wants one
	// and was given none. Retrying will not fix it.
	ErrUnauthorized = errors.New("pihole: password rejected")

	// ErrTLS is a Pi-hole whose certificate could not be verified. It is told
	// apart from ErrUnreachable because the server is plainly there and no
	// number of retries will change its certificate: it wants Insecure set,
	// or a certificate installed.
	ErrTLS = errors.New("pihole: certificate not verified")

	// ErrUnexpected is an answer that is not the Pi-hole v6 API, such as an
	// older Pi-hole or something else listening on the port.
	ErrUnexpected = errors.New("pihole: unexpected answer")
)

// APIError is a call the server refused with an error of its own, such as
// every session seat being taken. Message and Hint are the server's, and say
// what to change.
type APIError struct {
	Path    string
	Key     string
	Message string
	Hint    string
}

func (e *APIError) Error() string {
	if e.Hint == "" {
		return fmt.Sprintf("pihole: %s: %s", e.Path, e.Message)
	}

	return fmt.Sprintf("pihole: %s: %s (%s)", e.Path, e.Message, e.Hint)
}
