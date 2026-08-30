package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB opens a database in a directory scoped to the test.
func newTestDB(t *testing.T) *DB {
	t.Helper()

	db, err := New(&Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)
	require.NotNil(t, db)

	t.Cleanup(func() { _ = db.Conn.Close() })

	return db
}

func TestNew(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	db, err := New(&Config{Path: dir, Name: "test.db"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Conn.Close() })

	require.NoError(t, db.Conn.Ping())
	assert.FileExists(t, filepath.Join(dir, "test.db"))
}

func TestNewFailsOnUnwritablePath(t *testing.T) {
	t.Parallel()

	db, err := New(&Config{Path: filepath.Join(t.TempDir(), "no-such-dir"), Name: "test.db"})

	require.Error(t, err)
	assert.Nil(t, db)
}

// TestNewRunsMigrations checks that New leaves the schema at dbVersion, with
// the tables the queries expect.
func TestNewRunsMigrations(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)

	var version int
	var dirty bool

	err := db.Conn.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	require.NoError(t, err)

	assert.Equal(t, dbVersion, version)
	assert.False(t, dirty, "migrations left the schema in a dirty state")

	var name string
	err = db.Conn.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&name)
	require.NoError(t, err, "users table was not created")
	assert.Equal(t, "users", name)
}

// TestNewIsIdempotent covers reopening an already-migrated database, which is
// what every restart after the first one does.
func TestNewIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &Config{Path: dir, Name: "test.db"}

	first, err := New(cfg)
	require.NoError(t, err)
	require.NoError(t, first.Conn.Close())

	second, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Conn.Close() })

	require.NoError(t, second.Conn.Ping())
}

func TestMigrateDBIsIdempotent(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)

	// New already migrated; running it again must be a no-op rather than an error.
	require.NoError(t, migrateDB(db))
}

// TestCreateUser exercises the generated queries against a real migrated
// schema, which is the only place the two are checked against each other.
func TestCreateUser(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	q := models.New(newTestDB(t).Conn)

	user, err := q.CreateUser(ctx, models.CreateUserParams{
		Username:     "ada",
		PasswordHash: "hash",
	})
	require.NoError(t, err)

	assert.NotZero(t, user.ID)
	assert.Equal(t, "ada", user.Username)
	assert.Equal(t, "hash", user.PasswordHash)
	assert.True(t, user.CreatedAt.Valid, "created_at default was not applied")
	assert.NotEmpty(t, user.CreatedAt.String)
}

func TestCreateUserRejectsDuplicateUsername(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	q := models.New(newTestDB(t).Conn)

	params := models.CreateUserParams{Username: "ada", PasswordHash: "hash"}

	_, err := q.CreateUser(ctx, params)
	require.NoError(t, err)

	_, err = q.CreateUser(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNIQUE constraint failed")
}

// TestNewAppliesPragmas checks the DSN pragmas actually reach the connection.
// foreign_keys is the one that matters: without it every REFERENCES clause in
// the schema is decorative.
func TestNewAppliesPragmas(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)

	var foreignKeys int
	require.NoError(t, db.Conn.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys))
	assert.Equal(t, 1, foreignKeys, "foreign key enforcement is off")

	var journalMode string
	require.NoError(t, db.Conn.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode))
	assert.Equal(t, "wal", strings.ToLower(journalMode))
}

// TestPragmasApplyToEveryPooledConnection is the reason the pragmas live in the
// DSN. A PRAGMA issued once after Open binds to whichever connection served it,
// leaving the rest of the pool unconfigured.
func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := newTestDB(t)

	first, err := db.Conn.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })

	// Held at the same time as the first, so the pool has to open a second one
	// rather than hand back the same connection.
	second, err := db.Conn.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	for i, conn := range []*sql.Conn{first, second} {
		var foreignKeys int
		require.NoError(t, conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys))
		assert.Equal(t, 1, foreignKeys, "foreign keys off on pooled connection %d", i)
	}
}

func TestDSNEscapesPath(t *testing.T) {
	t.Parallel()

	// Given a file path with characters that need escaping in a URL (like ? and #)
	file := "my_db?.sqlite"

	// When we generate the DSN
	result := dsn(file)

	// Then it should be properly escaped, preventing DSN injection
	assert.Contains(t, result, "my_db%3F.sqlite")
	// The pragmas should still be attached correctly as the raw query
	assert.Contains(t, result, "?_pragma=foreign_keys%281%29")
}

func BenchmarkDSN(b *testing.B) {
	for i := 0; i < b.N; i++ {
		dsn("test.db")
	}
}
