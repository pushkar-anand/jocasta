package adguard

import (
	"errors"
	"fmt"
)

// A caller acts differently on each of these sentinels. The question is
// whether the next attempt can go better. A server behind a firewall can come
// back. A wrong login stays wrong until someone edits the config.
var (
	// ErrUnreachable is a server that the client cannot contact at all. For
	// example, the connection is refused, it times out, or the name does not
	// resolve. A retry can help.
	ErrUnreachable = errors.New("adguard: server unreachable")

	// ErrUnauthorized is a server that refused the login, or that needs a
	// login and got none. A retry does not help.
	ErrUnauthorized = errors.New("adguard: login rejected")

	// ErrTLS is a server whose certificate does not verify. It is separate
	// from ErrUnreachable, because the server is clearly there. Retries do not
	// change its certificate. The fix is to set Insecure, or to install a
	// certificate.
	ErrTLS = errors.New("adguard: certificate not verified")

	// ErrUnexpected is an answer that is not the AdGuard Home API. For
	// example, a different service listens on the port.
	ErrUnexpected = errors.New("adguard: unexpected answer")
)

// APIError is a call that the server refused for its own reason. Message is
// the text of the server. It is empty when the server gave no text.
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
