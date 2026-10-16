// Package auth verifies who asks. It covers password credentials, the setup of
// the first account, browser sessions, and API tokens. It also has the HTTP
// middleware that enforces each.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// loginBurst and loginRefill set the password attempts of each account: first
// loginBurst at once, then one more every loginRefill. This allows a few
// mistyped passwords. It still limits a guesser to one try each minute.
const (
	loginBurst  = 10
	loginRefill = time.Minute
)

// loginAddrBurst is the number of password attempts at once for each client
// address, across every account. It also refills at loginRefill. It is larger
// than loginBurst, because the people behind one NAT share an address. When
// it is used up, one address that tries many accounts gets one password each
// minute.
const loginAddrBurst = 30

// unknownUserKey is the loginAttempts key that every unknown username shares.
// No account has ID 0, because users.id is AUTOINCREMENT and starts from 1.
const unknownUserKey int64 = 0

type (
	// hasher is what Auth needs from its password hashing library.
	hasher interface {
		Hash(string) (string, error)
		Compare(string, string) error
	}

	// userManager is what Auth needs from the generated store for accounts:
	// credential checks, setup and admin management.
	userManager interface {
		GetUserByUsername(ctx context.Context, username string) (*models.User, error)
		GetUserByID(ctx context.Context, id int64) (*models.User, error)
		CreateUser(ctx context.Context, arg models.CreateUserParams) (*models.User, error)
		CreateFirstUser(ctx context.Context, arg models.CreateFirstUserParams) (*models.User, error)
		CountUsers(ctx context.Context) (int64, error)
		ListUsers(ctx context.Context) ([]*models.User, error)
	}

	// tokenManager is what Auth needs from the store to manage API tokens.
	tokenManager interface {
		CreateAPIToken(ctx context.Context, arg models.CreateAPITokenParams) (*models.ApiToken, error)
		TouchAPITokenByHash(ctx context.Context, arg models.TouchAPITokenByHashParams) (*models.ApiToken, error)
		ListAPITokensByUser(ctx context.Context, userID int64) ([]*models.ApiToken, error)
		DeleteAPIToken(ctx context.Context, arg models.DeleteAPITokenParams) error
	}

	// store is everything that Auth needs from the generated store: accounts,
	// API tokens and two-factor state.
	store interface {
		userManager
		tokenManager
		totpManager
	}
)

// Auth verifies a login attempt against stored credentials. It also makes and
// checks the API tokens that replace a login where there is no session.
type Auth struct {
	conn   *sql.DB
	store  store
	hasher hasher

	// txStore returns the store bound to tx. It is a field, so a test can
	// make a write in a transaction fail.
	txStore func(tx *sql.Tx) store

	// now is a field, so a test can fix the timestamps that it asserts on.
	now func() time.Time

	// Verify compares against unknownUserHash when the username is not found.
	// So that path costs the same derivation as a wrong password for a real
	// user. Without it, Compare would reject the missing hash of an unknown
	// user when it parses the format, long before a derivation. The timing
	// difference between the two paths would then tell whether the account
	// exists.
	unknownUserHash string

	// hasUsers caches whether an account exists. So SetupRequired costs a
	// query only until the first account is confirmed. Nothing in this
	// package removes the last account. So a true answer never gets old.
	hasUsers atomic.Bool

	// totpAttempts is the number of second-factor codes for each account. Its
	// key is the user ID. Auth writes it only after the password matched. So
	// it holds at most one entry for each account.
	totpAttempts *attemptLimiter[int64]

	// totpSteps is the last accepted TOTP step of each account. As for
	// totpAttempts, Auth writes it only after the password matched. So it
	// holds at most one entry for each account.
	totpSteps totpSteps

	// loginAttempts is the number of passwords for each account. Its key is
	// the user ID. Every unknown username shares unknownUserKey. So the map
	// holds at most one entry for each account, plus one. Also, a refusal for
	// a made-up name looks the same as a refusal for a real account.
	loginAttempts *attemptLimiter[int64]

	// loginAddrAttempts is the number of passwords for each client address,
	// across every account. Its key is addrKey. So every address in one IPv6
	// /64 shares one limit.
	loginAddrAttempts *attemptLimiter[netip.Prefix]
}

// Option configures New.
type Option func(*config)

type config struct {
	loginBurst     int
	loginAddrBurst int
}

// WithLoginAllowance sets how many passwords an account, and a client address,
// can try at once before Verify refuses them. The refill stays one each
// minute. New ignores a burst that is not positive, and keeps loginBurst and
// loginAddrBurst. Zero would lock out every account. This option exists for
// tests that sign in more often than a person.
func WithLoginAllowance(burst int) Option {
	return func(c *config) {
		if burst > 0 {
			c.loginBurst = burst
			c.loginAddrBurst = burst
		}
	}
}

// New builds an Auth over s, the store for conn. Writes that must go together
// run in a transaction on conn. New hashes the placeholder password once, at
// the start. Verify compares against it when the username is not found.
func New(conn *sql.DB, s store, hasher hasher, opts ...Option) (*Auth, error) {
	cfg := config{loginBurst: loginBurst, loginAddrBurst: loginAddrBurst}
	for _, opt := range opts {
		opt(&cfg)
	}

	unknownUserHash, err := hasher.Hash("no-such-user")
	if err != nil {
		return nil, fmt.Errorf("prepare unknown-user hash: %w", err)
	}

	return &Auth{
		conn:            conn,
		store:           s,
		hasher:          hasher,
		txStore:         func(tx *sql.Tx) store { return models.New(tx) },
		now:             time.Now,
		unknownUserHash: unknownUserHash,
		totpAttempts:    newAttemptLimiter[int64](totpBurst, totpRefill),
		loginAttempts:   newAttemptLimiter[int64](cfg.loginBurst, loginRefill),

		loginAddrAttempts: newAttemptLimiter[netip.Prefix](cfg.loginAddrBurst, loginRefill),
	}, nil
}

// Verify checks username and password, sent from the client at addr, against
// the stored credential. It returns the matching user only when both match.
//
// Each password, right or wrong, uses one attempt of addr (see loginAddrBurst)
// and one of the account (see loginBurst). If either has none left, Verify
// returns ErrLoginLocked, even for the right password. An addr that is not
// valid, as from a connection with no IP address, uses only an attempt of the
// account.
func (a *Auth) Verify(ctx context.Context, addr netip.Addr, username, password string) (*models.User, error) {
	user, err := a.store.GetUserByUsername(ctx, username)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		user = nil
	case err != nil:
		return nil, fmt.Errorf("user %q: %w", username, err)
	}

	key, hash := unknownUserKey, a.unknownUserHash
	if user != nil {
		key, hash = user.ID, user.PasswordHash
	}

	// Use the attempt before the compare. So passwords sent together cannot
	// all be checked before one of them is counted. The address goes first.
	// An address with nothing left uses nothing from the account. So a
	// refused address cannot use up the attempts of an account while its
	// owner signs in.
	now, allowed := a.now(), true
	if addr.IsValid() {
		allowed, _ = a.loginAddrAttempts.spend(addrKey(addr), now)
	}

	if allowed {
		allowed, _ = a.loginAttempts.spend(key, now)
	}

	// A refused attempt still does the compare. So its timing does not tell a
	// locked account from an open account.
	matched := a.hasher.Compare(password, hash) == nil

	switch {
	case !allowed:
		return nil, ErrLoginLocked
	case user == nil || !matched:
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

// addrKey returns the prefix under which Auth counts the attempts of addr. For
// IPv4, it is the address itself. For IPv6, it is its /64, because one host
// often gets a whole /64 and can send from any address in it.
func addrKey(addr netip.Addr) netip.Prefix {
	bits := 64
	if addr.Is4() {
		bits = 32
	}

	p, _ := addr.Prefix(bits)

	return p
}

// inTx runs fn against a store bound to one transaction. It commits only if fn
// returns nil.
func (a *Auth) inTx(ctx context.Context, fn func(q store) error) error {
	tx, err := a.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if err := fn(a.txStore(tx)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// LoginResult reports the outcome of a Login attempt. Exactly one of User and
// TOTPPending has a meaning.
type LoginResult struct {
	// User is set when Login makes a full session. This happens when the
	// account has 2FA off.
	User *models.User

	// TOTPPending is true when the password matched, but the account has 2FA
	// on. The caller then sends the visitor to the second-factor page.
	TOTPPending bool
}

// Login checks the credentials, sent from the client at addr, with
// [Auth.Verify]. It signs the visitor in, and renews the session token. For an
// account with 2FA on, it leaves the visitor signed out, and waits for
// [Auth.VerifyTOTP].
func (a *Auth) Login(
	ctx context.Context,
	sm *Session,
	addr netip.Addr,
	username, password string,
	rememberMe bool,
) (LoginResult, error) {
	user, err := a.Verify(ctx, addr, username, password)
	if err != nil {
		return LoginResult{}, err
	}

	// Set this before either branch. RememberMe writes into the stored record
	// of the session. This record stays through the Renew of a later 2FA
	// sign-in, and through the Renew of establishSession below. So a choice
	// made now still applies after the second factor succeeds.
	sm.s.RememberMe(ctx, rememberMe)

	if user.TOTPEnabled {
		if err := sm.s.Renew(ctx); err != nil {
			return LoginResult{}, err
		}

		sm.s.Update(ctx, func(d *Data) {
			d.PendingUserID = user.ID
			d.PendingUsername = user.Username
		})

		return LoginResult{TOTPPending: true}, nil
	}

	if err := a.establishSession(ctx, sm, user); err != nil {
		return LoginResult{}, err
	}

	return LoginResult{User: user}, nil
}

// establishSession renews the session token. So a token from the anonymous
// session cannot go into the authenticated session. Then it records the owner
// of the session. Both sign-in and the end of setup need exactly this sequence
// to sign a visitor in. It also clears pending-2FA state. So a session that
// comes through VerifyTOTP has no old pending fields.
func (a *Auth) establishSession(ctx context.Context, sm *Session, user *models.User) error {
	if err := sm.s.Renew(ctx); err != nil {
		return err
	}

	sm.s.Update(ctx, func(d *Data) {
		d.UserID = user.ID
		d.Username = user.Username
		d.Role = user.Role
		d.PendingUserID = 0
		d.PendingUsername = ""
	})

	return nil
}

// SetupRequired reports whether no account exists yet. So a caller can tell
// the one-time setup page from a normal sign-in.
func (a *Auth) SetupRequired(ctx context.Context) (bool, error) {
	if a.hasUsers.Load() {
		return false, nil
	}

	n, err := a.store.CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}

	if n > 0 {
		a.hasUsers.Store(true)
	}

	return n == 0, nil
}

// CreateFirstUser creates the one account that setup exists for, as admin, and
// signs it in immediately. It returns ErrSetupComplete when an account exists.
// This includes an account that a different setup request made a moment
// before.
func (a *Auth) CreateFirstUser(ctx context.Context, sm *Session, username, password string) (*models.User, error) {
	// A cheap refusal before the slow hash. If several setup requests pass
	// this check together, the insert below decides between them.
	required, err := a.SetupRequired(ctx)
	if err != nil {
		return nil, err
	}

	if !required {
		return nil, ErrSetupComplete
	}

	hash, err := a.hasher.Hash(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user, err := a.store.CreateFirstUser(ctx, models.CreateFirstUserParams{
		Username:     username,
		PasswordHash: hash,
	})

	switch {
	case errors.Is(err, sql.ErrNoRows):
		a.hasUsers.Store(true)
		return nil, ErrSetupComplete
	case err != nil:
		return nil, fmt.Errorf("create first user %q: %w", username, err)
	}

	a.hasUsers.Store(true)

	if err := a.establishSession(ctx, sm, user); err != nil {
		return nil, err
	}

	return user, nil
}

// CreateUser adds another account, which a signed-in admin gives to a
// different person. It does not change the session of the admin. An instance
// has one admin. So it refuses a second admin with ErrSecondAdmin.
func (a *Auth) CreateUser(ctx context.Context, username, password string, role dbtype.UserRole) (*models.User, error) {
	if role == dbtype.RoleAdmin {
		users, err := a.store.ListUsers(ctx)
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}

		for _, u := range users {
			if u.Role == dbtype.RoleAdmin {
				return nil, ErrSecondAdmin
			}
		}
	}

	return a.createUser(ctx, username, password, role)
}

// createUser looks up the username before the insert. So it can tell a
// duplicate from all other write failures. GetUserByUsername also tells a
// missing user from a lookup failure.
func (a *Auth) createUser(ctx context.Context, username, password string, role dbtype.UserRole) (*models.User, error) {
	switch _, err := a.store.GetUserByUsername(ctx, username); {
	case errors.Is(err, sql.ErrNoRows):
		// Only this outcome means that the username is free.
	case err != nil:
		return nil, fmt.Errorf("check username %q: %w", username, err)
	default:
		return nil, ErrUsernameTaken
	}

	hash, err := a.hasher.Hash(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user, err := a.store.CreateUser(ctx, models.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		Role:         role,
	})
	if err != nil {
		return nil, fmt.Errorf("create user %q: %w", username, err)
	}

	return user, nil
}

// ListUsers returns every account, oldest first, in the order of creation.
func (a *Auth) ListUsers(ctx context.Context) ([]*models.User, error) {
	return a.store.ListUsers(ctx)
}
