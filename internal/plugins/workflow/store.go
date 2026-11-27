package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/store"
)

var workflowMigrations = []store.Migration{{
	Version: 1,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE tasks(origin TEXT NOT NULL,session TEXT NOT NULL,record TEXT NOT NULL,PRIMARY KEY(origin,session))`)
		return err
	},
}}

type storeWrite struct {
	origin  string
	session agent.SessionID
	record  *savedLane
}
type workflowStore struct {
	path  string
	mu    sync.Mutex
	queue []storeWrite
	wake  chan struct{}
}

func newStore(path string) *workflowStore {
	return &workflowStore{path: path, wake: make(chan struct{}, 1)}
}
func (s *workflowStore) enqueue(w storeWrite) {
	s.mu.Lock()
	s.queue = append(s.queue, w)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *workflowStore) put(record savedLane) {
	s.enqueue(storeWrite{origin: record.Origin, session: record.Session, record: &record})
}
func (s *workflowStore) remove(origin string, id agent.SessionID) {
	s.enqueue(storeWrite{origin: origin, session: id})
}
func (s *workflowStore) run(ctx context.Context, loaded func([]savedLane, error), failed func(error)) {
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
		var statements []store.Statement
		for _, w := range batch {
			if w.record == nil {
				statements = append(statements, store.Statement{SQL: "DELETE FROM tasks WHERE origin=? AND session=?", Args: []any{w.origin, string(w.session)}})
				continue
			}
			raw, err := json.Marshal(w.record)
			if err != nil {
				return err
			}
			statements = append(statements, store.Statement{SQL: "INSERT INTO tasks(origin,session,record) VALUES(?,?,?) ON CONFLICT(origin,session) DO UPDATE SET record=excluded.record", Args: []any{w.origin, string(w.session), string(raw)}})
		}
		wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := store.ExecTx(wctx, db, statements)
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
func openStore(ctx context.Context, path string) (*sql.DB, []savedLane, error) {
	db, err := store.Open(ctx, path, "workflow", workflowMigrations)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*sql.DB, []savedLane, error) { _ = db.Close(); return nil, nil, err }
	rows, err := db.QueryContext(ctx, "SELECT record FROM tasks")
	if err != nil {
		return fail(err)
	}
	defer rows.Close()
	var records []savedLane
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return fail(fmt.Errorf("invalid workflow record: %w", err))
		}
		var record savedLane
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return fail(err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}
	return db, records, nil
}
