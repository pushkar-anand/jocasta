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

// queryRows returns every row q selects, each as its column values in order.
func queryRows(t *testing.T, conn *sql.DB, q string) [][]any {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), q)
	require.NoError(t, err)

	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	require.NoError(t, err)

	var out [][]any

	for rows.Next() {
		row := make([]any, len(cols))
		ptrs := make([]any, len(cols))

		for i := range row {
			ptrs[i] = &row[i]
		}

		require.NoError(t, rows.Scan(ptrs...))

		out = append(out, row)
	}

	require.NoError(t, rows.Err())

	return out
}

// peerOnDelete returns what deleting the peer device does to table's rows.
func peerOnDelete(t *testing.T, conn *sql.DB, table string) string {
	t.Helper()

	var action string
	require.NoError(t, conn.QueryRowContext(t.Context(),
		`SELECT on_delete FROM pragma_foreign_key_list(?) WHERE "from" = 'peer_device_id'`, table,
	).Scan(&action))

	return action
}

// TestPeerSetNullRebuildKeepsEveryColumn covers migration 16, which rebuilds
// the traffic and attempt tables. Every column of a row survives the copy, a
// row whose peer device is gone keeps its address with no peer device, and a
// row whose own device is gone is dropped. Going back down keeps the rows.
func TestPeerSetNullRebuildKeepsEveryColumn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	conn, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "test.db")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, migrateTo(conn, 15))

	// The orphans below cannot be written while foreign keys are enforced.
	seed, err := conn.Conn(ctx)
	require.NoError(t, err)

	for _, q := range []string{
		`PRAGMA foreign_keys = OFF`,
		`INSERT INTO sources (id, kind, name) VALUES (1, 'ROUTER', 'netflow:test')`,
		`INSERT INTO devices (id, mac) VALUES (1, '00:00:5e:00:53:01'), (2, '00:00:5e:00:53:02')`,
		`INSERT INTO traffic_hourly (source_id, device_id, hour, peer_device_id, peer_ip, peer_name, peer_asn,
		                            protocol, service_port, bytes_out, bytes_in, packets_out, packets_in,
		                            connections, connections_in)
		 VALUES (1, 1, '2026-01-01T12:00:00.000Z', 2, '192.0.2.11', 'host-b.example.com', 64496, 6, 22,
		         1, 2, 3, 4, 5, 6),
		        (1, 1, '2026-01-01T12:00:00.000Z', 9, '192.0.2.99', NULL, NULL, 17, 53, 7, 8, 9, 10, 11, 12),
		        (1, 9, '2026-01-01T12:00:00.000Z', 1, '192.0.2.10', NULL, NULL, 6, 22, 1, 1, 1, 1, 1, 1)`,
		`INSERT INTO attempts_hourly (source_id, device_id, hour, peer_device_id, peer_ip, peer_asn,
		                             protocol, attempts, answered, port_count, ports)
		 VALUES (1, 1, '2026-01-01T12:00:00.000Z', 2, '192.0.2.11', 64496, 6, 7, 3, 2, '22,23'),
		        (1, 1, '2026-01-01T12:00:00.000Z', 9, '192.0.2.99', NULL, 1, 4, 0, 0, ''),
		        (1, 9, '2026-01-01T12:00:00.000Z', 1, '192.0.2.10', NULL, 6, 1, 0, 1, '22')`,
		`PRAGMA foreign_keys = ON`,
	} {
		_, err := seed.ExecContext(ctx, q)
		require.NoError(t, err, q)
	}

	require.NoError(t, seed.Close())

	const (
		traffic = `SELECT source_id, device_id, hour, peer_device_id, peer_ip, peer_name, peer_asn,
		                  protocol, service_port, bytes_out, bytes_in, packets_out, packets_in,
		                  connections, connections_in
		           FROM traffic_hourly ORDER BY device_id, peer_ip`
		attempts = `SELECT source_id, device_id, hour, peer_device_id, peer_ip, peer_asn,
		                   protocol, attempts, answered, port_count, ports
		            FROM attempts_hourly ORDER BY device_id, peer_ip`
	)

	hour := "2026-01-01T12:00:00.000Z"
	wantTraffic := [][]any{
		{int64(1), int64(1), hour, int64(2), "192.0.2.11", "host-b.example.com", int64(64496), int64(6), int64(22),
			int64(1), int64(2), int64(3), int64(4), int64(5), int64(6)},
		{int64(1), int64(1), hour, nil, "192.0.2.99", nil, nil, int64(17), int64(53),
			int64(7), int64(8), int64(9), int64(10), int64(11), int64(12)},
	}
	wantAttempts := [][]any{
		{int64(1), int64(1), hour, int64(2), "192.0.2.11", int64(64496), int64(6), int64(7), int64(3), int64(2), "22,23"},
		{int64(1), int64(1), hour, nil, "192.0.2.99", nil, int64(1), int64(4), int64(0), int64(0), ""},
	}

	require.NoError(t, migrateTo(conn, 16))

	assert.Equal(t, wantTraffic, queryRows(t, conn, traffic))
	assert.Equal(t, wantAttempts, queryRows(t, conn, attempts))
	assert.Equal(t, "SET NULL", peerOnDelete(t, conn, "traffic_hourly"))
	assert.Equal(t, "SET NULL", peerOnDelete(t, conn, "attempts_hourly"))

	require.NoError(t, migrateTo(conn, 15))

	assert.Equal(t, wantTraffic, queryRows(t, conn, traffic))
	assert.Equal(t, wantAttempts, queryRows(t, conn, attempts))
	assert.Equal(t, "CASCADE", peerOnDelete(t, conn, "traffic_hourly"))
	assert.Equal(t, "CASCADE", peerOnDelete(t, conn, "attempts_hourly"))
}

// TestTimestampWritersAgreeOnOrdering covers the two writers of a timestamp
// column: SQLite's own default and a Go value through dbtype. They are
// compared as TEXT, so a disagreement over separator or width would order rows
// by which writer produced them.
func TestTimestampWritersAgreeOnOrdering(t *testing.T) {
	t.Parallel()

	conn := newTestDB(t)

	_, err := conn.ExecContext(t.Context(), `INSERT INTO users (username, password_hash, role) VALUES ('middle', 'h', 'admin')`)
	require.NoError(t, err)

	var middle dbtype.Time
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT created_at FROM users`).Scan(&middle))

	insert := func(name string, at dbtype.Time) {
		_, err := conn.ExecContext(t.Context(),
			`INSERT INTO users (username, password_hash, role, created_at) VALUES (?, 'h', 'admin', ?)`, name, at)
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
