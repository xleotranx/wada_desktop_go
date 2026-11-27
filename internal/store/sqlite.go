// Package store opens local SQLite databases with pure-Go modernc.org/sqlite,
// WAL journaling, and numbered schema migrations.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Migration upgrades a database schema to Version inside a single transaction.
type Migration struct {
	Version int
	Up      func(ctx context.Context, tx *sql.Tx) error
}

// Statement is one SQL command executed inside ExecTx.
type Statement struct {
	SQL  string
	Args []any
}

// Open opens or creates a SQLite database at path, applies WAL/busy/foreign-key
// pragmas, refuses unrecognized non-empty databases or future schema versions,
// and runs pending migrations in version order.
func Open(ctx context.Context, path, name string, migrations []Migration) (*sql.DB, error) {
	if len(migrations) == 0 {
		return nil, errors.New("store: at least one migration is required")
	}
	prev := 0
	for _, m := range migrations {
		if m.Version <= prev || m.Up == nil {
			return nil, fmt.Errorf("store: invalid migration version %d", m.Version)
		}
		prev = m.Version
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*sql.DB, error) {
		_ = db.Close()
		return nil, err
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return fail(err)
		}
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	maxVersion := migrations[len(migrations)-1].Version
	if version > maxVersion {
		return fail(fmt.Errorf("%s has %s schema %d; this build reads up to %d", path, name, version, maxVersion))
	}
	if version == 0 {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
			return fail(err)
		}
		if count > 0 {
			return fail(fmt.Errorf("%s is not a %s database", path, name))
		}
	}
	for _, m := range migrations {
		if m.Version <= version {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fail(err)
		}
		if err := m.Up(ctx, tx); err != nil {
			_ = tx.Rollback()
			return fail(err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
			_ = tx.Rollback()
			return fail(err)
		}
		if err := tx.Commit(); err != nil {
			return fail(err)
		}
	}
	return db, nil
}

// ExecTx runs stmts atomically in one SQLite transaction.
func ExecTx(ctx context.Context, db *sql.DB, stmts []Statement) error {
	if len(stmts) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.SQL, st.Args...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
