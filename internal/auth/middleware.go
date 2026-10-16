package auth

import (
	"net/http"
	"regexp"
)

// setupPath is the only route that a request can reach only while
// SetupRequired is true. Only here does the usual rule of this middleware go
// the other way.
const setupPath = "/setup"

type sessionMiddleware struct {
	sm *Session
	a  *Auth

	// always exempts a path from every check of this middleware. An example
	// is the static assets that the setup and sign-in pages need.
	always []*regexp.Regexp

	// signIn exempts a path, usually /login, from the session check. The
	// setup redirect still applies to it, as to every other route.
	signIn []*regexp.Regexp

	next http.Handler
}

// NewSessionMiddleware creates HTTP middleware that enforces session checks,
// and bypasses some request paths. The always and signIn fields of
// sessionMiddleware tell what each bypass list exempts a path from.
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

// ServeHTTP redirects requests to setup or sign-in when necessary, with the
// configured exemptions. After the first account, nobody can reach setup.
func (m *sessionMiddleware) ServeHTTP(
	w http.ResponseWriter,
	r *http.Request,
) {
	ctx := r.Context()
	path := r.URL.Path

	for _, b := range m.always {
		if b.MatchString(path) {
			m.next.ServeHTTP(w, r)
			return
		}
	}

	setupRequired, err := m.a.SetupRequired(ctx)
	if err != nil {
		http.Error(w, "Could not check account setup. Try again; if this continues, ask an admin to check the server log.", http.StatusInternalServerError)
		return
	}

	switch {
	case path == setupPath && setupRequired:
		m.next.ServeHTTP(w, r)
		return
	case path == setupPath:
		// Setup already happened. This visitor signs in as everybody else.
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	case setupRequired:
		http.Redirect(w, r, setupPath, http.StatusFound)
		return
	}

	for _, b := range m.signIn {
		if b.MatchString(path) {
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
