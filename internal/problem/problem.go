// Package problem describes a failed request as an RFC 9457 problem document.
// The JSON API and the MCP server both answer with one, built from the same
// mapping, so a caller sees one failure described the same way whichever
// surface it asked through.
package problem

import (
	"errors"
	"maps"
	"net/http"
	"strings"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// For renders the errors the inventory returns that are not simply failures.
// Anything else is nil, which the caller turns into a generic 500: all a
// caller should learn about an unexpected error.
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

// Resolve is the whole of the decision response.JSONWriter makes about an
// error: For's mapping first, then a Problem the error already is. It reports
// false for an error neither describes, which the caller logs and answers with
// Internal.
func Resolve(err error) (response.Problem, bool) {
	if p := For(err); p != nil {
		return p, true
	}

	if p, ok := errors.AsType[response.Problem](err); ok {
		return p, true
	}

	return nil, false
}

// Internal is the problem an unexpected error is answered with. It says only
// that something failed, and the cause goes to the server log.
func Internal() response.Problem {
	return response.NewProblem().Build()
}

// Document lays p out with the members response.JSONWriter writes, for a
// surface that carries the document somewhere other than an HTTP response
// body. instance names what failed, where the API names the request path.
func Document(p response.Problem, instance string) map[string]any {
	doc := map[string]any{
		"title":    p.Title(),
		"status":   p.Status(),
		"detail":   p.Detail(),
		"instance": instance,
	}

	// RFC 9457: with no type of its own, a problem is about:blank and its title
	// is the status text.
	if t := p.Type(); t == "" || strings.EqualFold(t, "about:blank") {
		doc["type"] = "about:blank"
		doc["title"] = http.StatusText(p.Status())
	} else {
		doc["type"] = t
	}

	maps.Copy(doc, p.CustomMembers())

	return doc
}
