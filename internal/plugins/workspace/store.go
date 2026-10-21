package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/plugins/sqlite"
)

const schema = 1

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
type store struct {
	path  string
	mu    sync.Mutex
	queue []sqlite.Statement
	dead  bool // the database did not open; nothing is queued any more
	wake  chan struct{}
}

func newStore(path string) *store { return &store{path: path, wake: make(chan struct{}, 1)} }

// enqueue is called on the UI thread.
func (s *store) enqueue(statements ...sqlite.Statement) {
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

func (s *store) take() []sqlite.Statement {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queue
	s.queue = nil
	return q
}

func (s *store) putBack(batch []sqlite.Statement) {
	s.mu.Lock()
	s.queue = append(batch, s.queue...)
	s.mu.Unlock()
}

// run is the worker: it opens the database, reports its contents, then writes
// queued changes until ctx ends. loaded and failed are called on the worker.
func (s *store) run(ctx context.Context, loaded func(snapshot, error), failed func(error)) {
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
		if _, err := db.Transaction(wctx, batch); err != nil {
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

// open opens or creates the database at path. It refuses a database of
// another schema rather than changing it.
func open(ctx context.Context, path string) (*sqlite.DB, snapshot, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, snapshot{}, err
	}
	db, err := sqlite.Open(ctx, path, sqlite.OpenOptions{})
	if err != nil {
		return nil, snapshot{}, err
	}
	fail := func(err error) (*sqlite.DB, snapshot, error) { db.Close(); return nil, snapshot{}, err }
	rows, err := db.Query(ctx, "PRAGMA user_version")
	if err != nil {
		return fail(err)
	}
	switch version, _ := rows.Values[0][0].(int64); version {
	case schema:
	case 0:
		tables, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
		if err != nil {
			return fail(err)
		}
		if len(tables.Values) > 0 {
			return fail(fmt.Errorf("%s is not a workspace database", path))
		}
		if _, err := db.Transaction(ctx, []sqlite.Statement{
			{SQL: `CREATE TABLE folders (
				path      TEXT PRIMARY KEY,
				collapsed INTEGER NOT NULL,
				added     INTEGER NOT NULL)`},
			{SQL: `CREATE TABLE conversations (
				origin         TEXT NOT NULL,
				session        TEXT NOT NULL,
				folder         TEXT NOT NULL,
				title          TEXT NOT NULL,
				model_provider TEXT NOT NULL,
				model_id       TEXT NOT NULL,
				model_variant  TEXT NOT NULL,
				created        INTEGER NOT NULL,
				updated        INTEGER NOT NULL,
				PRIMARY KEY (origin, session))`},
			{SQL: fmt.Sprintf("PRAGMA user_version = %d", schema)},
		}); err != nil {
			return fail(err)
		}
	default:
		return fail(fmt.Errorf("%s has workspace schema %d; this build reads %d", path, version, schema))
	}
	snap, err := read(ctx, db)
	if err != nil {
		return fail(err)
	}
	return db, snap, nil
}

func read(ctx context.Context, db *sqlite.DB) (snapshot, error) {
	snap := snapshot{convs: map[key]workspace.Conversation{}}
	rows, err := db.Query(ctx, "SELECT path, collapsed, added FROM folders")
	if err != nil {
		return snap, err
	}
	for _, r := range rows.Values {
		path, _ := r[0].(string)
		collapsed, _ := r[1].(int64)
		added, _ := r[2].(int64)
		snap.folders = append(snap.folders, workspace.Folder{Path: path, Collapsed: collapsed != 0, Added: added})
	}
	rows, err = db.Query(ctx, `SELECT origin, session, folder, title, model_provider, model_id, model_variant,
		created, updated FROM conversations`)
	if err != nil {
		return snap, err
	}
	for _, r := range rows.Values {
		text := func(i int) string { s, _ := r[i].(string); return s }
		number := func(i int) int64 { n, _ := r[i].(int64); return n }
		k := key{origin: text(0), id: agent.SessionID(text(1))}
		snap.convs[k] = workspace.Conversation{ID: k.id, Folder: text(2), Title: text(3),
			Model:   agent.ModelRef{Provider: text(4), ID: text(5), Variant: text(6)},
			Created: number(7), Updated: number(8)}
	}
	return snap, nil
}

// ---- Statements

func putFolder(f workspace.Folder) sqlite.Statement {
	collapsed := int64(0)
	if f.Collapsed {
		collapsed = 1
	}
	return sqlite.Statement{
		SQL:  "INSERT INTO folders (path, collapsed, added) VALUES (?, ?, ?) ON CONFLICT (path) DO UPDATE SET collapsed = excluded.collapsed",
		Args: []any{f.Path, collapsed, f.Added}}
}

// dropFolder forgets a folder and its conversations under every origin.
func dropFolder(path string) []sqlite.Statement {
	return []sqlite.Statement{
		{SQL: "DELETE FROM folders WHERE path = ?", Args: []any{path}},
		{SQL: "DELETE FROM conversations WHERE folder = ?", Args: []any{path}},
	}
}

func putConversation(k key, c workspace.Conversation) sqlite.Statement {
	return sqlite.Statement{
		SQL: `INSERT INTO conversations (origin, session, folder, title, model_provider, model_id, model_variant, created, updated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (origin, session) DO UPDATE SET folder = excluded.folder, title = excluded.title,
				model_provider = excluded.model_provider, model_id = excluded.model_id,
				model_variant = excluded.model_variant, updated = excluded.updated`,
		Args: []any{k.origin, string(k.id), c.Folder, c.Title, c.Model.Provider, c.Model.ID, c.Model.Variant, c.Created, c.Updated}}
}

func dropConversation(k key) sqlite.Statement {
	return sqlite.Statement{SQL: "DELETE FROM conversations WHERE origin = ? AND session = ?", Args: []any{k.origin, string(k.id)}}
}
