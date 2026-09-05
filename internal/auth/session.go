package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

// sessionUserKey is the one value a session carries: the id Login writes and
// CurrentUserID and the middleware's presence check both read back. Kept
// unexported so nothing outside this package can address the session under a
// different name and quietly stop agreeing with it.
const sessionUserKey = "user"

type Session struct {
	sm *scs.SessionManager
}

func NewSession() *Session {
	sm := scs.New()

	sm.Lifetime = 7 * 24 * time.Hour
	sm.IdleTimeout = 24 * time.Hour

	sm.Cookie.Name = "jocasta_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.Path = "/"
	sm.Cookie.SameSite = http.SameSiteStrictMode
	sm.Cookie.Persist = false

	return &Session{
		sm,
	}
}

// LoadAndSave wraps next with the session middleware every request must pass
// through before any code in this package can touch session data.
func (s *Session) LoadAndSave(next http.Handler) http.Handler {
	return s.sm.LoadAndSave(next)
}

// Load returns a context carrying session data for token, the way the
// request middleware does for a live request -- for a test that needs one
// without going through HTTP to get it.
func (s *Session) Load(ctx context.Context, token string) (context.Context, error) {
	return s.sm.Load(ctx, token)
}

// CurrentUserID returns the id Login put in the session, if any.
func (s *Session) CurrentUserID(ctx context.Context) (int64, bool) {
	id, ok := s.sm.Get(ctx, sessionUserKey).(int64)
	return id, ok
}

// Logout ends the session -- Destroy under the name a caller of this package
// actually wants.
func (s *Session) Logout(ctx context.Context) error {
	return s.sm.Destroy(ctx)
}
