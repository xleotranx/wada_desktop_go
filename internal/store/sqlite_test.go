package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesMigratesAndChecksVersion(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "app.db")

	v1 := []Migration{{
		Version: 1,
		Up: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, val TEXT NOT NULL)`)
			return err
		},
	}}
	db, err := Open(ctx, path, "test", v1)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	if err := ExecTx(ctx, db, []Statement{{SQL: "INSERT INTO items (id, val) VALUES (?, ?)", Args: []any{"k1", "one"}}}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Upgrade v1 -> v2 preserving existing data.
	v2 := append(v1, Migration{
		Version: 2,
		Up: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `ALTER TABLE items ADD COLUMN extra INTEGER NOT NULL DEFAULT 7`)
			return err
		},
	})
	db, err = Open(ctx, path, "test", v2)
	if err != nil {
		t.Fatal(err)
	}
	var val string
	var extra, version int
	if err := db.QueryRowContext(ctx, "SELECT val, extra FROM items WHERE id = ?", "k1").Scan(&val, &extra); err != nil {
		t.Fatal(err)
	}
	if val != "one" || extra != 7 {
		t.Fatalf("migrated row = (%q, %d)", val, extra)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("user_version = %d, want 2", version)
	}
	db.Close()

	// Future schema version is refused.
	if db, err := Open(ctx, path, "test", v1); err == nil {
		db.Close()
		t.Fatal("future schema accepted")
	}
}

func TestOpenRefusesForeignNonEmptyDatabase(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "foreign.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "CREATE TABLE other (x TEXT)"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := Open(ctx, path, "test", []Migration{{
		Version: 1,
		Up:      func(context.Context, *sql.Tx) error { return nil },
	}})
	if err == nil {
		db.Close()
		t.Fatal("foreign database accepted")
	}
	if !strings.Contains(err.Error(), "is not a test database") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecTxRollsBackOnFailure(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "tx.db")
	db, err := Open(ctx, path, "test", []Migration{{
		Version: 1,
		Up: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `CREATE TABLE t (id TEXT PRIMARY KEY)`)
			return err
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = ExecTx(ctx, db, []Statement{
		{SQL: "INSERT INTO t (id) VALUES (?)", Args: []any{"a"}},
		{SQL: "INSERT INTO t (id) VALUES (?)", Args: []any{"a"}}, // constraint failure
	})
	if err == nil {
		t.Fatal("duplicate insert succeeded")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM t").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rollback left %d rows", count)
	}
}
