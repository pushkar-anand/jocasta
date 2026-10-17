// Package problem describes a failed request as an RFC 9457 problem document.
// The JSON API and the MCP server both answer with one, from the same
// mapping. So a caller sees the same description of a failure on both.
package problem

import (
	"errors"
	"maps"
	"net/http"
	"strings"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// For maps known inventory errors to HTTP problems. It returns nil for
// unexpected errors. Their details go only in the server log.
func For(err error) response.Problem {
	if errors.Is(err, inventory.ErrNotFound) {
		return response.NewProblem().
			WithStatus(http.StatusNotFound).
			WithTitle(http.StatusText(http.StatusNotFound)).
			WithDetail(err.Error()).
			Build()
	}

	return nil
}

// Resolve is the full decision that response.JSONWriter makes about an error.
// It tries the mapping of For first, then a Problem that the error already
// is. It reports false for an error that neither describes. The caller logs
// that error and answers with Internal.
func Resolve(err error) (response.Problem, bool) {
	if p := For(err); p != nil {
		return p, true
	}

	if p, ok := errors.AsType[response.Problem](err); ok {
		return p, true
	}

	return nil, false
}

// Internal is the answer to an unexpected error. It tells only that something
// failed. The cause goes to the server log.
func Internal() response.Problem {
	return response.NewProblem().Build()
}

// Document lays out p with the members that response.JSONWriter writes. Use
// it where the document does not go in an HTTP response body. instance names
// what failed. The API uses the request path there.
func Document(p response.Problem, instance string) map[string]any {
	doc := map[string]any{
		"title":    p.Title(),
		"status":   p.Status(),
		"detail":   p.Detail(),
		"instance": instance,
	}

	// RFC 9457: a problem with no type is about:blank, and its title is the
	// status text.
	if t := p.Type(); t == "" || strings.EqualFold(t, "about:blank") {
		doc["type"] = "about:blank"
		doc["title"] = http.StatusText(p.Status())
	} else {
		doc["type"] = t
	}

	maps.Copy(doc, p.CustomMembers())

	return doc
}
