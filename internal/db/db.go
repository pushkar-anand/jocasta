// Package db opens and migrates the jocasta SQLite database.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"path"

	// Registers the pure-Go "sqlite" driver. Imported explicitly rather than
	// leaning on the migrate driver's own import, so swapping that out later
	// cannot silently unregister the driver the application opens with.
	_ "modernc.org/sqlite"
)

const dbVersion = 2

// pragmas travel in the DSN so the pool applies them to every connection it
// opens. foreign_keys is the reason this matters: it is per-connection, so
// setting it once after Open leaves every later pooled connection with
// constraint enforcement quietly switched off.
var pragmas = []string{
	"foreign_keys(1)",
	"journal_mode(WAL)",
	"busy_timeout(5000)",
}

var pragmaString string

func init() {
	q := make(url.Values, len(pragmas))
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}

	pragmaString = q.Encode()
}

type (
	// DB wraps the pooled connection to the application database.
	DB struct {
		Conn *sql.DB
	}

	// Config names where the database file lives.
	Config struct {
		Name string
		Path string
	}
)

// New opens (creating if needed) and migrates the database described by cfg.
func New(cfg *Config) (*DB, error) {
	n := path.Join(cfg.Path, cfg.Name)

	conn, err := sql.Open("sqlite", dsn(n))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{
		Conn: conn,
	}

	err = migrateDB(db)
	if err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return db, nil
}

// dsn builds a file DSN carrying the pragmas above.
func dsn(file string) string {
	u := &url.URL{
		Scheme:   "file",
		Opaque:   url.PathEscape(file),
		RawQuery: pragmaString,
	}

	return u.String()
}
