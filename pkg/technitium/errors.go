package technitium

import (
	"errors"
	"fmt"
)

// A caller acts differently on each of these sentinels. The question is
// whether the next attempt can go better. A server behind a firewall can come
// back. A wrong token stays wrong until someone edits the config.
var (
	// ErrUnreachable is a server that the client cannot contact at all. For
	// example, the connection is refused, it times out, or the name does not
	// resolve. A retry can help.
	ErrUnreachable = errors.New("technitium: server unreachable")

	// ErrUnauthorized is a server that refused the token as expired or
	// unknown. A retry does not help.
	ErrUnauthorized = errors.New("technitium: token rejected")

	// ErrTLS is a server whose certificate does not verify. It is separate
	// from ErrUnreachable, because the server is clearly there. Retries do not
	// change its certificate. The fix is to set Insecure, or to install a
	// certificate.
	ErrTLS = errors.New("technitium: certificate not verified")

	// ErrUnexpected is an answer that is not the Technitium API, such as
	// something else listening on the port.
	ErrUnexpected = errors.New("technitium: unexpected answer")
)

// APIError is a call that the server answered with status "error". For
// example, the user does not have the permission that the call needs, or the
// server does not know the app name. Message is the errorMessage of the
// server, which tells the cause.
type APIError struct {
	Path    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("technitium: %s: %s", e.Path, e.Message)
}
