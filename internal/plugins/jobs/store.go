package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"wada-desktop-go/internal/contract/jobs"

	"github.com/egoist/mygo/plugins/sqlite"
)

// store keeps the job list in jobs.db: one row per job, the record as JSON so
// the shape of a job can grow without a migration.
type store struct {
	path string
	mu   sync.Mutex
	// queue holds jobs to write and ids to delete, appended by the UI thread
	// and drained by run.
	queue   []jobs.Job
	removed []string
	wake    chan struct{}
}

func newStore(path string) *store { return &store{path: path, wake: make(chan struct{}, 1)} }

// put queues a job to be written. Call it on the UI thread.
func (s *store) put(job jobs.Job) {
	s.mu.Lock()
	s.queue = append(s.queue, job)
	s.mu.Unlock()
	s.signal()
}

// remove queues a job to be deleted. Call it on the UI thread.
func (s *store) remove(id string) {
	s.mu.Lock()
	s.removed = append(s.removed, id)
	s.mu.Unlock()
	s.signal()
}

// signal wakes the writer.
func (s *store) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// run opens the database, reports what it held, then writes what put queues
// until the context ends. It runs on its own worker.
func (s *store) run(ctx context.Context, loaded func([]jobs.Job, error), failed func(error)) {
	db, records, err := openStore(ctx, s.path)
	if err != nil {
		loaded(nil, err)
		return
	}
	defer db.Close()
	loaded(records, nil)
	write := func() error {
		s.mu.Lock()
		batch, removed := s.queue, s.removed
		s.queue, s.removed = nil, nil
		s.mu.Unlock()
		if len(batch) == 0 && len(removed) == 0 {
			return nil
		}
		statements := make([]sqlite.Statement, 0, len(batch)+len(removed))
		for _, job := range batch {
			raw, err := json.Marshal(job)
			if err != nil {
				return err
			}
			statements = append(statements, sqlite.Statement{
				SQL:  "INSERT INTO jobs(id,record) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET record=excluded.record",
				Args: []any{job.ID, string(raw)},
			})
		}
		for _, id := range removed {
			statements = append(statements, sqlite.Statement{SQL: "DELETE FROM jobs WHERE id=?", Args: []any{id}})
		}
		wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.Transaction(wctx, statements); err != nil {
			s.mu.Lock()
			s.queue = append(batch, s.queue...)
			s.removed = append(removed, s.removed...)
			s.mu.Unlock()
			return err
		}
		return nil
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

// openStore opens jobs.db, creating it on first use and refusing another
// plugin's database.
func openStore(ctx context.Context, path string) (*sqlite.DB, []jobs.Job, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, err
	}
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*sqlite.DB, []jobs.Job, error) {
		db.Close()
		return nil, nil, err
	}
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
			return fail(fmt.Errorf("%s is not a jobs database", path))
		}
		_, err = db.Transaction(ctx, []sqlite.Statement{
			{SQL: "CREATE TABLE jobs(id TEXT NOT NULL PRIMARY KEY,record TEXT NOT NULL)"},
			{SQL: "PRAGMA user_version=1"},
		})
		if err != nil {
			return fail(err)
		}
	case 1:
	default:
		return fail(fmt.Errorf("jobs schema %d is unsupported", version))
	}
	rows, err = db.Query(ctx, "SELECT record FROM jobs")
	if err != nil {
		return fail(err)
	}
	var records []jobs.Job
	for _, row := range rows.Values {
		raw, ok := row[0].(string)
		if !ok {
			return fail(fmt.Errorf("invalid job record"))
		}
		var job jobs.Job
		if err := json.Unmarshal([]byte(raw), &job); err != nil {
			return fail(err)
		}
		records = append(records, job)
	}
	return db, records, nil
}
