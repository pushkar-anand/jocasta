package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// migrationDir is the path the migrations are embedded under, below.
const migrationDir = "migrations"

// migrationFiles carries the schema history in the binary, so a fresh database
// is brought up to dbVersion with no files on disk.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrateDB(conn *sql.DB) error {
	td, err := sqlite.WithInstance(conn, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to init sqlite migration target: %w", err)
	}

	// td is deliberately not closed: the sqlite driver's Close closes the
	// *sql.DB it was handed, which is the connection the caller goes on using.

	sd, err := iofs.New(migrationFiles, migrationDir)
	if err != nil {
		return fmt.Errorf("failed to init iofs migration source: %w", err)
	}

	defer func(s source.Driver) { _ = s.Close() }(sd)

	m, err := migrate.NewWithInstance("iofs", sd, "sqlite", td)
	if err != nil {
		return fmt.Errorf("failed to init migrate: %w", err)
	}

	err = m.Migrate(dbVersion)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to migrate: %w", err)
	}

	return nil
}
