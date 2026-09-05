package auth

import (
	"net/http"
	"regexp"
)

type sessionMiddleware struct {
	sm     *Session
	Bypass []*regexp.Regexp
	next   http.Handler
}

// NewSessionMiddleware creates HTTP middleware to enforce session checks and bypass certain request paths if matched.
func NewSessionMiddleware(
	sm *Session,
	bypass ...*regexp.Regexp,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &sessionMiddleware{
			sm, bypass, next,
		}
	}
}

func (m *sessionMiddleware) ServeHTTP(
	w http.ResponseWriter,
	r *http.Request,
) {
	ctx := r.Context()
	uri := r.RequestURI

	for _, b := range m.Bypass {
		if b.MatchString(uri) {
			m.next.ServeHTTP(w, r)
			return
		}
	}

	if _, ok := m.sm.CurrentUserID(ctx); !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	m.next.ServeHTTP(w, r)
}
