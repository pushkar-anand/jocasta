package openwrt

import (
	"errors"
	"fmt"
)

// A caller acts differently on each of these sentinels. The question is
// whether the next attempt can go better. A router behind a firewall can come
// back. A wrong password stays wrong until someone edits the config.
var (
	// ErrUnreachable is a router that the client cannot contact at all. For
	// example, the connection is refused, it times out, or the name does not
	// resolve. A retry can help.
	ErrUnreachable = errors.New("openwrt: router unreachable")

	// ErrUnauthorized is a router that refused the login. It is also a login
	// whose ACL does not allow a call. A retry does not help.
	ErrUnauthorized = errors.New("openwrt: credentials rejected")

	// ErrTLS is a router whose certificate does not verify. It is separate
	// from ErrUnreachable, because the router is clearly there. Retries do not
	// change its certificate. The fix is to set Insecure, or to install a
	// certificate.
	ErrTLS = errors.New("openwrt: certificate not verified")

	// ErrNotFound is an object or method that this build asks for and this
	// router does not publish. Usually, the package that publishes it is not
	// installed, such as rpcd-mod-luci.
	ErrNotFound = errors.New("openwrt: object or method not found")

	// ErrUnexpected is an answer that is not ubus JSON-RPC. For example, a
	// different service listens on the port.
	ErrUnexpected = errors.New("openwrt: unexpected answer")

	// errAccessDenied means that rpcd refused a session. Either the session
	// expired, or the ACL does not allow the call. [call] tells which, because
	// it logs in again.
	errAccessDenied = errors.New("openwrt: access denied")
)

// JSON-RPC error codes that uhttpd returns when a call does not reach the
// object.
const (
	rpcMethodNotFound = -32601
	rpcObjectNotFound = -32000
	rpcAccessDenied   = -32002
)

// rpcError is a JSON-RPC error. uhttpd returns it when it refuses a call
// before the call reaches the object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// err maps the error to the sentinels of this package.
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

// StatusError is a call that reached the object and failed there. It has the
// ubus status of the answer.
type StatusError struct {
	Object string
	Method string
	Code   int
}

func (s *StatusError) Error() string {
	return fmt.Sprintf("openwrt: %s %s: ubus status %d", s.Object, s.Method, s.Code)
}

// Unwrap maps the status to sentinels. So errors.Is tells whether to retry,
// and no caller needs to read Code. A login with a wrong password and a
// command that the ACL does not allow both return a permission status.
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
