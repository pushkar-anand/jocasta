// Package auth verifies who is asking: password credentials and first-account
// setup, browser sessions, and API tokens, with the HTTP middleware that
// enforces each.
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

// loginBurst and loginRefill set each account's allowance of password
// attempts: loginBurst at once, then one more every loginRefill. That leaves
// room for a few mistyped passwords and still holds a guesser to one try a
// minute.
const (
	loginBurst  = 10
	loginRefill = time.Minute
)

// loginAddrBurst is each client address's allowance of password attempts at
// once, across every account; it refills at loginRefill too. It is larger
// than loginBurst because the people behind one NAT share an address, and it
// still holds one address trying many accounts to one password a minute.
const loginAddrBurst = 30

// unknownUserKey is the loginAttempts key every unknown username shares. No
// account has ID 0, since users.id is AUTOINCREMENT and starts from 1.
const unknownUserKey int64 = 0

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

	// store is everything Auth needs from the generated store: accounts, API
	// tokens and two-factor state.
	store interface {
		userManager
		tokenManager
		totpManager
	}
)

// Auth verifies a login attempt against stored credentials, and issues and
// checks the API tokens that stand in for one where there is no session to
// carry.
type Auth struct {
	conn   *sql.DB
	store  store
	hasher hasher

	// txStore returns the store bound to tx. It is a field so a test can make
	// a write inside a transaction fail.
	txStore func(tx *sql.Tx) store

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
	// a query only until the first one is confirmed. Nothing in this package
	// removes the last account, so a true answer never goes stale.
	hasUsers atomic.Bool

	// totpAttempts is each account's allowance of second-factor codes. It is
	// keyed by user ID, and only after the password matched, so it holds at
	// most one entry per account.
	totpAttempts *attemptLimiter[int64]

	// totpSteps is each account's last accepted TOTP step. Like totpAttempts
	// it is written only after the password matched, so it holds at most one
	// entry per account.
	totpSteps totpSteps

	// loginAttempts is each account's allowance of passwords, keyed by user
	// ID. Every unknown username shares unknownUserKey, so the map holds at
	// most one entry per account plus one, and a refusal for a made-up name
	// looks the same as one for a real account.
	loginAttempts *attemptLimiter[int64]

	// loginAddrAttempts is each client address's allowance of passwords,
	// across every account. It is keyed by addrKey, so the many addresses of
	// one IPv6 network share an allowance.
	loginAddrAttempts *attemptLimiter[netip.Prefix]
}

// Option configures New.
type Option func(*config)

type config struct {
	loginBurst     int
	loginAddrBurst int
}

// WithLoginAllowance sets how many passwords an account, and a client address,
// may try at once before Verify starts refusing them; the refill stays one a
// minute. A non-positive burst is ignored, leaving the defaults of 10 and 30
// in place: zero would lock every account out. It exists for tests that sign
// in more often than a person does.
func WithLoginAllowance(burst int) Option {
	return func(c *config) {
		if burst > 0 {
			c.loginBurst = burst
			c.loginAddrBurst = burst
		}
	}
}

// New builds an Auth over s, the store for conn. Writes that have to land
// together run in a transaction on conn. New hashes the placeholder password
// Verify compares against on a username miss once, up front.
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
// the stored credential and returns the matching user only once both hold.
//
// Each password, right or wrong, spends one of addr's attempts (see
// loginAddrBurst) and one of the account's (see loginBurst). With none left in
// either, Verify returns ErrLoginLocked, even for the right password. An addr
// that is not valid, as from a connection with no IP address, spends only the
// account's.
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

	// Spent before the compare, so passwords sent together cannot all be
	// checked before any of them is counted. The address goes first, and one
	// with nothing left spends nothing from the account, so a refused address
	// cannot keep draining an account its owner is about to sign in to.
	now, allowed := a.now(), true
	if addr.IsValid() {
		allowed, _ = a.loginAddrAttempts.spend(addrKey(addr), now)
	}

	if allowed {
		allowed, _ = a.loginAttempts.spend(key, now)
	}

	// A refused attempt still pays for the compare, so its timing does not
	// tell a locked account from an open one.
	matched := a.hasher.Compare(password, hash) == nil

	switch {
	case !allowed:
		return nil, ErrLoginLocked
	case user == nil || !matched:
		return nil, ErrInvalidCredentials
	}

	return user, nil
}

// addrKey returns the prefix addr's attempts are counted under: the address
// itself for IPv4, and its /64 for IPv6, since one host is routinely handed a
// whole /64 and can send from any address in it.
func addrKey(addr netip.Addr) netip.Prefix {
	bits := 64
	if addr.Is4() {
		bits = 32
	}

	p, _ := addr.WithZone("").Prefix(bits)

	return p
}

// inTx runs fn against a store bound to one transaction, and commits only if
// fn returns nil.
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

// LoginResult reports how a Login attempt landed. Exactly one of User and
// TOTPPending is meaningful.
type LoginResult struct {
	// User is set when a full session is established, which happens when the
	// account has 2FA off.
	User *models.User

	// TOTPPending is true when the password matched but the account has 2FA
	// enabled: the caller sends the visitor to the second-factor page.
	TOTPPending bool
}

// Login checks the credentials, sent from the client at addr, with
// [Auth.Verify] and signs the visitor in, renewing the session token. For an
// account with 2FA enabled it leaves the visitor signed out and pending on
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

	// Set before either branch. RememberMe writes into the session's own
	// persisted record, which survives the Renew a completed 2FA sign-in does
	// later as well as establishSession's Renew below, so a choice made now
	// still holds once the second factor succeeds.
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

// establishSession renews the session token, so a token held while anonymous
// cannot carry over into the authenticated session, then records who the
// session belongs to. Both signing in and completing setup need this exact
// sequence to leave a visitor signed in. It also clears any pending-2FA state,
// so a session that arrives by way of VerifyTOTP carries no stale pending
// fields.
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

// SetupRequired reports whether no account exists yet, so a caller can tell
// the one-time setup page from an ordinary sign-in.
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

// CreateFirstUser creates the one account setup exists to create, as admin,
// and signs it straight in. It returns ErrSetupComplete once any account
// exists, including one that another setup request made a moment earlier.
func (a *Auth) CreateFirstUser(ctx context.Context, sm *Session, username, password string) (*models.User, error) {
	// A cheap refusal before the slow hash. The insert below is what
	// decides between setup requests that pass this together.
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

// CreateUser adds another account, for an admin already signed in to hand to
// someone else. It leaves the acting admin's own session untouched. An
// instance has one admin, so it refuses to make a second with
// ErrSecondAdmin.
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

// createUser looks the username up before the insert, so a duplicate is told
// apart from every other way the write could fail, just as GetUserByUsername
// tells a missing user apart from a lookup failure.
func (a *Auth) createUser(ctx context.Context, username, password string, role dbtype.UserRole) (*models.User, error) {
	switch _, err := a.store.GetUserByUsername(ctx, username); {
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

// ListUsers returns every account, oldest first, in the order the accounts
// were made.
func (a *Auth) ListUsers(ctx context.Context) ([]*models.User, error) {
	return a.store.ListUsers(ctx)
}
