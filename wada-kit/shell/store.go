package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/xleotranx/wada-kit/kernel"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/sqlite"
)

const storeSchema = 1

// dataDir is where the app keeps its data: override when it is set, or else
// folder in the user's config directory. A production build (`mygo build`)
// and a development run keep theirs apart.
func dataDir(folder, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, dataFolder(folder, mygo.IsDev())), nil
}

// dataFolder names the app's folder in the user's config directory.
func dataFolder(folder string, dev bool) string {
	if dev {
		return folder + "-dev"
	}
	return folder
}

// sqliteStore is the kernel's settings store: one table of JSON values.
type sqliteStore struct{ db *sqlite.DB }

// openStore opens or creates the settings database. It refuses a database of
// another schema rather than changing it.
func openStore(path string) (*sqliteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*sqliteStore, error) { db.Close(); return nil, err }
	rows, err := db.Query(ctx, "PRAGMA user_version")
	if err != nil {
		return fail(err)
	}
	switch version, _ := rows.Values[0][0].(int64); version {
	case storeSchema:
		return &sqliteStore{db}, nil
	case 0:
		tables, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
		if err != nil {
			return fail(err)
		}
		if len(tables.Values) > 0 {
			return fail(fmt.Errorf("%s is not a settings database", path))
		}
	default:
		return fail(fmt.Errorf("%s has settings schema %d; this build reads %d", path, version, storeSchema))
	}
	_, err = db.Transaction(ctx, []sqlite.Statement{
		{SQL: "CREATE TABLE settings (namespace TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY (namespace, key))"},
		{SQL: fmt.Sprintf("PRAGMA user_version = %d", storeSchema)},
	})
	if err != nil {
		return fail(err)
	}
	return &sqliteStore{db}, nil
}

func (s *sqliteStore) Load(ctx context.Context) ([]kernel.Value, error) {
	rows, err := s.db.Query(ctx, "SELECT namespace, key, value FROM settings ORDER BY namespace, key")
	if err != nil {
		return nil, err
	}
	values := make([]kernel.Value, 0, len(rows.Values))
	for _, row := range rows.Values {
		ns, _ := row[0].(string)
		key, _ := row[1].(string)
		value, _ := row[2].(string)
		values = append(values, kernel.Value{Namespace: ns, Key: key, JSON: json.RawMessage(value)})
	}
	return values, nil
}

func (s *sqliteStore) Save(ctx context.Context, changed []kernel.Value) error {
	statements := make([]sqlite.Statement, 0, len(changed))
	for _, v := range changed {
		if v.JSON == nil {
			statements = append(statements, sqlite.Statement{SQL: "DELETE FROM settings WHERE namespace = ? AND key = ?",
				Args: []any{v.Namespace, v.Key}})
			continue
		}
		statements = append(statements, sqlite.Statement{
			SQL:  "INSERT INTO settings (namespace, key, value) VALUES (?, ?, ?) ON CONFLICT (namespace, key) DO UPDATE SET value = excluded.value",
			Args: []any{v.Namespace, v.Key, string(v.JSON)}})
	}
	_, err := s.db.Transaction(ctx, statements)
	return err
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// memoryStore keeps settings for this run only, when the database can't open.
type memoryStore struct {
	mu     sync.Mutex
	values map[[2]string]json.RawMessage
}

func (s *memoryStore) Load(context.Context) ([]kernel.Value, error) { return nil, nil }

func (s *memoryStore) Save(_ context.Context, changed []kernel.Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[[2]string]json.RawMessage{}
	}
	for _, v := range changed {
		s.values[[2]string{v.Namespace, v.Key}] = v.JSON
	}
	return nil
}
