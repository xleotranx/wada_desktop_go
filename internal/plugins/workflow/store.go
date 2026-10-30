package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"wada-desktop-go/internal/contract/agent"
)

type storeWrite struct {
	origin  string
	session agent.SessionID
	record  *savedLane
}
type store struct {
	path  string
	mu    sync.Mutex
	queue []storeWrite
	wake  chan struct{}
}

func newStore(path string) *store { return &store{path: path, wake: make(chan struct{}, 1)} }
func (s *store) enqueue(w storeWrite) {
	s.mu.Lock()
	s.queue = append(s.queue, w)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *store) put(record savedLane) {
	s.enqueue(storeWrite{origin: record.Origin, session: record.Session, record: &record})
}
func (s *store) remove(origin string, id agent.SessionID) {
	s.enqueue(storeWrite{origin: origin, session: id})
}
func (s *store) run(ctx context.Context, loaded func([]savedLane, error), failed func(error)) {
	db, records, err := openStore(ctx, s.path)
	if err != nil {
		loaded(nil, err)
		return
	}
	defer db.Close()
	loaded(records, nil)
	write := func() error {
		s.mu.Lock()
		batch := s.queue
		s.queue = nil
		s.mu.Unlock()
		if len(batch) == 0 {
			return nil
		}
		var statements []sqlite.Statement
		for _, w := range batch {
			if w.record == nil {
				statements = append(statements, sqlite.Statement{SQL: "DELETE FROM tasks WHERE origin=? AND session=?", Args: []any{w.origin, string(w.session)}})
				continue
			}
			raw, err := json.Marshal(w.record)
			if err != nil {
				return err
			}
			statements = append(statements, sqlite.Statement{SQL: "INSERT INTO tasks(origin,session,record) VALUES(?,?,?) ON CONFLICT(origin,session) DO UPDATE SET record=excluded.record", Args: []any{w.origin, string(w.session), string(raw)}})
		}
		wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := db.Transaction(wctx, statements)
		if err != nil {
			s.mu.Lock()
			s.queue = append(batch, s.queue...)
			s.mu.Unlock()
		}
		return err
	}
	for {
		select {
		case <-s.wake:
			if err := write(); err != nil {
				failed(err)
			}
		case <-ctx.Done():
			if err := write(); err != nil {
				failed(err)
			}
			return
		}
	}
}
func openStore(ctx context.Context, path string) (*sqlite.DB, []savedLane, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*sqlite.DB, []savedLane, error) { db.Close(); return nil, nil, err }
	rows, err := db.Query(ctx, "PRAGMA user_version")
	if err != nil {
		return fail(err)
	}
	version, _ := rows.Values[0][0].(int64)
	switch version {
	case 0:
		tables, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
		if err != nil {
			return fail(err)
		}
		if len(tables.Values) > 0 {
			return fail(fmt.Errorf("%s is not a workflow database", path))
		}
		_, err = db.Transaction(ctx, []sqlite.Statement{{SQL: "CREATE TABLE tasks(origin TEXT NOT NULL,session TEXT NOT NULL,record TEXT NOT NULL,PRIMARY KEY(origin,session))"}, {SQL: "PRAGMA user_version=1"}})
		if err != nil {
			return fail(err)
		}
	case 1:
	default:
		return fail(fmt.Errorf("workflow schema %d is unsupported", version))
	}
	rows, err = db.Query(ctx, "SELECT record FROM tasks")
	if err != nil {
		return fail(err)
	}
	var records []savedLane
	for _, row := range rows.Values {
		var record savedLane
		raw, ok := row[0].(string)
		if !ok {
			return fail(fmt.Errorf("invalid workflow record"))
		}
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return fail(err)
		}
		records = append(records, record)
	}
	return db, records, nil
}
