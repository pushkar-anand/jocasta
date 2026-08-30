package db

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// migrationDir is the directory where the migration files are stored
const migrationDir = "migrations"

// migrationFiles contains all the migration files embedded in the binary
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrateDB(db *DB) error {
	td, err := sqlite3.WithInstance(db.Conn, &sqlite3.Config{})
	if err != nil {
		return fmt.Errorf("failed to init sqlite3 migration target: %w", err)
	}

	defer func(t database.Driver) { _ = t.Close() }(td)

	sd, err := iofs.New(migrationFiles, migrationDir)
	if err != nil {
		return fmt.Errorf("failed to init iofs migration source: %w", err)
	}

	defer func(s source.Driver) { _ = s.Close() }(sd)

	m, err := migrate.NewWithInstance("iofs", sd, "sqlite3", td)
	if err != nil {
		return fmt.Errorf("failed to init migrate: %w", err)
	}

	err = m.Migrate(dbVersion)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to migrate: %w", err)
	}

	return nil
}
