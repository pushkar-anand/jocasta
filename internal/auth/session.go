package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/security/session"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// Data is the whole of what a session carries, as one typed document: a
// misspelled field is then a compile error, not a read that silently returns
// the zero value.
type Data struct {
	// UserID is the signed-in account, or 0 when nobody is signed in.
	UserID int64

	// Username is cached here so the topbar shows the signed-in account without
	// a per-request lookup.
	Username string

	// Role is what the account was allowed to do at the moment it signed in.
	Role dbtype.UserRole

	// Flash holds values a handler leaves for the GET it redirects to and
	// that are read back exactly once -- a message, or a secret shown a
	// single time. nil until the first Flash call.
	Flash map[string]string
}

// Session adapts the generic typed session to jocasta's own vocabulary
// (CurrentUserID, Flash, PopFlash, Logout), so callers in internal/web never
// import the session library or name its types.
type Session struct {
	s *session.Session[Data]
}

// NewSession sets every cookie and lifetime option explicitly rather than
// leaning on the library defaults, so a change to those defaults can't quietly
// move jocasta's session semantics.
func NewSession(log *slog.Logger) *Session {
	s := session.New[Data](
		session.WithLogger(log),
		session.WithLifetime(7*24*time.Hour),
		session.WithIdleTimeout(24*time.Hour),
		session.WithCookieName("jocasta_session"),
		session.WithCookiePath("/"),
		session.WithCookieHttpOnly(true),
		session.WithCookieSameSite(http.SameSiteStrictMode),
		session.WithCookiePersist(false),
	)

	return &Session{s: s}
}

// LoadAndSave wraps next with the session middleware every request must pass
// through before any code in this package can touch session data.
func (s *Session) LoadAndSave(next http.Handler) http.Handler {
	return s.s.LoadAndSave(next)
}

// Load returns a context carrying session data for token, so a test can get one
// without an HTTP round trip through the middleware.
func (s *Session) Load(ctx context.Context, token string) (context.Context, error) {
	return s.s.Manager().Load(ctx, token)
}

// CurrentUserID returns the id establishSession put in the session, if any.
func (s *Session) CurrentUserID(ctx context.Context) (int64, bool) {
	d, ok := s.s.Current(ctx)
	if !ok || d.UserID == 0 {
		return 0, false
	}

	return d.UserID, true
}

// CurrentUsername returns the signed-in account's name, or "".
func (s *Session) CurrentUsername(ctx context.Context) string {
	d, ok := s.s.Current(ctx)
	if !ok {
		return ""
	}

	return d.Username
}

// CurrentRole returns the role for the User
func (s *Session) CurrentRole(ctx context.Context) dbtype.UserRole {
	d, ok := s.s.Current(ctx)
	if !ok {
		return ""
	}

	return d.Role
}

// Flash stores a value read back exactly once. It is how a handler carries a
// result -- a message, or a secret shown a single time -- across the redirect
// it makes after a POST, so a reload re-fetches the page rather than resending
// the form.
func (s *Session) Flash(ctx context.Context, key, value string) {
	s.s.Update(ctx, func(d *Data) {
		if d.Flash == nil {
			d.Flash = make(map[string]string, 1)
		}

		d.Flash[key] = value
	})
}

// PopFlash returns the value Flash stored under key and removes it, or "" when
// there is none.
func (s *Session) PopFlash(ctx context.Context, key string) string {
	d, ok := s.s.Current(ctx)
	if !ok {
		return ""
	}

	v, ok := d.Flash[key]
	if !ok {
		return ""
	}

	s.s.Update(ctx, func(d *Data) {
		delete(d.Flash, key)
	})

	return v
}

// Logout ends the session -- Destroy under the name a caller of this package
// actually wants.
func (s *Session) Logout(ctx context.Context) error {
	return s.s.Destroy(ctx)
}
