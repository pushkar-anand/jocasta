package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

type (
	// hasher is what Auth needs from the password hashing library it uses.
	hasher interface {
		Hash(string) (string, error)
		Compare(string, string) error
	}

	// userManager is what Auth needs from the generated store for everything
	// account-related: verifying a credential, setup and admin management alike.
	userManager interface {
		GetUserByUsername(ctx context.Context, username string) (*models.User, error)
		GetUserByID(ctx context.Context, id int64) (*models.User, error)
		CreateUser(ctx context.Context, arg models.CreateUserParams) (*models.User, error)
		CountUsers(ctx context.Context) (int64, error)
		ListUsers(ctx context.Context) ([]*models.User, error)
	}

	// tokenManager is what Auth needs from the store to manage API tokens
	tokenManager interface {
		CreateAPIToken(ctx context.Context, arg models.CreateAPITokenParams) (*models.ApiToken, error)
		TouchAPITokenByHash(ctx context.Context, arg models.TouchAPITokenByHashParams) (*models.ApiToken, error)
		ListAPITokensByUser(ctx context.Context, userID int64) ([]*models.ApiToken, error)
		DeleteAPIToken(ctx context.Context, arg models.DeleteAPITokenParams) error
	}

	// store is what Auth needs from the generated store altogether both credential
	// lookup and API token management
	store interface {
		userManager
		tokenManager
	}
)

// Auth verifies a login attempt against stored credentials, and issues and
// checks the API tokens that stand in for one where there is no session to
// carry.
type Auth struct {
	q      userManager
	tokens tokenManager
	hasher hasher

	// now is a field so a test can pin the timestamps it asserts on.
	now func() time.Time

	// unknownUserHash is compared against on a username miss, so that path
	// costs the same derivation as a wrong password against a real
	// user. Without it, Compare would reject an unknown user's missing hash
	// at the format-parsing stage, well before deriving anything, and the
	// timing gap between the two paths would answer "does this account
	// exist?" for free.
	unknownUserHash string

	// hasUsers caches whether an account exists at all, so SetupRequired costs
	// a query only until the first one is confirmed -- there is no path in
	// this package that removes the last account, so a true answer here never
	// goes stale.
	hasUsers atomic.Bool
}

// New builds an Auth.
func New(s store, hasher hasher) (*Auth, error) {
	unknownUserHash, err := hasher.Hash("no-such-user")
	if err != nil {
		return nil, fmt.Errorf("prepare unknown-user hash: %w", err)
	}

	return &Auth{
		q:               s,
		tokens:          s,
		hasher:          hasher,
		now:             time.Now,
		unknownUserHash: unknownUserHash,
	}, nil
}

// Verify checks username and password against the stored credential and
// returns the matching user only once both hold.
func (a *Auth) Verify(ctx context.Context, username, password string) (*models.User, error) {
	user, err := a.q.GetUserByUsername(ctx, username)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		_ = a.hasher.Compare(password, a.unknownUserHash)
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, fmt.Errorf("user %q: %w", username, err)
	}

	if err := a.hasher.Compare(password, user.PasswordHash); err != nil {
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

// Login is a util over Verify that also generates the new session token and
// stores it in the session.
func (a *Auth) Login(
	ctx context.Context,
	sm *Session,
	username, password string,
	rememberMe bool,
) (*models.User, error) {
	user, err := a.Verify(ctx, username, password)
	if err != nil {
		return nil, err
	}

	if err := a.establishSession(ctx, sm, user.ID); err != nil {
		return nil, err
	}

	if rememberMe {
		sm.sm.RememberMe(ctx, true)
	}

	return user, nil
}

// establishSession renews the session token and records who it belongs to --
// the one sequence both signing in and completing setup need to leave a
// visitor signed in afterward.
func (a *Auth) establishSession(ctx context.Context, sm *Session, userID int64) error {
	if err := sm.sm.RenewToken(ctx); err != nil {
		return err
	}

	// Only the id goes into the session.
	sm.sm.Put(ctx, sessionUserKey, userID)

	return nil
}

// SetupRequired reports whether no account exists yet, so a caller can tell
// the one-time setup page from an ordinary sign-in.
func (a *Auth) SetupRequired(ctx context.Context) (bool, error) {
	if a.hasUsers.Load() {
		return false, nil
	}

	n, err := a.q.CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}

	if n > 0 {
		a.hasUsers.Store(true)
	}

	return n == 0, nil
}

// CreateFirstUser creates the one account setup exists to create, as admin,
// and signs it straight in -- there is no separate sign-in step to land on
// afterward the way there is for an account an admin creates for someone
// else. It goes through SetupRequired rather than reading hasUsers directly,
// so it stays the authoritative check even reached without the session
// middleware's own SetupRequired call having warmed the cache first.
func (a *Auth) CreateFirstUser(ctx context.Context, sm *Session, username, password string) (*models.User, error) {
	required, err := a.SetupRequired(ctx)
	if err != nil {
		return nil, err
	}

	if !required {
		return nil, ErrSetupComplete
	}

	user, err := a.createUser(ctx, username, password, dbtype.RoleAdmin)
	if err != nil {
		return nil, err
	}

	a.hasUsers.Store(true)

	if err := a.establishSession(ctx, sm, user.ID); err != nil {
		return nil, err
	}

	return user, nil
}

// CreateUser adds another account, for an admin already signed in to hand to
// someone else. It leaves the acting admin's own session untouched.
func (a *Auth) CreateUser(ctx context.Context, username, password string, role dbtype.UserRole) (*models.User, error) {
	return a.createUser(ctx, username, password, role)
}

// createUser looks the username up first rather than reading a constraint
// violation back off the insert, so a duplicate is told apart from every
// other way the write could fail the same way GetUserByUsername already
// tells a missing user apart from a lookup failure.
func (a *Auth) createUser(ctx context.Context, username, password string, role dbtype.UserRole) (*models.User, error) {
	switch _, err := a.q.GetUserByUsername(ctx, username); {
	case errors.Is(err, sql.ErrNoRows):
		// The one outcome that means the username is free.
	case err != nil:
		return nil, fmt.Errorf("check username %q: %w", username, err)
	default:
		return nil, ErrUsernameTaken
	}

	hash, err := a.hasher.Hash(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user, err := a.q.CreateUser(ctx, models.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		Role:         role,
	})
	if err != nil {
		return nil, fmt.Errorf("create user %q: %w", username, err)
	}

	return user, nil
}

// IsAdmin reports whether id names an admin account, for a route only an
// admin may reach.
func (a *Auth) IsAdmin(ctx context.Context, id int64) (bool, error) {
	user, err := a.q.GetUserByID(ctx, id)
	if err != nil {
		return false, fmt.Errorf("user %d: %w", id, err)
	}

	return user.Role == dbtype.RoleAdmin, nil
}

// ListUsers returns every account, newest first is not required here the way
// it is for tokens -- the settings page reads better oldest-first, in the
// order accounts were made.
func (a *Auth) ListUsers(ctx context.Context) ([]*models.User, error) {
	return a.q.ListUsers(ctx)
}
