package auth

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

const authHeaderName = "Authorization"

// TokenMiddleware guards the JSON API with a bearer token rather than the
// browser session Middleware checks: a script has no session to present, so
// the API needs a credential of its own.
type TokenMiddleware struct {
	jw     *response.JSONWriter
	a      *Auth
	bypass []*regexp.Regexp
	next   http.Handler
}

// NewTokenMiddleware builds the API's auth gate. jw is the same writer the
// API handlers answer with, so a request this middleware refuses gets the
// same problem-document shape as one the API itself turned down. bypass
// exempts a path -- typically a health check -- from needing a token at all.
func NewTokenMiddleware(
	jw *response.JSONWriter,
	a *Auth,
	bypass ...*regexp.Regexp,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &TokenMiddleware{jw, a, bypass, next}
	}
}

func (m *TokenMiddleware) ServeHTTP(
	w http.ResponseWriter,
	r *http.Request,
) {
	// Matched against the path rather than RequestURI: this sits inside
	// StripPrefix, which rewrites URL.Path but not the untouched RequestURI, so
	// a bypass written against the mounted prefix would never match here.
	path := r.URL.Path

	for _, b := range m.bypass {
		if b.MatchString(path) {
			m.next.ServeHTTP(w, r)
			return
		}
	}

	raw, ok := strings.CutPrefix(r.Header.Get(authHeaderName), "Bearer ")
	if !ok {
		m.unauthorized(w, r)
		return
	}

	token, err := m.a.VerifyToken(r.Context(), raw)
	if err != nil {
		m.unauthorized(w, r)
		return
	}

	if !readOnly(r.Method) && token.Scope != dbtype.TokenReadWrite {
		m.jw.WriteProblem(w, r, response.NewProblem().
			WithStatus(http.StatusForbidden).
			WithDetail("this token is read-only").
			Build())

		return
	}

	m.next.ServeHTTP(w, r)
}

// readOnly reports whether method only reads -- the same distinction a token
// scope draws, so a read-scoped token can be handed to something that has no
// business writing.
func readOnly(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}

	return false
}

func (m *TokenMiddleware) unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="jocasta"`)

	m.jw.WriteProblem(w, r, response.NewProblem().
		WithStatus(http.StatusUnauthorized).
		WithDetail("missing or invalid API token").
		Build())
}
