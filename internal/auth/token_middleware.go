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

// TokenMiddleware guards the JSON API with a bearer token. A script has no
// browser session. So the API needs its own credential.
type TokenMiddleware struct {
	jw     *response.JSONWriter
	a      *Auth
	bypass []*regexp.Regexp

	// methodScope refuses a request that changes something when the token
	// is read-only. The HTTP method tells whether a request changes
	// something.
	methodScope bool

	next http.Handler
}

// TokenOption adjusts what a TokenMiddleware checks.
type TokenOption func(*TokenMiddleware)

// WithTokenBypass exempts the paths that match one of bypass from the token
// check. An example is a health check.
func WithTokenBypass(bypass ...*regexp.Regexp) TokenOption {
	return func(m *TokenMiddleware) { m.bypass = append(m.bypass, bypass...) }
}

// WithoutMethodScope leaves the scope of a token to the handler. Use it where
// the method tells nothing about what a request does. Over MCP, every call is
// a POST, for reads and for writes. The handler reads the scope of the token
// from [TokenFromContext].
func WithoutMethodScope() TokenOption {
	return func(m *TokenMiddleware) { m.methodScope = false }
}

// NewTokenMiddleware builds a bearer-token gate. jw is the same writer as for
// the guarded handlers. So a request that this middleware refuses gets the
// same problem-document shape as a request that a handler refused.
//
// By default, a read-only token can use only a read method.
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

// tokenKey is the context key of the verified token.
type tokenKey struct{}

// TokenFromContext returns the token that TokenMiddleware verified for this
// request. It returns nil for a bypassed path, or for a request that never
// passed through the middleware.
func TokenFromContext(ctx context.Context) *models.ApiToken {
	token, _ := ctx.Value(tokenKey{}).(*models.ApiToken)

	return token
}

// ServeHTTP verifies a bearer token, and adds it to the request context. If
// method checks are on, a read-only token cannot make writes.
func (m *TokenMiddleware) ServeHTTP(
	w http.ResponseWriter,
	r *http.Request,
) {
	// Match against URL.Path. This middleware is inside StripPrefix.
	// StripPrefix removes the mounted prefix from URL.Path, but not from
	// RequestURI.
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
			WithDetail("This token is read-only. Use a read_write token to change the inventory.").
			Build())

		return
	}

	m.next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, token)))
}

// readOnly reports whether method only reads. A token scope uses the same
// rule. So you can give a read-scoped token to a client that must not
// write.
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
		WithDetail("API token not recognised. Send an active token in the Authorization header as Bearer <token>.").
		Build())
}
