package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/plugins/sqlite"
)

// The local server's identity survives its random port and process restarts.
const localServerKey = "local-opencode"

type sidebarMetadata struct {
	projects    []string
	pins        []string
	projectPins map[string]bool
}

type metadataStore struct{ *sqlite.DB }

func openMetadata(path string) (*metadataStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOptions{})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*metadataStore, error) { db.Close(); return nil, err }
	rows, err := db.Query(context.Background(), "PRAGMA user_version")
	if err != nil {
		return fail(err)
	}
	version := rows.Values[0][0].(int64)
	if version > 2 {
		return fail(fmt.Errorf("sidebar database version %d is newer than this app supports", version))
	}
	if version == 0 {
		_, err = db.Transaction(context.Background(), []sqlite.Statement{
			{SQL: "CREATE TABLE projects (directory TEXT PRIMARY KEY, position INTEGER NOT NULL, pinned INTEGER NOT NULL DEFAULT 0)"},
			{SQL: "CREATE TABLE session_pins (server_key TEXT NOT NULL, session_id TEXT NOT NULL, position INTEGER NOT NULL, PRIMARY KEY(server_key, session_id))"},
			{SQL: "CREATE TABLE preferences (key TEXT PRIMARY KEY, value TEXT NOT NULL)"},
			{SQL: "PRAGMA user_version = 2"},
		})
		if err != nil {
			return fail(err)
		}
	}
	if version == 1 {
		// The first schema seeded projects from neighboring repositories.
		// Reset that legacy list once; chat pins belong to a separate table.
		_, err = db.Transaction(context.Background(), []sqlite.Statement{
			{SQL: "DELETE FROM projects"},
			{SQL: "PRAGMA user_version = 2"},
		})
		if err != nil {
			return fail(err)
		}
	}
	return &metadataStore{db}, nil
}

func (db *metadataStore) load(ctx context.Context) (sidebarMetadata, error) {
	state := sidebarMetadata{projectPins: map[string]bool{}}
	rows, err := db.Query(ctx, "SELECT directory, pinned FROM projects ORDER BY position")
	if err != nil {
		return state, err
	}
	for _, row := range rows.Values {
		dir := row[0].(string)
		state.projects = append(state.projects, dir)
		if row[1].(int64) != 0 {
			state.projectPins[dir] = true
		}
	}
	rows, err = db.Query(ctx, "SELECT session_id FROM session_pins WHERE server_key = ? ORDER BY position", localServerKey)
	if err != nil {
		return state, err
	}
	for _, row := range rows.Values {
		state.pins = append(state.pins, row[0].(string))
	}
	return state, nil
}

func (db *metadataStore) save(ctx context.Context, state sidebarMetadata) error {
	statements := []sqlite.Statement{
		{SQL: "DELETE FROM projects"},
		{SQL: "DELETE FROM session_pins WHERE server_key = ?", Args: []any{localServerKey}},
		{SQL: "INSERT OR REPLACE INTO preferences VALUES ('sidebar_initialized', '1')"},
	}
	for i, dir := range state.projects {
		statements = append(statements, sqlite.Statement{SQL: "INSERT INTO projects VALUES (?, ?, ?)", Args: []any{dir, i, state.projectPins[dir]}})
	}
	for i, id := range state.pins {
		statements = append(statements, sqlite.Statement{SQL: "INSERT INTO session_pins VALUES (?, ?, ?)", Args: []any{localServerKey, id, i}})
	}
	_, err := db.Transaction(ctx, statements)
	return err
}

// Pending snapshots are replaced by the newest, and shutdown drains the queue.
type metadataWrite struct {
	state   sidebarMetadata
	version uint64
}

type metadataWriter struct {
	queue chan metadataWrite
	done  chan struct{}
}

func startMetadataWriter(db *metadataStore, publish func(uint64, error)) *metadataWriter {
	w := &metadataWriter{queue: make(chan metadataWrite, 1), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer db.Close()
		for job := range w.queue {
			err := db.save(context.Background(), job.state)
			publish(job.version, err)
		}
	}()
	return w
}

func (a *app) sidebarSnapshot() sidebarMetadata {
	state := sidebarMetadata{projects: slices.Clone(a.projects), pins: slices.Clone(a.pinOrder), projectPins: map[string]bool{}}
	for dir, pinned := range a.projectPins {
		state.projectPins[dir] = pinned
	}
	return state
}

func (a *app) saveSidebar() {
	if a.metadata == nil {
		return
	}
	a.metadataVersion++
	a.metadataSaving = true
	select {
	case <-a.metadata.queue:
	default:
	}
	a.metadata.queue <- metadataWrite{state: a.sidebarSnapshot(), version: a.metadataVersion}
}

func (a *app) acceptMetadataWrite(version uint64, err error) {
	if version != a.metadataVersion {
		return
	}
	a.metadataSaving = false
	a.metadataError = ""
	if err != nil {
		a.metadataError = "Sidebar changes are not saved: " + err.Error()
	}
}

func (a *app) restoreSidebar(state sidebarMetadata) {
	a.projects, a.pinOrder, a.projectPins = state.projects, state.pins, state.projectPins
	a.pinned = map[string]bool{}
	for _, id := range state.pins {
		a.pinned[id] = true
	}
}

// Startup loads only folders explicitly saved in the app.
func (a *app) loadSidebar(path string) (*metadataStore, error) {
	db, err := openMetadata(path)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(context.Background(), "SELECT value FROM preferences WHERE key = 'sidebar_initialized'")
	if err == nil && len(rows.Values) == 0 {
		err = db.save(context.Background(), a.sidebarSnapshot())
	}
	var state sidebarMetadata
	if err == nil {
		state, err = db.load(context.Background())
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	a.restoreSidebar(state)
	for _, dir := range a.projects {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			a.unavailableProjects[dir] = true
		}
	}
	return db, nil
}

func (a *app) closeMetadata() {
	if a.metadata == nil {
		return
	}
	close(a.metadata.queue)
	<-a.metadata.done
}
