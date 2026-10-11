package adguard

import (
	"errors"
	"fmt"
)

// Sentinels a caller acts on differently. The distinction that matters is
// whether the next attempt could go better: a server behind a firewall may
// come back, a login that is wrong is wrong until someone edits the config.
var (
	// ErrUnreachable is a server that could not be contacted at all: a
	// refused connection, a timeout, a name that does not resolve. Worth
	// retrying.
	ErrUnreachable = errors.New("adguard: server unreachable")

	// ErrUnauthorized is a server that refused the login, or wants one and
	// was given none. Retrying will not fix it.
	ErrUnauthorized = errors.New("adguard: login rejected")

	// ErrTLS is a server whose certificate could not be verified. It is told
	// apart from ErrUnreachable because the server is plainly there and no
	// number of retries will change its certificate: it wants Insecure set,
	// or a certificate installed.
	ErrTLS = errors.New("adguard: certificate not verified")

	// ErrUnexpected is an answer that is not the AdGuard Home API, such as
	// something else listening on the port.
	ErrUnexpected = errors.New("adguard: unexpected answer")
)

// APIError is a call the server refused for a reason of its own. Message is
// the server's wording, empty when it gave none.
type APIError struct {
	Path    string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("adguard: %s: status %d", e.Path, e.Status)
	}

	return fmt.Sprintf("adguard: %s: status %d: %s", e.Path, e.Status, e.Message)
}
