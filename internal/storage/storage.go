// Package storage owns SQLite opening and embedded schema migrations.
package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open creates or opens a SQLite database, enables integrity protections, and
// applies the migrations bundled with this binary.
func Open(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			return nil, fmt.Errorf("create database: %w", createErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("close new database: %w", closeErr)
		}
	} else if err != nil {
		return nil, fmt.Errorf("stat database: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1) // PRAGMAs such as foreign_keys are connection-scoped.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	for index, entry := range entries {
		version := index + 1
		var present int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&present); err != nil {
			return err
		}
		if present > 0 {
			continue
		}
		sqlText, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %d: %w", version, err)
		}
		if _, err := tx.Exec(string(sqlText)); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, CURRENT_TIMESTAMP)`, version); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}
