package auth

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/security/session"
	"github.com/pushkar-anand/build-with-go/security/session/sqlitestore"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// Data is all that a session holds, as one typed document. So a misspelled
// field is a compile error. A string-keyed read would return the zero value,
// with no warning.
type Data struct {
	// UserID is the signed-in account, or 0 when nobody is signed in.
	UserID int64

	// Username is cached here. So the topbar shows the signed-in account
	// without a lookup on each request.
	Username string

	// Role is what the account could do when it signed in.
	Role dbtype.UserRole

	// Flash holds values that a handler leaves for the GET of its redirect,
	// such as a message. The GET reads each value exactly once. It is nil
	// until the first Flash call.
	Flash map[string]string

	// PendingUserID is set when Verify succeeds for an account with 2FA on.
	// It is cleared when a second factor also succeeds. A nonzero value here
	// proves nothing alone. Every other check uses UserID, which stays zero
	// until then.
	PendingUserID int64

	// PendingUsername goes with PendingUserID, as Username goes with UserID.
	// So the second-factor page can name the account without a lookup.
	PendingUsername string
}

// Session adapts the generic typed session to the words of jocasta
// (CurrentUserID, Flash, PopFlash, Logout). So callers in internal/web never
// import the session library or name its types.
type Session struct {
	s *session.Session[Data]
}

// SessionOption configures NewSession.
type SessionOption func(*sessionConfig)

type sessionConfig struct {
	db *sql.DB

	lifetime     time.Duration
	idleTimeout  time.Duration
	cookieSecure bool
}

// WithSessionStore stores sessions in db, the jocasta SQLite database. So a
// signed-in browser stays signed in after a restart. Without it, sessions are
// only in memory. The tests need this, so the option is opt-in.
func WithSessionStore(db *sql.DB) SessionOption {
	return func(c *sessionConfig) { c.db = db }
}

// WithLifetime limits how long a session lasts after sign-in, with or without
// activity. NewSession ignores a d that is not positive, and keeps the
// default. A zero would expire every session at once.
func WithLifetime(d time.Duration) SessionOption {
	return func(c *sessionConfig) {
		if d > 0 {
			c.lifetime = d
		}
	}
}

// WithIdleTimeout sets how long a session stays without a request. Then the
// session ends. NewSession ignores a d that is not positive, and keeps the
// default.
func WithIdleTimeout(d time.Duration) SessionOption {
	return func(c *sessionConfig) {
		if d > 0 {
			c.idleTimeout = d
		}
	}
}

// WithCookieSecure sets whether the session cookie works only over HTTPS. It is
// true in every real deployment. false exists only for sign-in over plain HTTP
// during local development.
func WithCookieSecure(secure bool) SessionOption {
	return func(c *sessionConfig) { c.cookieSecure = secure }
}

// NewSession sets every cookie and lifetime option explicitly. So a change to
// the defaults of the library cannot change the session behaviour of jocasta
// without a warning.
//
// The lifetime, idle timeout, and cookie-secure flag start at values that a
// deployment can override in config. jocasta sets the cookie name, path,
// SameSite and HttpOnly flags itself.
func NewSession(log *slog.Logger, opts ...SessionOption) *Session {
	cfg := sessionConfig{
		lifetime:     7 * 24 * time.Hour,
		idleTimeout:  24 * time.Hour,
		cookieSecure: true,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	sopts := []session.Option{
		session.WithLogger(log),
		session.WithLifetime(cfg.lifetime),
		session.WithIdleTimeout(cfg.idleTimeout),
		session.WithCookieName("jocasta_session"),
		session.WithCookiePath("/"),
		session.WithCookieHttpOnly(true),
		session.WithCookieSameSite(http.SameSiteStrictMode),
		session.WithCookieSecure(cfg.cookieSecure),
		session.WithCookiePersist(false),
	}

	if cfg.db != nil {
		sopts = append(sopts, session.WithStore(sqlitestore.New(cfg.db)))
	}

	return &Session{s: session.New[Data](sopts...)}
}

// LoadAndSave wraps next with the session middleware. Every request must pass
// through it before code in this package can use session data.
func (s *Session) LoadAndSave(next http.Handler) http.Handler {
	return s.s.LoadAndSave(next)
}

// Load returns a context with session data for token. So a test can get one
// without an HTTP round trip through the middleware.
func (s *Session) Load(ctx context.Context, token string) (context.Context, error) {
	return s.s.Manager().Load(ctx, token)
}

// CurrentUserID returns the id that establishSession put in the session, if
// any.
func (s *Session) CurrentUserID(ctx context.Context) (int64, bool) {
	d, ok := s.s.Current(ctx)
	if !ok || d.UserID == 0 {
		return 0, false
	}

	return d.UserID, true
}

// CurrentUsername returns the name of the signed-in account, or "".
func (s *Session) CurrentUsername(ctx context.Context) string {
	d, ok := s.s.Current(ctx)
	if !ok {
		return ""
	}

	return d.Username
}

// CurrentRole returns the role of the signed-in account, or "".
func (s *Session) CurrentRole(ctx context.Context) dbtype.UserRole {
	d, ok := s.s.Current(ctx)
	if !ok {
		return ""
	}

	return d.Role
}

// Flash stores a value that the next request reads exactly once. With it, a
// handler keeps a result, such as a message, through the redirect after a
// POST. So a reload gets the page again, and does not send the form again.
// The value is saved with the session. With [WithSessionStore], it goes in the
// database. So it must not be a secret.
func (s *Session) Flash(ctx context.Context, key, value string) {
	s.s.Update(ctx, func(d *Data) {
		if d.Flash == nil {
			d.Flash = make(map[string]string, 1)
		}

		d.Flash[key] = value
	})
}

// PopFlash returns the value that Flash stored under key, and removes it. It
// returns "" when there is none.
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

// Logout ends the session and deletes its data from the store.
func (s *Session) Logout(ctx context.Context) error {
	return s.s.Destroy(ctx)
}

// HasPendingTOTP reports whether Login left this session to wait for a second
// factor.
func (s *Session) HasPendingTOTP(ctx context.Context) bool {
	d, ok := s.s.Current(ctx)
	return ok && d.PendingUserID != 0
}
