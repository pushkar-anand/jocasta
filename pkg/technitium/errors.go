package technitium

import (
	"errors"
	"fmt"
)

// Sentinels a caller acts on differently. The distinction that matters is
// whether the next attempt could go better: a server behind a firewall may
// come back, a token that is wrong is wrong until someone edits the config.
var (
	// ErrUnreachable is a server that could not be contacted at all: a
	// refused connection, a timeout, a name that does not resolve. Worth
	// retrying.
	ErrUnreachable = errors.New("technitium: server unreachable")

	// ErrUnauthorized is a server that refused the token as expired or
	// unknown. Retrying will not fix it.
	ErrUnauthorized = errors.New("technitium: token rejected")

	// ErrTLS is a server whose certificate could not be verified. It is told
	// apart from ErrUnreachable because the server is plainly there and no
	// number of retries will change its certificate: it wants Insecure set,
	// or a certificate installed.
	ErrTLS = errors.New("technitium: certificate not verified")

	// ErrUnexpected is an answer that is not the Technitium API, such as
	// something else listening on the port.
	ErrUnexpected = errors.New("technitium: unexpected answer")
)

// APIError is a call the server answered with status "error", such as a user
// without the permission the call needs, or an app name it does not know.
// Message is the server's errorMessage, which says which.
type APIError struct {
	Path    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("technitium: %s: %s", e.Path, e.Message)
}
