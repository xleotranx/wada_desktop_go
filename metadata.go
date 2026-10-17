package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/plugins/sqlite"
)

// appMetadata is everything the app keeps between runs: the sidebar's
// projects and pins, and the settings' servers and model choices.
type appMetadata struct {
	projects    []string
	projectPins map[string]bool
	pins        map[string][]string // server id -> its chats, in pin order
	servers     []serverConn
	active      string
	modelOff    map[string]bool // modelKey of every disabled model
}

type metadataStore struct{ *sqlite.DB }

// serverTables is the version 3 schema: the connections and the models
// the user turned off, one row each. The creates tolerate a database
// whose tables are already there, as a hand-wound version number makes.
func serverTables() []sqlite.Statement {
	return []sqlite.Statement{
		{SQL: "CREATE TABLE IF NOT EXISTS servers (id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, managed INTEGER NOT NULL DEFAULT 0, url TEXT NOT NULL DEFAULT '', password TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL)"},
		{SQL: "CREATE TABLE IF NOT EXISTS disabled_models (server_id TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, PRIMARY KEY (server_id, provider, model))"},
	}
}

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
	if version > 3 {
		return fail(fmt.Errorf("metadata database version %d is newer than this app supports", version))
	}
	if version == 0 {
		statements := []sqlite.Statement{
			{SQL: "CREATE TABLE projects (directory TEXT PRIMARY KEY, position INTEGER NOT NULL, pinned INTEGER NOT NULL DEFAULT 0)"},
			{SQL: "CREATE TABLE session_pins (server_key TEXT NOT NULL, session_id TEXT NOT NULL, position INTEGER NOT NULL, PRIMARY KEY(server_key, session_id))"},
			{SQL: "CREATE TABLE preferences (key TEXT PRIMARY KEY, value TEXT NOT NULL)"},
		}
		statements = append(statements, serverTables()...)
		statements = append(statements, sqlite.Statement{SQL: "PRAGMA user_version = 3"})
		if _, err := db.Transaction(context.Background(), statements); err != nil {
			return fail(err)
		}
		return &metadataStore{db}, nil
	}
	if version == 1 {
		// The first schema seeded projects from neighboring repositories.
		// Reset that legacy list once; chat pins belong to a separate table.
		if _, err := db.Transaction(context.Background(), []sqlite.Statement{
			{SQL: "DELETE FROM projects"},
			{SQL: "PRAGMA user_version = 2"},
		}); err != nil {
			return fail(err)
		}
		version = 2
	}
	if version == 2 {
		statements := append(serverTables(), sqlite.Statement{SQL: "PRAGMA user_version = 3"})
		if _, err := db.Transaction(context.Background(), statements); err != nil {
			return fail(err)
		}
	}
	return &metadataStore{db}, nil
}

func (db *metadataStore) load(ctx context.Context) (appMetadata, error) {
	state := appMetadata{
		projectPins: map[string]bool{},
		pins:        map[string][]string{},
		modelOff:    map[string]bool{},
	}
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
	rows, err = db.Query(ctx, "SELECT server_key, session_id FROM session_pins ORDER BY server_key, position")
	if err != nil {
		return state, err
	}
	for _, row := range rows.Values {
		key, sid := row[0].(string), row[1].(string)
		state.pins[key] = append(state.pins[key], sid)
	}
	rows, err = db.Query(ctx, "SELECT id, name, kind, managed, url, password FROM servers ORDER BY position")
	if err != nil {
		return state, err
	}
	for _, row := range rows.Values {
		state.servers = append(state.servers, serverConn{
			id:       row[0].(string),
			name:     row[1].(string),
			local:    row[2].(string) == "local",
			managed:  row[3].(int64) != 0,
			url:      row[4].(string),
			password: row[5].(string),
		})
	}
	rows, err = db.Query(ctx, "SELECT server_id, provider, model FROM disabled_models")
	if err != nil {
		return state, err
	}
	for _, row := range rows.Values {
		state.modelOff[modelKey(row[0].(string), row[1].(string), row[2].(string))] = true
	}
	rows, err = db.Query(ctx, "SELECT value FROM preferences WHERE key = 'active_server'")
	if err != nil {
		return state, err
	}
	if len(rows.Values) > 0 {
		state.active = rows.Values[0][0].(string)
	}
	return state, nil
}

func (db *metadataStore) save(ctx context.Context, state appMetadata) error {
	statements := []sqlite.Statement{
		{SQL: "DELETE FROM projects"},
		{SQL: "DELETE FROM session_pins"},
		{SQL: "DELETE FROM servers"},
		{SQL: "DELETE FROM disabled_models"},
		{SQL: "INSERT OR REPLACE INTO preferences VALUES ('sidebar_initialized', '1')"},
		{SQL: "INSERT OR REPLACE INTO preferences VALUES ('active_server', ?)", Args: []any{state.active}},
	}
	for i, dir := range state.projects {
		statements = append(statements, sqlite.Statement{SQL: "INSERT INTO projects VALUES (?, ?, ?)", Args: []any{dir, i, state.projectPins[dir]}})
	}
	for key, ids := range state.pins {
		for i, id := range ids {
			statements = append(statements, sqlite.Statement{SQL: "INSERT INTO session_pins VALUES (?, ?, ?)", Args: []any{key, id, i}})
		}
	}
	for i, s := range state.servers {
		kind, managed := "remote", 0
		if s.local {
			kind = "local"
		}
		if s.managed {
			managed = 1
		}
		statements = append(statements, sqlite.Statement{
			SQL:  "INSERT INTO servers VALUES (?, ?, ?, ?, ?, ?, ?)",
			Args: []any{s.id, s.name, kind, managed, s.url, s.password, i},
		})
	}
	for key := range state.modelOff {
		serverID, rest, _ := strings.Cut(key, "\x00")
		provider, model, _ := strings.Cut(rest, "/")
		statements = append(statements, sqlite.Statement{
			SQL:  "INSERT INTO disabled_models VALUES (?, ?, ?)",
			Args: []any{serverID, provider, model},
		})
	}
	_, err := db.Transaction(ctx, statements)
	return err
}

// Pending snapshots are replaced by the newest, and shutdown drains the queue.
type metadataWrite struct {
	state   appMetadata
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

func (a *app) stateSnapshot() appMetadata {
	state := appMetadata{
		projects:    slices.Clone(a.projects),
		projectPins: map[string]bool{},
		pins:        map[string][]string{},
		servers:     slices.Clone(a.servers),
		active:      a.activeServer,
		modelOff:    map[string]bool{},
	}
	for dir, pinned := range a.projectPins {
		state.projectPins[dir] = pinned
	}
	// Entries hold chats or nothing: the store rewrites every list whole,
	// and an empty one simply has no rows.
	for key, ids := range a.pinsByServer {
		if len(ids) > 0 {
			state.pins[key] = slices.Clone(ids)
		}
	}
	if len(a.pinOrder) > 0 {
		state.pins[a.activeServer] = slices.Clone(a.pinOrder)
	}
	for key := range a.modelOff {
		state.modelOff[key] = true
	}
	return state
}

func (a *app) saveState() {
	if a.metadata == nil {
		return
	}
	a.metadataVersion++
	a.metadataSaving = true
	select {
	case <-a.metadata.queue:
	default:
	}
	a.metadata.queue <- metadataWrite{state: a.stateSnapshot(), version: a.metadataVersion}
}

func (a *app) acceptMetadataWrite(version uint64, err error) {
	if version != a.metadataVersion {
		return
	}
	a.metadataSaving = false
	a.metadataError = ""
	if err != nil {
		a.metadataError = "Changes are not saved: " + err.Error()
	}
}

func (a *app) restoreState(state appMetadata) {
	a.projects, a.projectPins = state.projects, state.projectPins
	if a.projectPins == nil {
		a.projectPins = map[string]bool{}
	}
	a.servers = state.servers
	if !a.hasServer(localServerID) {
		a.servers = append([]serverConn{defaultLocalServer()}, a.servers...)
	}
	a.activeServer = state.active
	if !a.hasServer(a.activeServer) {
		a.activeServer = localServerID
	}
	// A server the app runs itself is stopped until it is the active one.
	for _, s := range a.servers {
		if s.managed && s.id != a.activeServer {
			a.setHealth(s.id, serverHealth{status: healthStopped})
		}
	}
	a.pinsByServer = state.pins
	if a.pinsByServer == nil {
		a.pinsByServer = map[string][]string{}
	}
	a.pinOrder = slices.Clone(a.pinsByServer[a.activeServer])
	a.pinned = map[string]bool{}
	for _, id := range a.pinOrder {
		a.pinned[id] = true
	}
	a.modelOff = state.modelOff
	if a.modelOff == nil {
		a.modelOff = map[string]bool{}
	}
}

func (a *app) hasServer(id string) bool {
	_, ok := a.serverByID(id)
	return ok
}

// Startup loads only folders explicitly saved in the app.
func (a *app) loadState(path string) (*metadataStore, error) {
	db, err := openMetadata(path)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(context.Background(), "SELECT value FROM preferences WHERE key = 'sidebar_initialized'")
	if err == nil && len(rows.Values) == 0 {
		err = db.save(context.Background(), a.stateSnapshot())
	}
	var state appMetadata
	if err == nil {
		state, err = db.load(context.Background())
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	a.restoreState(state)
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
