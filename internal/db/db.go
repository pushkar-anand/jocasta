package db

import (
	"database/sql"
	"fmt"
	"path"
)

const dbVersion = 1

type (
	DB struct {
		Conn *sql.DB
	}

	Config struct {
		Name string
		Path string
	}
)

func New(cfg *Config) (*DB, error) {
	n := path.Join(cfg.Path, cfg.Name)

	conn, err := sql.Open("sqlite3", n)
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
