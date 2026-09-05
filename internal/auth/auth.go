package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/models"
)

type (
	// hasher is what Auth needs from the password hashing library it uses.
	hasher interface {
		Hash(string) (string, error)
		Compare(string, string) error
	}

	// userProvider is what Auth needs from the generated store to verify user credentials.
	userProvider interface {
		GetUserByUsername(ctx context.Context, username string) (*models.User, error)
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
		userProvider
		tokenManager
	}
)

// Auth verifies a login attempt against stored credentials, and issues and
// checks the API tokens that stand in for one where there is no session to
// carry.
type Auth struct {
	q      userProvider
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

	if err := sm.sm.RenewToken(ctx); err != nil {
		return nil, err
	}

	if rememberMe {
		sm.sm.RememberMe(ctx, true)
	}

	// Only the id goes into the session
	sm.sm.Put(ctx, sessionUserKey, user.ID)

	return user, nil
}
