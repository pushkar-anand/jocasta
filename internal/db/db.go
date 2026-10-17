// Package db opens and migrates the jocasta SQLite database.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"path"

	// Registers the pure-Go "sqlite" driver. The migrate driver also imports
	// it. This import stays here, so a later change of the migrate driver
	// cannot remove the driver that the application opens.
	_ "modernc.org/sqlite"
)

const dbVersion = 24

// pragmas go in the DSN, so the pool applies them to every connection that it
// opens. This is important for foreign_keys, which applies to one connection
// only. If you set it once after Open, every later connection in the pool has
// no constraint enforcement, and nothing warns you.
var pragmas = []string{
	"foreign_keys(1)",
	"journal_mode(WAL)",
	"busy_timeout(5000)",
}

// pragmaString is the pragmas as DSN query parameters. It is built once.
var pragmaString = encodePragmas()

func encodePragmas() string {
	q := make(url.Values, len(pragmas))
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}

	return q.Encode()
}

// Config names the location of the database file.
type Config struct {
	Name string
	Path string
}

// New opens and migrates the database that cfg describes. If the database does
// not exist, New creates it. New returns the pool for reads and writes.
func New(cfg *Config) (*sql.DB, error) {
	n := path.Join(cfg.Path, cfg.Name)

	conn, err := sql.Open("sqlite", dsn(n))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := migrateDB(conn); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return conn, nil
}

// dsn builds a file DSN with the pragmas above.
func dsn(file string) string {
	u := &url.URL{
		Scheme:   "file",
		Opaque:   url.PathEscape(file),
		RawQuery: pragmaString,
	}

	return u.String()
}
