package auth

import (
	"net/http"
	"regexp"
)

// setupPath is the one route reachable only while SetupRequired holds -- the
// one place this middleware's usual requirement runs the other way around.
const setupPath = "/setup"

type sessionMiddleware struct {
	sm *Session
	a  *Auth

	// always exempts a path from every check this middleware makes, such as
	// the static assets the setup and sign-in pages both need to render.
	always []*regexp.Regexp

	// signIn exempts a path from needing a session -- typically /login --
	// while leaving it subject to the setup redirect like every other route.
	signIn []*regexp.Regexp

	next http.Handler
}

// NewSessionMiddleware creates HTTP middleware to enforce session checks and
// bypass certain request paths if matched. See sessionMiddleware's always and
// signIn fields for what the two bypass lists exempt a path from.
func NewSessionMiddleware(
	sm *Session,
	a *Auth,
	always []*regexp.Regexp,
	signIn []*regexp.Regexp,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &sessionMiddleware{sm, a, always, signIn, next}
	}
}

func (m *sessionMiddleware) ServeHTTP(
	w http.ResponseWriter,
	r *http.Request,
) {
	ctx := r.Context()
	uri := r.RequestURI

	for _, b := range m.always {
		if b.MatchString(uri) {
			m.next.ServeHTTP(w, r)
			return
		}
	}

	setupRequired, err := m.a.SetupRequired(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	switch {
	case r.URL.Path == setupPath && setupRequired:
		m.next.ServeHTTP(w, r)
		return
	case r.URL.Path == setupPath:
		// Setup already happened elsewhere; this visitor signs in like anyone
		// else instead.
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	case setupRequired:
		http.Redirect(w, r, setupPath, http.StatusFound)
		return
	}

	for _, b := range m.signIn {
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
