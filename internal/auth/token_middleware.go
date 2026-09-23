package auth

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

const authHeaderName = "Authorization"

// TokenMiddleware guards the JSON API with a bearer token rather than the
// browser session Middleware checks: a script has no session to present, so
// the API needs a credential of its own.
type TokenMiddleware struct {
	jw     *response.JSONWriter
	a      *Auth
	bypass []*regexp.Regexp

	// methodScope refuses a request that changes something when the token
	// is read-only, judging "changes something" by the HTTP method.
	methodScope bool

	next http.Handler
}

// TokenOption adjusts what a TokenMiddleware checks.
type TokenOption func(*TokenMiddleware)

// WithTokenBypass exempts the paths matching any of bypass -- typically a
// health check -- from needing a token at all.
func WithTokenBypass(bypass ...*regexp.Regexp) TokenOption {
	return func(m *TokenMiddleware) { m.bypass = append(m.bypass, bypass...) }
}

// WithoutMethodScope leaves a token's scope for the handler to enforce, for a
// surface where the method says nothing about what a request does: over MCP,
// every call is a POST, whether it reads or writes. The handler reads the
// token's scope off [TokenFromContext].
func WithoutMethodScope() TokenOption {
	return func(m *TokenMiddleware) { m.methodScope = false }
}

// NewTokenMiddleware builds a bearer-token gate. jw is the same writer the
// guarded handlers answer with, so a request this middleware refuses gets the
// same problem-document shape as one a handler itself turned down.
//
// By default a read-only token is refused any method but a read.
func NewTokenMiddleware(
	jw *response.JSONWriter,
	a *Auth,
	opts ...TokenOption,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		m := &TokenMiddleware{jw: jw, a: a, methodScope: true, next: next}
		for _, opt := range opts {
			opt(m)
		}

		return m
	}
}

// tokenKey is the context key the verified token travels under.
type tokenKey struct{}

// TokenFromContext returns the token TokenMiddleware verified for this
// request, or nil if none was -- a bypassed path, or a request that never
// passed through the middleware.
func TokenFromContext(ctx context.Context) *models.ApiToken {
	token, _ := ctx.Value(tokenKey{}).(*models.ApiToken)

	return token
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

	if m.methodScope && !readOnly(r.Method) && token.Scope != dbtype.TokenReadWrite {
		m.jw.WriteProblem(w, r, response.NewProblem().
			WithStatus(http.StatusForbidden).
			WithDetail("this token is read-only").
			Build())

		return
	}

	m.next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, token)))
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
