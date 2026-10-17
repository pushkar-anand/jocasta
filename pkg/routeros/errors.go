package routeros

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// A caller acts differently on each of these sentinels. The question is
// whether the next attempt can go better. A router behind a firewall can come
// back. A wrong password stays wrong until someone edits the config.
var (
	// ErrUnreachable is a router that the client cannot contact at all. For
	// example, the connection is refused, it times out, or the name does not
	// resolve. A retry can help.
	ErrUnreachable = errors.New("routeros: router unreachable")

	// ErrUnauthorized is a router that refused the credentials. It is also a
	// user without the policy that the endpoint needs. A retry does not help.
	ErrUnauthorized = errors.New("routeros: credentials rejected")

	// ErrTLS is a router whose certificate does not verify. It is separate
	// from ErrUnreachable, because the router is clearly there. Retries do not
	// change its certificate. The fix is to set Insecure, or to import the
	// certificate.
	ErrTLS = errors.New("routeros: certificate not verified")

	// ErrNotFound is an endpoint that this build asks for and this router
	// does not serve. Usually, the router runs RouterOS 6, which has no REST
	// service. Or the router does not have the package for the endpoint.
	ErrNotFound = errors.New("routeros: endpoint not found")
)

// stdFailureNotAllowed is the RouterOS code for a request without a policy for
// the logged-in user. Its status is 500. The router authenticated the user,
// then its console refused the command. The router reports that refusal as a
// fault.
const stdFailureNotAllowed = 9

// stdFailure reads the RouterOS failure code from the end of a detail, such as
// "std failure: not allowed (9)". It returns -1 when there is no code.
//
// Only the code is safe to branch on. The text around it is for a human, and
// it can change between releases.
func stdFailure(detail string) int {
	if !strings.HasSuffix(detail, ")") {
		return -1
	}

	open := strings.LastIndexByte(detail, '(')
	if open < 0 {
		return -1
	}

	code, err := strconv.Atoi(detail[open+1 : len(detail)-1])
	if err != nil {
		return -1
	}

	return code
}

// Error is a fault that the router reported. RouterOS answers a failed REST
// call with a JSON body that names the problem. In a log, this is more useful
// than the status alone. For example, "no such command prefix" and "not enough
// permissions" are both 400.
type Error struct {
	// Status is the HTTP status of the answer.
	Status int `json:"-"`

	// Code is the RouterOS error number. It repeats the status.
	Code int `json:"error"`

	// Message is the short form, such as "Unauthorized".
	Message string `json:"message"`

	// Detail is the specific problem. It is often empty.
	Detail string `json:"detail"`
}

func (e *Error) Error() string {
	switch {
	case e.Detail != "" && e.Message != "":
		return fmt.Sprintf("routeros: %s: %s (%d)", e.Message, e.Detail, e.Status)
	case e.Message != "":
		return fmt.Sprintf("routeros: %s (%d)", e.Message, e.Status)
	default:
		return fmt.Sprintf("routeros: unexpected status %d", e.Status)
	}
}

// Unwrap maps the answer of the router to sentinels. So errors.Is tells whether
// to retry, and no caller needs to read Status.
//
// Some users log in, and then the router refuses every command. Such an
// account cannot do the job. This is a credentials problem, as a wrong
// password is. So it unwraps to ErrUnauthorized, although its status is 500.
func (e *Error) Unwrap() error {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusBadRequest:
		// For a menu that it does not have, the router answers "no such
		// command or directory (wireless)". An example is the wireless menu on
		// a router that runs wifi. The router gives no code for it, so only
		// the text can match.
		if strings.HasPrefix(e.Detail, "no such command") {
			return ErrNotFound
		}
	case http.StatusInternalServerError:
		if stdFailure(e.Detail) == stdFailureNotAllowed {
			return ErrUnauthorized
		}
	}

	return nil
}

// statusError reads what the router said about a non-OK response. If the body
// is not the expected JSON, it still returns an [Error] with the status. A
// different service on this port does not describe itself in RouterOS
// terms.
func statusError(status int, body io.Reader) error {
	e := &Error{Status: status}

	if err := json.UnmarshalRead(body, e); err != nil {
		return e
	}

	e.Status = status

	return e
}
