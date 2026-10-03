package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB opens a database in a directory scoped to the test.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := New(&Config{Path: t.TempDir(), Name: "test.db"})
	require.NoError(t, err)
	require.NotNil(t, db)

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func TestNew(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	db, err := New(&Config{Path: dir, Name: "test.db"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.PingContext(t.Context()))
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

	var (
		version int
		dirty   bool
	)

	err := db.QueryRowContext(t.Context(), `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	require.NoError(t, err)

	assert.Equal(t, dbVersion, version)
	assert.False(t, dirty, "migrations left the schema in a dirty state")

	for _, table := range []string{
		"users", "sources", "networks", "devices", "addresses", "scans", "events", "device_ports", "sessions",
	} {
		var name string

		err = db.QueryRowContext(t.Context(),
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		require.NoErrorf(t, err, "%s table was not created", table)
		assert.Equal(t, table, name)
	}
}

// TestNewIsIdempotent covers reopening an already-migrated database, which is
// what every restart after the first one does.
func TestNewIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &Config{Path: dir, Name: "test.db"}

	first, err := New(cfg)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	require.NoError(t, second.PingContext(t.Context()))
}

func TestMigrateDBIsIdempotent(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)

	// New already migrated; running it again must be a no-op.
	require.NoError(t, migrateDB(db))
}

// TestCreateUser exercises the generated queries against a real migrated
// schema, which is the only place the two are checked against each other.
func TestCreateUser(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	q := models.New(newTestDB(t))

	user, err := q.CreateUser(ctx, models.CreateUserParams{
		Username:     "ada",
		PasswordHash: "hash",
		Role:         dbtype.RoleAdmin,
	})
	require.NoError(t, err)

	assert.NotZero(t, user.ID)
	assert.Equal(t, "ada", user.Username)
	assert.Equal(t, "hash", user.PasswordHash)
	// Reading this back also proves the column default is written in a form
	// dbtype parses, which is the whole point of the two matching.
	assert.False(t, user.CreatedAt.IsZero(), "created_at default was not applied")
	assert.WithinDuration(t, time.Now(), user.CreatedAt.Time, time.Minute)
}

func TestCreateUserRejectsDuplicateUsername(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	q := models.New(newTestDB(t))

	params := models.CreateUserParams{Username: "ada", PasswordHash: "hash", Role: dbtype.RoleAdmin}

	_, err := q.CreateUser(ctx, params)
	require.NoError(t, err)

	_, err = q.CreateUser(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNIQUE constraint failed")
}

// TestCreateUserRefusesASecondAdmin checks the schema itself holds an
// instance to one admin, whichever code path inserts the account.
func TestCreateUserRefusesASecondAdmin(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	q := models.New(newTestDB(t))

	_, err := q.CreateUser(ctx, models.CreateUserParams{Username: "ada", PasswordHash: "hash", Role: dbtype.RoleAdmin})
	require.NoError(t, err)

	_, err = q.CreateUser(ctx, models.CreateUserParams{Username: "grace", PasswordHash: "hash", Role: dbtype.RoleAdmin})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNIQUE constraint failed")

	_, err = q.CreateUser(ctx, models.CreateUserParams{Username: "linus", PasswordHash: "hash", Role: dbtype.RoleReadWrite})
	assert.NoError(t, err, "other roles have no limit")
}

// TestCreateFirstUserOnlyIntoAnEmptyTable checks the setup insert writes
// nothing once any account exists.
func TestCreateFirstUserOnlyIntoAnEmptyTable(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	q := models.New(newTestDB(t))

	user, err := q.CreateFirstUser(ctx, models.CreateFirstUserParams{Username: "ada", PasswordHash: "hash"})
	require.NoError(t, err)
	assert.Equal(t, dbtype.RoleAdmin, user.Role)

	_, err = q.CreateFirstUser(ctx, models.CreateFirstUserParams{Username: "grace", PasswordHash: "hash"})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// TestMigrationKeepsTheOldestAdmin covers a database that already holds more
// than one admin: the migration that limits it to one keeps the oldest and
// makes the others editors, so it cannot fail and stop startup.
func TestMigrationKeepsTheOldestAdmin(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	conn, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "test.db")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, migrateTo(conn, 15))

	for _, u := range []struct{ name, role string }{
		{"ada", "admin"}, {"grace", "read"}, {"linus", "admin"}, {"ken", "admin"},
	} {
		_, err := conn.ExecContext(ctx,
			`INSERT INTO users (username, password_hash, role) VALUES (?, 'hash', ?)`, u.name, u.role)
		require.NoError(t, err)
	}

	require.NoError(t, migrateTo(conn, dbVersion))

	users, err := models.New(conn).ListUsers(ctx)
	require.NoError(t, err)

	roles := make(map[string]dbtype.UserRole, len(users))
	for _, u := range users {
		roles[u.Username] = u.Role
	}

	assert.Equal(t, map[string]dbtype.UserRole{
		"ada":   dbtype.RoleAdmin,
		"grace": dbtype.RoleRead,
		"linus": dbtype.RoleReadWrite,
		"ken":   dbtype.RoleReadWrite,
	}, roles)
}

// TestTOTPSecretIsFixedOnceEnabled checks the guard on the two enrollment
// writes: neither touches an account whose 2FA is already on.
func TestTOTPSecretIsFixedOnceEnabled(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	q := models.New(newTestDB(t))

	user, err := q.CreateUser(ctx, models.CreateUserParams{Username: "ada", PasswordHash: "hash", Role: dbtype.RoleAdmin})
	require.NoError(t, err)

	secret := func(s string) models.SetUserTOTPSecretParams {
		return models.SetUserTOTPSecretParams{TOTPSecret: sql.NullString{String: s, Valid: true}, ID: user.ID}
	}
	enable := models.EnableUserTOTPParams{TOTPConfirmedAt: dbtype.NewNullTime(time.Now()), ID: user.ID}

	n, err := q.SetUserTOTPSecret(ctx, secret("FIRSTSECRET"))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	n, err = q.EnableUserTOTP(ctx, enable)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	n, err = q.SetUserTOTPSecret(ctx, secret("SECONDSECRET"))
	require.NoError(t, err)
	assert.Zero(t, n, "a secret was replaced while 2FA was on")

	n, err = q.EnableUserTOTP(ctx, enable)
	require.NoError(t, err)
	assert.Zero(t, n, "2FA was enabled a second time")

	got, err := q.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "FIRSTSECRET", got.TOTPSecret.String)
}

// TestNewAppliesPragmas checks the DSN pragmas actually reach the connection.
// foreign_keys is the one that matters: without it every REFERENCES clause in
// the schema is decorative.
func TestNewAppliesPragmas(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)

	var foreignKeys int
	require.NoError(t, db.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys))
	assert.Equal(t, 1, foreignKeys, "foreign key enforcement is off")

	var journalMode string
	require.NoError(t, db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journalMode))
	assert.Equal(t, "wal", strings.ToLower(journalMode))
}

// TestPragmasApplyToEveryPooledConnection is the reason the pragmas live in the
// DSN. A PRAGMA issued once after Open binds to whichever connection served it,
// leaving the rest of the pool unconfigured.
func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := newTestDB(t)

	first, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })

	// Held at the same time as the first, so the pool has to open a second
	// connection.
	second, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	for i, conn := range []*sql.Conn{first, second} {
		var foreignKeys int
		require.NoError(t, conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys))
		assert.Equal(t, 1, foreignKeys, "foreign keys off on pooled connection %d", i)
	}
}

// TestDSNEscapesPath covers a file name holding a character that means
// something in a URL. Left unescaped, the ? would start the query string
// early and the pragmas would no longer be read as pragmas.
func TestDSNEscapesPath(t *testing.T) {
	t.Parallel()

	file := "my_db?.sqlite"

	result := dsn(file)

	assert.Contains(t, result, "my_db%3F.sqlite")
	assert.Contains(t, result, "?_pragma=foreign_keys%281%29")
}

func BenchmarkDSN(b *testing.B) {
	for b.Loop() {
		dsn("test.db")
	}
}

// TestTimestampWritersAgreeOnOrdering covers the two writers of a timestamp
// column: SQLite's own default and a Go value through dbtype. They are
// compared as TEXT, so a disagreement over separator or width would order rows
// by which writer produced them.
func TestTimestampWritersAgreeOnOrdering(t *testing.T) {
	t.Parallel()

	conn := newTestDB(t)

	_, err := conn.ExecContext(t.Context(), `INSERT INTO users (username, password_hash, role) VALUES ('middle', 'h', 'read')`)
	require.NoError(t, err)

	var middle dbtype.Time
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT created_at FROM users`).Scan(&middle))

	insert := func(name string, at dbtype.Time) {
		_, err := conn.ExecContext(t.Context(),
			`INSERT INTO users (username, password_hash, role, created_at) VALUES (?, 'h', 'read', ?)`, name, at)
		require.NoError(t, err)
	}

	insert("last", dbtype.NewTime(middle.Add(time.Second)))
	insert("first", dbtype.NewTime(middle.Add(-time.Second)))

	rows, err := conn.QueryContext(t.Context(), `SELECT username FROM users ORDER BY created_at`)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	var order []string

	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))

		order = append(order, name)
	}

	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"first", "middle", "last"}, order)
}
