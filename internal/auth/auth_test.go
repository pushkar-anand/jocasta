package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/pushkar-anand/build-with-go/security/password"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQueries answers the store interface from memory, so Auth's logic can be
// tested without a database. Tokens are keyed by hash, the way the real table
// is looked up by.
type fakeQueries struct {
	users  map[string]*models.User
	tokens map[string]*models.ApiToken
	nextID int64
}

func (f *fakeQueries) GetUserByUsername(_ context.Context, username string) (*models.User, error) {
	u, ok := f.users[username]
	if !ok {
		return nil, sql.ErrNoRows
	}

	return u, nil
}

func (f *fakeQueries) CreateAPIToken(_ context.Context, arg models.CreateAPITokenParams) (*models.ApiToken, error) {
	f.nextID++

	t := &models.ApiToken{
		ID:        f.nextID,
		UserID:    arg.UserID,
		Name:      arg.Name,
		TokenHash: arg.TokenHash,
		Scope:     arg.Scope,
		CreatedAt: dbtype.NewTime(time.Now()),
	}

	if f.tokens == nil {
		f.tokens = map[string]*models.ApiToken{}
	}

	f.tokens[arg.TokenHash] = t

	return t, nil
}

func (f *fakeQueries) ListAPITokensByUser(_ context.Context, userID int64) ([]*models.ApiToken, error) {
	var out []*models.ApiToken

	for _, t := range f.tokens {
		if t.UserID == userID {
			out = append(out, t)
		}
	}

	return out, nil
}

func (f *fakeQueries) TouchAPITokenByHash(_ context.Context, arg models.TouchAPITokenByHashParams) (*models.ApiToken, error) {
	t, ok := f.tokens[arg.TokenHash]
	if !ok {
		return nil, sql.ErrNoRows
	}

	t.LastUsedAt = arg.LastUsedAt

	return t, nil
}

// DeleteAPIToken matches the real query's :exec semantics: a WHERE that names
// no row is not an error, the same as SQL's DELETE affecting zero rows.
func (f *fakeQueries) DeleteAPIToken(_ context.Context, arg models.DeleteAPITokenParams) error {
	for hash, t := range f.tokens {
		if t.ID == arg.ID && t.UserID == arg.UserID {
			delete(f.tokens, hash)
			break
		}
	}

	return nil
}

func newTestAuth(t *testing.T, users map[string]*models.User) *Auth {
	t.Helper()

	a, err := New(&fakeQueries{users: users}, password.NewHasher())
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

		user, err := a.Verify(t.Context(), "ada", "correct-password")

		require.NoError(t, err)
		assert.Equal(t, int64(1), user.ID)
	})

	t.Run("known username, wrong password", func(t *testing.T) {
		t.Parallel()

		_, err := a.Verify(t.Context(), "ada", "wrong-password")

		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("unknown username", func(t *testing.T) {
		t.Parallel()

		_, err := a.Verify(t.Context(), "nobody", "whatever")

		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})
}

// New hashes its placeholder password once, up front, rather than Verify
// doing it lazily on the first miss -- so the first unknown-user login isn't
// the one request that pays for it.
func TestNewPrecomputesUnknownUserHash(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, nil)

	assert.NotEmpty(t, a.unknownUserHash)
}

func TestLogin(t *testing.T) {
	t.Parallel()

	a := newTestAuth(t, map[string]*models.User{
		"ada": {ID: 42, Username: "ada", PasswordHash: hashOf(t, "correct-password")},
	})

	t.Run("matching credentials populate the session", func(t *testing.T) {
		t.Parallel()

		sm := NewSession()

		ctx, err := sm.Load(t.Context(), "")
		require.NoError(t, err)

		user, err := a.Login(ctx, sm, "ada", "correct-password", false)
		require.NoError(t, err)
		assert.Equal(t, int64(42), user.ID)

		id, ok := sm.CurrentUserID(ctx)
		require.True(t, ok)
		assert.Equal(t, int64(42), id)
	})

	t.Run("wrong credentials leave no session", func(t *testing.T) {
		t.Parallel()

		sm := NewSession()

		ctx, err := sm.Load(t.Context(), "")
		require.NoError(t, err)

		_, err = a.Login(ctx, sm, "ada", "wrong-password", false)
		require.ErrorIs(t, err, ErrInvalidCredentials)

		_, ok := sm.CurrentUserID(ctx)
		assert.False(t, ok)
	})
}
