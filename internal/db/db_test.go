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

// newTestDB opens a database in a directory for the test only.
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
// the tables that the queries expect.
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

// TestNewIsIdempotent covers a second open of a migrated database. Every
// restart after the first does this.
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

	// New already migrated. A second run must do nothing.
	require.NoError(t, migrateDB(db))
}

// TestCreateUser exercises the generated queries against a real migrated
// schema. Only this test checks the two against each other.
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
	// This read also proves that the column default has a form that dbtype
	// parses. The two must match for this reason.
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

// TestCreateUserRefusesASecondAdmin checks that the schema itself limits an
// instance to one admin, for every code path that inserts the account.
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

// TestCreateFirstUserOnlyIntoAnEmptyTable checks that the setup insert writes
// nothing when an account exists.
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

// TestMigrationKeepsTheOldestAdmin covers a database with more than one admin.
// The migration that limits it to one keeps the oldest admin. It makes the
// others editors. So it cannot fail and stop startup.
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
// writes. Neither write changes an account whose 2FA is already on.
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

// TestNewAppliesPragmas checks that the DSN pragmas reach the connection. The
// important pragma is foreign_keys. Without it, no REFERENCES clause in the
// schema has an effect.
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

// TestPragmasApplyToEveryPooledConnection shows why the pragmas are in the DSN.
// A PRAGMA sent once after Open applies only to the connection that ran it.
// The other connections in the pool do not get it.
func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := newTestDB(t)

	first, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })

	// The test holds this connection at the same time as the first. So the
	// pool must open a second connection.
	second, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	for i, conn := range []*sql.Conn{first, second} {
		var foreignKeys int
		require.NoError(t, conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys))
		assert.Equal(t, 1, foreignKeys, "foreign keys off on pooled connection %d", i)
	}
}

// TestDSNEscapesPath covers a file name with a character that has a meaning in
// a URL. Without an escape, the ? would start the query string too early. The
// pragmas would then not be read as pragmas.
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

// queryRows returns every row that q selects, each as its column values in
// order.
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

// peerOnDelete returns what a delete of the peer device does to the rows of
// table.
func peerOnDelete(t *testing.T, conn *sql.DB, table string) string {
	t.Helper()

	var action string
	require.NoError(t, conn.QueryRowContext(t.Context(),
		`SELECT on_delete FROM pragma_foreign_key_list(?) WHERE "from" = 'peer_device_id'`, table,
	).Scan(&action))

	return action
}

// TestPeerSetNullRebuildKeepsEveryColumn covers migration 16, which rebuilds
// the traffic and attempt tables. Every column of a row stays after the copy.
// If the peer device of a row is gone, the row keeps its address with no peer
// device. If the own device of a row is gone, the migration deletes the row.
// The down migration keeps the rows.
func TestPeerSetNullRebuildKeepsEveryColumn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	conn, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "test.db")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, migrateTo(conn, 15))

	// The test cannot write the orphans below while foreign keys are on.
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

// TestClaimsPerAddressBackfillsTheAddress covers migration 22, which keys
// device_sources by address. An existing claim takes the lowest current
// address of the device. If no address is current, it takes the latest. The
// migration deletes a claim about a device with no address. The down
// migration keeps one claim for each device and source.
func TestClaimsPerAddressBackfillsTheAddress(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	conn, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "test.db")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, migrateTo(conn, 21))

	for _, q := range []string{
		`INSERT INTO sources (id, kind, name) VALUES (1, 'SWEEP', 'sweep:test')`,
		`INSERT INTO devices (id, mac, hostname, hostname_source)
		 VALUES (1, '00:00:5e:00:53:01', 'host-a.example.com', 'DNS'), (2, '00:00:5e:00:53:02', NULL, NULL),
		        (3, '00:00:5e:00:53:03', NULL, NULL), (4, '00:00:5e:00:53:04', 'host-b.example.com', 'DNS'),
		        (5, '00:00:5e:00:53:05', '_gateway', 'DNS')`,
		`INSERT INTO addresses (device_id, ip, is_current, last_seen)
		 VALUES (1, '192.0.2.20', 1, '2026-01-01T00:00:00.000Z'),
		        (1, '192.0.2.10', 1, '2026-01-01T00:00:00.000Z'),
		        (2, '192.0.2.30', 0, '2026-01-01T00:00:00.000Z'),
		        (2, '192.0.2.31', 0, '2026-01-02T00:00:00.000Z'),
		        (4, '192.0.2.40', 1, '2026-01-01T00:00:00.000Z'),
		        (5, '192.0.2.1', 1, '2026-01-01T00:00:00.000Z')`,
		`INSERT INTO device_sources (device_id, source_id, hostname, hostname_source, detail, first_seen, last_seen)
		 VALUES (1, 1, 'host-a.example.com', 'DNS', '{"interface":"vlan10"}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
		        (2, 1, NULL, NULL, NULL, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
		        (3, 1, NULL, NULL, NULL, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
		        (4, 1, 'host-b.example.com', 'DNS', '{"interface":"vlan10"}', '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z'),
		        (5, 1, '_gateway', 'DNS', NULL, '2026-01-01T00:00:00.000Z', '2026-01-01T00:00:00.000Z')`,
	} {
		_, err := conn.ExecContext(ctx, q)
		require.NoError(t, err, q)
	}

	require.NoError(t, migrateTo(conn, 22))

	// Only a device that had one address tells which address its claim was
	// about. The others keep no name or detail until a source reads each one
	// again.
	assert.Equal(t, [][]any{
		{int64(1), "192.0.2.10", nil, nil, nil},
		{int64(2), "192.0.2.31", nil, nil, nil},
		{int64(4), "192.0.2.40", "host-b.example.com", "DNS", `{"interface":"vlan10"}`},
		{int64(5), "192.0.2.1", nil, nil, nil},
	}, queryRows(t, conn, `SELECT device_id, address, hostname, hostname_source, detail FROM device_sources ORDER BY device_id`))

	assert.Equal(t, [][]any{
		{int64(1), "host-a.example.com"},
		{int64(2), nil},
		{int64(3), nil},
		{int64(4), "host-b.example.com"},
		{int64(5), nil},
	}, queryRows(t, conn, `SELECT id, hostname FROM devices ORDER BY id`), "systemd-resolved's _gateway names no device")

	require.NoError(t, migrateTo(conn, 21))

	assert.Equal(t, [][]any{
		{int64(1), nil},
		{int64(2), nil},
		{int64(4), "host-b.example.com"},
		{int64(5), nil},
	}, queryRows(t, conn, `SELECT device_id, hostname FROM device_sources ORDER BY device_id`))
}

// TestTimestampWritersAgreeOnOrdering covers the two writers of a timestamp
// column: the SQLite default and a Go value through dbtype. SQLite compares
// them as TEXT. So if they used a different separator or width, the order of
// rows would depend on the writer.
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
