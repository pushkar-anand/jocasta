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

// Sentinels a caller acts on differently. The distinction that matters is
// whether the next attempt could go better: a router behind a firewall may
// come back, a password that is wrong is wrong until someone edits the config.
var (
	// ErrUnreachable is a router that could not be contacted at all -- a
	// refused connection, a timeout, a name that does not resolve. Worth
	// retrying.
	ErrUnreachable = errors.New("routeros: router unreachable")

	// ErrUnauthorized is a router that refused the credentials, or a user
	// without the policy the endpoint needs. Retrying will not fix it.
	ErrUnauthorized = errors.New("routeros: credentials rejected")

	// ErrTLS is a router whose certificate could not be verified. It is told
	// apart from ErrUnreachable because the router is plainly there and no
	// number of retries will change its certificate: it wants Insecure set,
	// or the certificate imported.
	ErrTLS = errors.New("routeros: certificate not verified")

	// ErrNotFound is an endpoint this build asks for and this router does not
	// serve -- most often a RouterOS 6 box, which has no REST service at all,
	// or a package the router does not have installed.
	ErrNotFound = errors.New("routeros: endpoint not found")
)

// stdFailureNotAllowed is RouterOS's own code for a request the logged-in user
// has no policy for. The status that carries it is 500, not 403: the router
// authenticated the user and then its console refused the command, and it
// reports that refusal as a fault rather than as a permission decision.
const stdFailureNotAllowed = 9

// stdFailure reads RouterOS's failure code off the end of a detail such as
// "std failure: not allowed (9)", returning -1 when there is none.
//
// The code is the only part of that string worth branching on. The prose
// around it is the router explaining itself to a human and is not a contract.
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

// Error is a fault the router itself reported. RouterOS answers a failed REST
// call with a JSON body naming what went wrong, which is more use in a log
// than the status alone: "no such command prefix" and "not enough permissions"
// are both 400.
type Error struct {
	// Status is the HTTP status the router answered with.
	Status int `json:"-"`

	// Code is RouterOS's own error number, which repeats the status.
	Code int `json:"error"`

	// Message is the short form, such as "Unauthorized".
	Message string `json:"message"`

	// Detail is the specific complaint, and is often empty.
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

// Unwrap maps what the router said onto sentinels, so that errors.Is answers
// the retry question without anyone reading Status.
//
// A user that logs in and is then refused every command is as much a
// credentials problem as a wrong password -- the account exists and cannot do
// the job -- so it unwraps to ErrUnauthorized despite arriving as a 500.
func (e *Error) Unwrap() error {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusInternalServerError:
		if stdFailure(e.Detail) == stdFailureNotAllowed {
			return ErrUnauthorized
		}
	}

	return nil
}

// statusError reads what the router said about a non-OK response. A body that
// is not the expected JSON is not itself an error: the status is the fact, and
// something answering this port that is not a router will not describe itself
// in RouterOS's terms.
func statusError(status int, body io.Reader) error {
	e := &Error{Status: status}

	if err := json.UnmarshalRead(body, e); err != nil {
		return e
	}

	e.Status = status

	return e
}
