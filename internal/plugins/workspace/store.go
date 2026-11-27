package workspace

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/store"
)

var workspaceMigrations = []store.Migration{{
	Version: 1,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range []string{
			`CREATE TABLE folders (
				path      TEXT PRIMARY KEY,
				collapsed INTEGER NOT NULL,
				added     INTEGER NOT NULL)`,
			`CREATE TABLE conversations (
				origin         TEXT NOT NULL,
				session        TEXT NOT NULL,
				folder         TEXT NOT NULL,
				title          TEXT NOT NULL,
				model_provider TEXT NOT NULL,
				model_id       TEXT NOT NULL,
				model_variant  TEXT NOT NULL,
				created        INTEGER NOT NULL,
				updated        INTEGER NOT NULL,
				PRIMARY KEY (origin, session))`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	},
}}

// key names a conversation: a session ID means something only under the
// backend origin it came from.
type key struct {
	origin string
	id     agent.SessionID
}

// snapshot is what the database held when it opened.
type snapshot struct {
	folders []workspace.Folder
	convs   map[key]workspace.Conversation
}

// store saves the record to SQLite from one worker. The UI thread queues
// statements; the worker writes them in order, one transaction per wake, and
// whatever is still queued when its lifetime ends. Every statement is an
// upsert or a delete, so a failed batch is written again with the next one.
type storeQueue struct {
	path  string
	mu    sync.Mutex
	queue []store.Statement
	dead  bool // the database did not open; nothing is queued any more
	wake  chan struct{}
}

func newStore(path string) *storeQueue { return &storeQueue{path: path, wake: make(chan struct{}, 1)} }

// enqueue is called on the UI thread.
func (s *storeQueue) enqueue(statements ...store.Statement) {
	s.mu.Lock()
	if !s.dead {
		s.queue = append(s.queue, statements...)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *storeQueue) take() []store.Statement {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queue
	s.queue = nil
	return q
}

func (s *storeQueue) putBack(batch []store.Statement) {
	s.mu.Lock()
	s.queue = append(batch, s.queue...)
	s.mu.Unlock()
}

// run is the worker: it opens the database, reports its contents, then writes
// queued changes until ctx ends. loaded and failed are called on the worker.
func (s *storeQueue) run(ctx context.Context, loaded func(snapshot, error), failed func(error)) {
	db, snap, err := open(ctx, s.path)
	if err != nil {
		s.mu.Lock()
		s.dead, s.queue = true, nil
		s.mu.Unlock()
		loaded(snapshot{}, err)
		return
	}
	defer db.Close()
	loaded(snap, nil)
	write := func() error {
		batch := s.take()
		if len(batch) == 0 {
			return nil
		}
		// Not ctx: a write under way finishes even as the plugin goes.
		wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.ExecTx(wctx, db, batch); err != nil {
			s.putBack(batch)
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
			_ = write() // the last changes; nobody is left to tell
			return
		}
	}
}

// open opens or creates the workspace database at path and applies migrations.
func open(ctx context.Context, path string) (*sql.DB, snapshot, error) {
	db, err := store.Open(ctx, path, "workspace", workspaceMigrations)
	if err != nil {
		return nil, snapshot{}, err
	}
	snap, err := read(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, snapshot{}, err
	}
	return db, snap, nil
}

func read(ctx context.Context, db *sql.DB) (snapshot, error) {
	snap := snapshot{convs: map[key]workspace.Conversation{}}
	rows, err := db.QueryContext(ctx, "SELECT path, added FROM folders")
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var path string
		var added int64
		if err := rows.Scan(&path, &added); err != nil {
			_ = rows.Close()
			return snap, err
		}
		snap.folders = append(snap.folders, workspace.Folder{Path: path, Added: added})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return snap, err
	}
	_ = rows.Close()

	rows, err = db.QueryContext(ctx, `SELECT origin, session, folder, title, model_provider, model_id, model_variant,
		created, updated FROM conversations`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var origin, session, folder, title, provider, modelID, variant string
		var created, updated int64
		if err := rows.Scan(&origin, &session, &folder, &title, &provider, &modelID, &variant, &created, &updated); err != nil {
			return snap, err
		}
		k := key{origin: origin, id: agent.SessionID(session)}
		snap.convs[k] = workspace.Conversation{
			ID:      k.id,
			Folder:  folder,
			Title:   title,
			Model:   agent.ModelRef{Provider: provider, ID: modelID, Variant: variant},
			Created: created,
			Updated: updated,
		}
	}
	return snap, rows.Err()
}

// ---- Statements

// putFolder records a folder. The collapsed column is from an earlier
// layout of schema 1 and always 0.
func putFolder(f workspace.Folder) store.Statement {
	return store.Statement{
		SQL:  "INSERT INTO folders (path, collapsed, added) VALUES (?, 0, ?) ON CONFLICT (path) DO NOTHING",
		Args: []any{f.Path, f.Added}}
}

// dropFolder forgets a folder and its conversations under every origin.
func dropFolder(path string) []store.Statement {
	return []store.Statement{
		{SQL: "DELETE FROM folders WHERE path = ?", Args: []any{path}},
		{SQL: "DELETE FROM conversations WHERE folder = ?", Args: []any{path}},
	}
}

func putConversation(k key, c workspace.Conversation) store.Statement {
	return store.Statement{
		SQL: `INSERT INTO conversations (origin, session, folder, title, model_provider, model_id, model_variant, created, updated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (origin, session) DO UPDATE SET folder = excluded.folder, title = excluded.title,
				model_provider = excluded.model_provider, model_id = excluded.model_id,
				model_variant = excluded.model_variant, updated = excluded.updated`,
		Args: []any{k.origin, string(k.id), c.Folder, c.Title, c.Model.Provider, c.Model.ID, c.Model.Variant, c.Created, c.Updated}}
}

func dropConversation(k key) store.Statement {
	return store.Statement{SQL: "DELETE FROM conversations WHERE origin = ? AND session = ?", Args: []any{k.origin, string(k.id)}}
}
