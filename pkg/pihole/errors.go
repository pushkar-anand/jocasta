package pihole

import (
	"errors"
	"fmt"
)

// A caller acts differently on each of these sentinels. The question is
// whether the next attempt can go better. A Pi-hole behind a firewall can come
// back. A wrong password stays wrong until someone edits the config.
var (
	// ErrUnreachable is a Pi-hole that the client cannot contact at all. For
	// example, the connection is refused, it times out, or the name does not
	// resolve. A retry can help.
	ErrUnreachable = errors.New("pihole: server unreachable")

	// ErrUnauthorized is a Pi-hole that refused the password, or that needs a
	// password and got none. A retry does not help.
	ErrUnauthorized = errors.New("pihole: password rejected")

	// ErrTLS is a server whose certificate does not verify. It is separate
	// from ErrUnreachable, because the server is clearly there. Retries do not
	// change its certificate. The fix is to set Insecure, or to install a
	// certificate.
	ErrTLS = errors.New("pihole: certificate not verified")

	// ErrUnexpected is an answer that is not the Pi-hole v6 API. For example,
	// an older Pi-hole or a different service listens on the port.
	ErrUnexpected = errors.New("pihole: unexpected answer")
)

// APIError is a call that the server refused with its own error. For
// example, all session seats are in use. Message and Hint come from the
// server, and tell what to change.
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
