package openwrt

import (
	"errors"
	"fmt"
)

// Sentinels a caller acts on differently. The distinction that matters is
// whether the next attempt could go better: a router behind a firewall may
// come back, a password that is wrong is wrong until someone edits the config.
var (
	// ErrUnreachable is a router that could not be contacted at all: a
	// refused connection, a timeout, a name that does not resolve. Worth
	// retrying.
	ErrUnreachable = errors.New("openwrt: router unreachable")

	// ErrUnauthorized is a router that refused the login, or a login whose
	// ACL does not grant a call. Retrying will not fix it.
	ErrUnauthorized = errors.New("openwrt: credentials rejected")

	// ErrTLS is a router whose certificate could not be verified. It is told
	// apart from ErrUnreachable because the router is plainly there and no
	// number of retries will change its certificate: it wants Insecure set,
	// or a certificate installed.
	ErrTLS = errors.New("openwrt: certificate not verified")

	// ErrNotFound is an object or method this build asks for and this router
	// does not publish. Most often the package that publishes it, such as
	// rpcd-mod-luci, is not installed.
	ErrNotFound = errors.New("openwrt: object or method not found")

	// ErrUnexpected is an answer that is not ubus JSON-RPC, such as something
	// else listening on the port.
	ErrUnexpected = errors.New("openwrt: unexpected answer")

	// errAccessDenied is rpcd refusing a session, which is either expired or
	// not granted the call. [call] tells the two apart by logging in again.
	errAccessDenied = errors.New("openwrt: access denied")
)

// JSON-RPC error codes uhttpd answers with when a call never reaches the
// object.
const (
	rpcMethodNotFound = -32601
	rpcObjectNotFound = -32000
	rpcAccessDenied   = -32002
)

// rpcError is a JSON-RPC error, which uhttpd answers with when it refuses a
// call before it reaches the object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// err maps the error onto this package's sentinels.
func (e *rpcError) err(object, method string) error {
	var sentinel error

	switch e.Code {
	case rpcAccessDenied:
		sentinel = errAccessDenied
	case rpcObjectNotFound, rpcMethodNotFound:
		sentinel = ErrNotFound
	default:
		sentinel = ErrUnexpected
	}

	return fmt.Errorf("%w: %s %s: %s (%d)", sentinel, object, method, e.Message, e.Code)
}

// ubus status codes that a caller branches on, from libubus.
const (
	statusMethodNotFound   = 3
	statusNotFound         = 4
	statusPermissionDenied = 6
)

// StatusError is a call that reached the object and failed there, with the ubus
// status it answered with.
type StatusError struct {
	Object string
	Method string
	Code   int
}

func (s *StatusError) Error() string {
	return fmt.Sprintf("openwrt: %s %s: ubus status %d", s.Object, s.Method, s.Code)
}

// Unwrap maps the status onto sentinels, so that errors.Is answers the retry
// question without anyone reading Code. A login with a wrong password and a
// command the ACL does not allow both answer with a permission status.
func (s *StatusError) Unwrap() error {
	switch s.Code {
	case statusPermissionDenied:
		return ErrUnauthorized
	case statusMethodNotFound, statusNotFound:
		return ErrNotFound
	default:
		return nil
	}
}
