package auth

import (
	"context"
	"database/sql"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQueries answers the store interface from memory, so Auth's logic can be
// tested without a database. Tokens are keyed by hash, as the real table is
// looked up. A real store is safe to call from parallel subtests, so the
// mutex guards every access to make this one behave the same.
type fakeQueries struct {
	mu            sync.Mutex
	users         map[string]*models.User
	tokens        map[string]*models.ApiToken
	recoveryCodes map[string]*models.UserRecoveryCode
	nextID        int64
}

func (f *fakeQueries) GetUserByUsername(_ context.Context, username string) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.users[username]
	if !ok {
		return nil, sql.ErrNoRows
	}

	return u, nil
}

func (f *fakeQueries) GetUserByID(_ context.Context, id int64) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}

	return nil, sql.ErrNoRows
}

func (f *fakeQueries) CreateUser(_ context.Context, arg models.CreateUserParams) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nextID++

	u := &models.User{
		ID:           f.nextID,
		Username:     arg.Username,
		PasswordHash: arg.PasswordHash,
		Role:         arg.Role,
		CreatedAt:    dbtype.NewTime(time.Now()),
	}

	if f.users == nil {
		f.users = map[string]*models.User{}
	}

	f.users[arg.Username] = u

	return u, nil
}

func (f *fakeQueries) CreateFirstUser(_ context.Context, arg models.CreateFirstUserParams) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.users) > 0 {
		return nil, sql.ErrNoRows
	}

	f.nextID++

	u := &models.User{
		ID:           f.nextID,
		Username:     arg.Username,
		PasswordHash: arg.PasswordHash,
		Role:         dbtype.RoleAdmin,
		CreatedAt:    dbtype.NewTime(time.Now()),
	}

	f.users = map[string]*models.User{arg.Username: u}

	return u, nil
}

func (f *fakeQueries) CountUsers(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return int64(len(f.users)), nil
}

func (f *fakeQueries) ListUsers(_ context.Context) ([]*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*models.User, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}

	return out, nil
}

func (f *fakeQueries) CreateAPIToken(_ context.Context, arg models.CreateAPITokenParams) (*models.ApiToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nextID++

	t := &models.ApiToken{
		ID:        f.nextID,
		UserID:    arg.UserID,
		Name:      arg.Name,
		TokenHash: arg.TokenHash,
		Scope:     arg.Scope,
		CreatedAt: dbtype.NewTime(time.Now()),
		ExpiresAt: arg.ExpiresAt,
	}

	if f.tokens == nil {
		f.tokens = map[string]*models.ApiToken{}
	}

	f.tokens[arg.TokenHash] = t

	return t, nil
}

func (f *fakeQueries) ListAPITokensByUser(_ context.Context, userID int64) ([]*models.ApiToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []*models.ApiToken

	for _, t := range f.tokens {
		if t.UserID == userID {
			out = append(out, t)
		}
	}

	return out, nil
}

func (f *fakeQueries) TouchAPITokenByHash(_ context.Context, arg models.TouchAPITokenByHashParams) (*models.ApiToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	t, ok := f.tokens[arg.TokenHash]
	if !ok || (t.ExpiresAt.Valid && !t.ExpiresAt.Time.After(arg.Now.Time.Time)) {
		return nil, sql.ErrNoRows
	}

	t.LastUsedAt = arg.Now

	return t, nil
}

// DeleteAPIToken matches the real query's :exec semantics: a WHERE that names
// no row succeeds, as SQL's DELETE affecting zero rows does.
func (f *fakeQueries) DeleteAPIToken(_ context.Context, arg models.DeleteAPITokenParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for hash, t := range f.tokens {
		if t.ID == arg.ID && t.UserID == arg.UserID {
			delete(f.tokens, hash)
			break
		}
	}

	return nil
}

func (f *fakeQueries) SetUserTOTPSecret(_ context.Context, arg models.SetUserTOTPSecretParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, u := range f.users {
		if u.ID == arg.ID && !u.TOTPEnabled {
			u.TOTPSecret = arg.TOTPSecret
			return 1, nil
		}
	}

	return 0, nil
}

func (f *fakeQueries) EnableUserTOTP(_ context.Context, arg models.EnableUserTOTPParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, u := range f.users {
		if u.ID == arg.ID && !u.TOTPEnabled {
			u.TOTPEnabled = true
			u.TOTPConfirmedAt = arg.TOTPConfirmedAt

			return 1, nil
		}
	}

	return 0, nil
}

func (f *fakeQueries) DisableUserTOTP(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, u := range f.users {
		if u.ID == id {
			u.TOTPSecret = sql.NullString{}
			u.TOTPEnabled = false
			u.TOTPConfirmedAt = dbtype.NullTime{}

			return nil
		}
	}

	return sql.ErrNoRows
}

func (f *fakeQueries) CreateRecoveryCode(_ context.Context, arg models.CreateRecoveryCodeParams) (*models.UserRecoveryCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.nextID++

	c := &models.UserRecoveryCode{
		ID:        f.nextID,
		UserID:    arg.UserID,
		CodeHash:  arg.CodeHash,
		CreatedAt: dbtype.NewTime(time.Now()),
	}

	if f.recoveryCodes == nil {
		f.recoveryCodes = map[string]*models.UserRecoveryCode{}
	}

	f.recoveryCodes[arg.CodeHash] = c

	return c, nil
}

func (f *fakeQueries) DeleteRecoveryCodesByUser(_ context.Context, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for hash, c := range f.recoveryCodes {
		if c.UserID == userID {
			delete(f.recoveryCodes, hash)
		}
	}

	return nil
}

func (f *fakeQueries) RedeemRecoveryCode(_ context.Context, arg models.RedeemRecoveryCodeParams) (*models.UserRecoveryCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, ok := f.recoveryCodes[arg.CodeHash]
	if !ok || c.UserID != arg.UserID || c.UsedAt.Valid {
		return nil, sql.ErrNoRows
	}

	c.UsedAt = arg.UsedAt

	return c, nil
}

func (f *fakeQueries) CountUnusedRecoveryCodesByUser(_ context.Context, userID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var n int64

	for _, c := range f.recoveryCodes {
		if c.UserID == userID && !c.UsedAt.Valid {
			n++
		}
	}

	return n, nil
}

// testAddr is the client address the tests sign in from, unless a test is
// about addresses.
var testAddr = netip.MustParseAddr("192.0.2.1")

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newTestAuth(t *testing.T, users map[string]*models.User) *Auth {
	t.Helper()

	a, err := New(nil, &fakeQueries{users: users}, password.NewHasher())
	require.NoError(t, err)

	return a
}

func hashOf(t *testing.T, plain string) string {
	t.Helper()

	hash, err := password.NewHasher().Hash(plain)
	require.NoError(t, err)

	return hash
}

func TestVerify(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{
		"ada": {ID: 1, Username: "ada", PasswordHash: hashOf(t, "correct-password")},
	})

	t.Run("matching username and password", func(t *testing.T) {
		t.Parallel()

		user, err := a.Verify(t.Context(), testAddr, "ada", "correct-password")

		require.NoError(t, err)
		assert.Equal(t, int64(1), user.ID)
	})

	t.Run("known username, wrong password", func(t *testing.T) {
		t.Parallel()

		_, err := a.Verify(t.Context(), testAddr, "ada", "wrong-password")

		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("unknown username", func(t *testing.T) {
		t.Parallel()

		_, err := a.Verify(t.Context(), testAddr, "nobody", "whatever")

		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})
}

// TestVerifyLimitRefills covers the password allowance over time: ten
// attempts at once, then one more each time the refill interval passes. The
// clock is Auth's own, moved by hand.
func TestVerifyLimitRefills(t *testing.T) {
	t.Parallel()

	for _, username := range []string{"ada", "nobody"} {
		t.Run(username, func(t *testing.T) {
			t.Parallel()

			a := newTestAuth(t, map[string]*models.User{
				"ada": {ID: 1, Username: "ada", PasswordHash: hashOf(t, "correct-password")},
			})

			now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			a.now = func() time.Time { return now }

			for range loginBurst {
				_, err := a.Verify(t.Context(), testAddr, username, "wrong-password")
				require.ErrorIs(t, err, ErrInvalidCredentials)
			}

			_, err := a.Verify(t.Context(), testAddr, username, "wrong-password")
			require.ErrorIs(t, err, ErrLoginLocked, "the eleventh attempt in a row is refused")

			_, err = a.Verify(t.Context(), testAddr, username, "correct-password")
			require.ErrorIs(t, err, ErrLoginLocked, "a correct password waits for the refill too")

			now = now.Add(loginRefill - time.Second)

			_, err = a.Verify(t.Context(), testAddr, username, "wrong-password")
			require.ErrorIs(t, err, ErrLoginLocked)

			now = now.Add(time.Second)

			_, err = a.Verify(t.Context(), testAddr, username, "wrong-password")
			require.ErrorIs(t, err, ErrInvalidCredentials, "one attempt is back after the refill interval")

			_, err = a.Verify(t.Context(), testAddr, username, "wrong-password")
			assert.ErrorIs(t, err, ErrLoginLocked, "and only one")
		})
	}
}

// TestVerifyLimitIsPerAccount covers one account running out of attempts while
// another, and every unknown username together, keep their own.
func TestVerifyLimitIsPerAccount(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{
		"ada":   {ID: 1, Username: "ada", PasswordHash: hashOf(t, "correct-password")},
		"grace": {ID: 2, Username: "grace", PasswordHash: hashOf(t, "correct-password")},
	})

	for range loginBurst {
		_, err := a.Verify(t.Context(), testAddr, "ada", "wrong-password")
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}

	_, err := a.Verify(t.Context(), testAddr, "ada", "correct-password")
	require.ErrorIs(t, err, ErrLoginLocked)

	user, err := a.Verify(t.Context(), testAddr, "grace", "correct-password")
	require.NoError(t, err)
	assert.Equal(t, int64(2), user.ID)

	_, err = a.Verify(t.Context(), testAddr, "nobody", "wrong-password")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

// TestVerifyLimitIsPerAddress covers one address running out of attempts
// across several accounts, each still inside its own allowance, while another
// address keeps its own. The attempts an address is refused spend nothing
// from the accounts it names.
func TestVerifyLimitIsPerAddress(t *testing.T) {
	t.Parallel()

	users := map[string]*models.User{}
	names := []string{"ada", "grace", "alan", "linus"}

	for i, name := range append(names, "edsger") {
		users[name] = &models.User{ID: int64(i + 1), Username: name, PasswordHash: hashOf(t, "correct-password")}
	}

	a := newTestAuth(t, users)
	from := netip.MustParseAddr("198.51.100.7")
	other := netip.MustParseAddr("198.51.100.8")

	// 30 spread over four accounts is 8, 8, 7 and 7: each under loginBurst.
	for i := range loginAddrBurst {
		_, err := a.Verify(t.Context(), from, names[i%len(names)], "wrong-password")
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}

	_, err := a.Verify(t.Context(), from, "edsger", "correct-password")
	require.ErrorIs(t, err, ErrLoginLocked, "the address has none left, whichever account it names")

	for range loginBurst {
		_, err = a.Verify(t.Context(), from, "ada", "wrong-password")
		require.ErrorIs(t, err, ErrLoginLocked)
	}

	user, err := a.Verify(t.Context(), other, "ada", "correct-password")
	require.NoError(t, err, "ada still has the attempts the refused address could not spend")
	assert.Equal(t, int64(1), user.ID)
}

// TestVerifyLimitGroupsIPv6ByNetwork covers the addresses of one IPv6 /64
// sharing an allowance, and a neighbouring /64 keeping its own. Without the
// grouping, one host could take a fresh allowance from every address in its
// network.
func TestVerifyLimitGroupsIPv6ByNetwork(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{
		"ada": {ID: 1, Username: "ada", PasswordHash: hashOf(t, "correct-password")},
	})

	// Unknown usernames share one account allowance, which would run out
	// before the address's. Raising it keeps this test about addresses.
	a.loginAttempts = newAttemptLimiter[int64](loginAddrBurst+1, loginRefill)

	for i := range loginAddrBurst {
		from := netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 15: byte(i + 1)})
		_, err := a.Verify(t.Context(), from, "nobody", "wrong-password")
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}

	_, err := a.Verify(t.Context(), netip.MustParseAddr("2001:db8::ffff"), "ada", "correct-password")
	require.ErrorIs(t, err, ErrLoginLocked, "another address in the same /64")

	_, err = a.Verify(t.Context(), netip.MustParseAddr("2001:db8:0:1::1"), "ada", "correct-password")
	assert.NoError(t, err, "an address in the next /64")
}

// TestVerifyWithoutAddress covers a connection with no IP address: only the
// account's allowance applies.
func TestVerifyWithoutAddress(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{
		"ada": {ID: 1, Username: "ada", PasswordHash: hashOf(t, "correct-password")},
	})

	user, err := a.Verify(t.Context(), netip.Addr{}, "ada", "correct-password")
	require.NoError(t, err)
	assert.Equal(t, int64(1), user.ID)
	assert.Empty(t, a.loginAddrAttempts.keys)
}

// TestWithLoginAllowance covers a raised allowance taking the place of the
// default, and a non-positive one leaving the default in place.
func TestWithLoginAllowance(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		burst int
		want  int
	}{
		{name: "raised", burst: loginBurst + 5, want: loginBurst + 5},
		{name: "zero keeps the default", burst: 0, want: loginBurst},
		{name: "negative keeps the default", burst: -1, want: loginBurst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := New(nil, &fakeQueries{}, password.NewHasher(), WithLoginAllowance(tc.burst))
			require.NoError(t, err)

			for range tc.want {
				_, err := a.Verify(t.Context(), testAddr, "nobody", "wrong-password")
				require.ErrorIs(t, err, ErrInvalidCredentials)
			}

			_, err = a.Verify(t.Context(), testAddr, "nobody", "wrong-password")
			assert.ErrorIs(t, err, ErrLoginLocked)
		})
	}
}

// New hashes its placeholder password once, up front, so the first
// unknown-user login does not pay for it.
func TestNewPrecomputesUnknownUserHash(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	assert.NotEmpty(t, a.unknownUserHash)
}

func TestLogin(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{
		"ada": {ID: 42, Username: "ada", PasswordHash: hashOf(t, "correct-password"), Role: dbtype.RoleReadWrite},
	})

	t.Run("matching credentials populate the session", func(t *testing.T) {
		t.Parallel()

		sm := NewSession(testLogger())

		ctx, err := sm.Load(t.Context(), "")
		require.NoError(t, err)

		result, err := a.Login(ctx, sm, testAddr, "ada", "correct-password", false)
		require.NoError(t, err)
		require.False(t, result.TOTPPending)
		assert.Equal(t, int64(42), result.User.ID)

		id, ok := sm.CurrentUserID(ctx)
		require.True(t, ok)
		assert.Equal(t, int64(42), id)

		assert.Equal(t, dbtype.RoleReadWrite, sm.CurrentRole(ctx), "the account's role rides in on login")
	})

	t.Run("wrong credentials leave no session", func(t *testing.T) {
		t.Parallel()

		sm := NewSession(testLogger())

		ctx, err := sm.Load(t.Context(), "")
		require.NoError(t, err)

		_, err = a.Login(ctx, sm, testAddr, "ada", "wrong-password", false)
		require.ErrorIs(t, err, ErrInvalidCredentials)

		_, ok := sm.CurrentUserID(ctx)
		assert.False(t, ok)
	})
}

func TestSetupRequired(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	required, err := a.SetupRequired(t.Context())
	require.NoError(t, err)
	assert.True(t, required, "an account-less store still needs setup")

	sm := NewSession(testLogger())
	ctx, err := sm.Load(t.Context(), "")
	require.NoError(t, err)

	_, err = a.CreateFirstUser(ctx, sm, "ada", "correct-password")
	require.NoError(t, err)

	required, err = a.SetupRequired(t.Context())
	require.NoError(t, err)
	assert.False(t, required, "an account now exists")
}

func TestCreateFirstUser(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)
	sm := NewSession(testLogger())

	ctx, err := sm.Load(t.Context(), "")
	require.NoError(t, err)

	user, err := a.CreateFirstUser(ctx, sm, "ada", "correct-password")
	require.NoError(t, err)
	assert.Equal(t, dbtype.RoleAdmin, user.Role, "the first account is always admin")

	id, ok := sm.CurrentUserID(ctx)
	require.True(t, ok, "CreateFirstUser signs the new account straight in")
	assert.Equal(t, user.ID, id)
	assert.Equal(t, dbtype.RoleAdmin, sm.CurrentRole(ctx), "and the session carries the admin role")

	_, err = a.CreateFirstUser(ctx, sm, "someone-else", "another-password")
	assert.ErrorIs(t, err, ErrSetupComplete, "setup is one-time, not reachable a second time")
}

func TestCreateUser(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{
		"ada": {ID: 1, Username: "ada", PasswordHash: hashOf(t, "correct-password"), Role: dbtype.RoleAdmin},
	})

	t.Run("adds an account under the given role", func(t *testing.T) {
		t.Parallel()

		user, err := a.CreateUser(t.Context(), "grace", "another-password", dbtype.RoleRead)
		require.NoError(t, err)
		assert.Equal(t, dbtype.RoleRead, user.Role)
	})

	t.Run("rejects a username already taken", func(t *testing.T) {
		t.Parallel()

		_, err := a.CreateUser(t.Context(), "ada", "another-password", dbtype.RoleRead)
		assert.ErrorIs(t, err, ErrUsernameTaken)
	})

	t.Run("refuses a second admin", func(t *testing.T) {
		t.Parallel()

		_, err := a.CreateUser(t.Context(), "linus", "another-password", dbtype.RoleAdmin)
		assert.ErrorIs(t, err, ErrSecondAdmin)
	})
}
