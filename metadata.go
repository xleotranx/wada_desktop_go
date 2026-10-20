package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/plugins/sqlite"
)

// appMetadata is everything the app keeps between runs: the sidebar's
// projects and pins, the settings' servers and model choices, the widths
// the user dragged the workspace panes to, and each server's open tabs.
type appMetadata struct {
	projects    []string
	projectPins map[string]bool
	pins        map[string][]string // server id -> its chats, in pin order
	servers     []serverConn
	active      string
	modelOff    map[string]bool // modelKey of every disabled model
	sidebarW    float32
	browserW    float32
	terminalH   float32
	tabs        map[string][]chatTab // server id -> its open conversations
	activeTabs  map[string]string    // server id -> the conversation on show
}

type metadataStore struct{ *sqlite.DB }

const metadataSchema = 5

func metadataPath() (string, error) {
	if dir := os.Getenv("WADA_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "metadata.db"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wada-desktop-go", "metadata.db"), nil
}

func openMetadata(path string) (*metadataStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
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
	if version == metadataSchema {
		return &metadataStore{db}, nil
	}
	if version != 0 {
		return fail(fmt.Errorf("metadata schema %d is unsupported; run go run . --reset-metadata to discard development settings", version))
	}
	// An unversioned database with tables is not an empty application store.
	tables, err := db.Query(context.Background(), "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return fail(err)
	}
	if len(tables.Values) > 0 {
		return fail(fmt.Errorf("unversioned metadata is unsupported; run go run . --reset-metadata"))
	}
	statements := []sqlite.Statement{
		{SQL: "CREATE TABLE projects (directory TEXT PRIMARY KEY, position INTEGER NOT NULL, pinned INTEGER NOT NULL DEFAULT 0)"},
		{SQL: "CREATE TABLE session_pins (server_key TEXT NOT NULL, session_id TEXT NOT NULL, position INTEGER NOT NULL, PRIMARY KEY(server_key,session_id))"},
		{SQL: "CREATE TABLE preferences (key TEXT PRIMARY KEY,value TEXT NOT NULL)"},
		{SQL: "CREATE TABLE servers (id TEXT PRIMARY KEY,name TEXT NOT NULL,kind TEXT NOT NULL,managed INTEGER NOT NULL DEFAULT 0,url TEXT NOT NULL DEFAULT '',password TEXT NOT NULL DEFAULT '',position INTEGER NOT NULL,paired INTEGER NOT NULL DEFAULT 0,paired_at INTEGER NOT NULL DEFAULT 0)"},
		{SQL: "CREATE TABLE disabled_models (server_id TEXT NOT NULL,provider TEXT NOT NULL,model TEXT NOT NULL,PRIMARY KEY(server_id,provider,model))"},
		{SQL: "PRAGMA user_version = 5"},
	}
	if _, err := db.Transaction(context.Background(), statements); err != nil {
		return fail(err)
	}
	return &metadataStore{db}, nil
}

// resetMetadata is an explicit development action, never an open-error fallback.
func resetMetadata(path string) error {
	if filepath.Base(path) != "metadata.db" {
		return fmt.Errorf("reset requires the app's metadata.db path")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		target := path + suffix
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to reset non-regular metadata file %s", target)
		}
	}
	for _, suffix := range []string{"-wal", "-shm", ""} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
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
	rows, err = db.Query(ctx, "SELECT id, name, kind, managed, url, password, paired, paired_at FROM servers ORDER BY position")
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
			paired:   row[6].(int64) != 0,
			pairedAt: row[7].(int64),
		})
	}
	rows, err = db.Query(ctx, "SELECT server_id, provider, model FROM disabled_models")
	if err != nil {
		return state, err
	}
	for _, row := range rows.Values {
		state.modelOff[modelKey(row[0].(string), row[1].(string), row[2].(string))] = true
	}
	rows, err = db.Query(ctx, "SELECT key, value FROM preferences")
	if err != nil {
		return state, err
	}
	for _, row := range rows.Values {
		key, value := row[0].(string), row[1].(string)
		switch key {
		case "active_server":
			state.active = value
		case "sidebar_width":
			if width, err := strconv.ParseFloat(value, 32); err == nil && width > 0 {
				state.sidebarW = float32(width)
			}
		case "browser_width":
			if width, err := strconv.ParseFloat(value, 32); err == nil && width > 0 {
				state.browserW = float32(width)
			}
		case "terminal_height":
			if height, err := strconv.ParseFloat(value, 32); err == nil && height > 0 {
				state.terminalH = float32(height)
			}
		default:
			switch {
			case strings.HasPrefix(key, "tabs:"):
				if tabs, ok := decodeTabs(value); ok {
					if state.tabs == nil {
						state.tabs = map[string][]chatTab{}
					}
					state.tabs[strings.TrimPrefix(key, "tabs:")] = tabs
				}
			case strings.HasPrefix(key, "active_tab:"):
				if value != "" {
					if state.activeTabs == nil {
						state.activeTabs = map[string]string{}
					}
					state.activeTabs[strings.TrimPrefix(key, "active_tab:")] = value
				}
			}
		}
	}
	return state, nil
}

func (db *metadataStore) save(ctx context.Context, state appMetadata) error {
	statements := []sqlite.Statement{
		{SQL: "DELETE FROM projects"},
		{SQL: "DELETE FROM session_pins"},
		{SQL: "DELETE FROM servers"},
		{SQL: "DELETE FROM disabled_models"},
		{SQL: "DELETE FROM preferences WHERE key LIKE 'tabs:%' OR key LIKE 'active_tab:%'"},
		{SQL: "INSERT OR REPLACE INTO preferences VALUES ('active_server', ?)", Args: []any{state.active}},
		{SQL: "INSERT OR REPLACE INTO preferences VALUES ('sidebar_width', ?)", Args: []any{formatWidth(state.sidebarW)}},
		{SQL: "INSERT OR REPLACE INTO preferences VALUES ('browser_width', ?)", Args: []any{formatWidth(state.browserW)}},
		{SQL: "INSERT OR REPLACE INTO preferences VALUES ('terminal_height', ?)", Args: []any{formatWidth(state.terminalH)}},
	}
	for serverID, tabs := range state.tabs {
		statements = append(statements, sqlite.Statement{
			SQL:  "INSERT OR REPLACE INTO preferences VALUES (?, ?)",
			Args: []any{"tabs:" + serverID, encodeTabs(tabs)},
		})
	}
	for serverID, id := range state.activeTabs {
		statements = append(statements, sqlite.Statement{
			SQL:  "INSERT OR REPLACE INTO preferences VALUES (?, ?)",
			Args: []any{"active_tab:" + serverID, id},
		})
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
		kind := "remote"
		if s.local {
			kind = "local"
		}
		statements = append(statements, sqlite.Statement{
			SQL: "INSERT INTO servers (id, name, kind, managed, url, password, paired, paired_at, position) " +
				"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			Args: []any{s.id, s.name, kind, s.managed, s.url, s.password, s.paired, s.pairedAt, i},
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

// formatWidth keeps a pane width in the preferences table as a stable
// decimal, so a restart reads back the size the divider last showed.
func formatWidth(width float32) string {
	return strconv.FormatFloat(float64(width), 'f', -1, 32)
}

// storedTab is a chat tab as it sits in the preferences table.
type storedTab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// decodeTabs reads one server's tab list, treating a malformed row as no
// tabs rather than failing the whole store.
func decodeTabs(value string) ([]chatTab, bool) {
	var stored []storedTab
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return nil, false
	}
	tabs := make([]chatTab, 0, len(stored))
	for _, tab := range stored {
		if tab.ID == "" {
			continue
		}
		tabs = append(tabs, chatTab{id: tab.ID, title: tab.Title})
	}
	return tabs, true
}

// encodeTabs writes a server's tab list for the next launch.
func encodeTabs(tabs []chatTab) string {
	stored := make([]storedTab, 0, len(tabs))
	for _, tab := range tabs {
		stored = append(stored, storedTab{ID: tab.id, Title: tab.title})
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return "[]"
	}
	return string(data)
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
		projects:    slices.Clone(a.preferences.projects),
		projectPins: map[string]bool{},
		pins:        map[string][]string{},
		servers:     slices.Clone(a.connections.servers),
		active:      a.connections.activeServer,
		modelOff:    map[string]bool{},
		sidebarW:    a.layout.sidebarW,
		browserW:    a.layout.browserW,
		terminalH:   a.terminal.height,
	}
	for dir, pinned := range a.preferences.projectPins {
		state.projectPins[dir] = pinned
	}
	// Entries hold chats or nothing: the store rewrites every list whole,
	// and an empty one simply has no rows.
	for key, scope := range a.connections.scopes {
		if len(scope.pinOrder) > 0 {
			state.pins[key] = slices.Clone(scope.pinOrder)
		}
		if len(scope.tabs) > 0 {
			if state.tabs == nil {
				state.tabs = map[string][]chatTab{}
			}
			state.tabs[key] = slices.Clone(scope.tabs)
		}
		if scope.activeTab != "" {
			if state.activeTabs == nil {
				state.activeTabs = map[string]string{}
			}
			state.activeTabs[key] = scope.activeTab
		}
	}
	for key := range a.connections.modelOff {
		state.modelOff[key] = true
	}
	return state
}

func (a *app) saveState() {
	if a.preferences.metadata == nil {
		return
	}
	a.preferences.metadataVersion++
	a.preferences.metadataSaving = true
	select {
	case <-a.preferences.metadata.queue:
	default:
	}
	a.preferences.metadata.queue <- metadataWrite{state: a.stateSnapshot(), version: a.preferences.metadataVersion}
}

func (a *app) acceptMetadataWrite(version uint64, err error) {
	if a.runtime.closed || version != a.preferences.metadataVersion {
		return
	}
	a.preferences.metadataSaving = false
	a.preferences.metadataError = ""
	if err != nil {
		a.preferences.metadataError = "Changes are not saved: " + err.Error()
	}
}

func (a *app) restoreState(state appMetadata) {
	a.preferences.projects, a.preferences.projectPins = state.projects, state.projectPins
	if a.preferences.projectPins == nil {
		a.preferences.projectPins = map[string]bool{}
	}
	// A stored width outside the panes' limits is a layout this version no
	// longer shows; the default takes over rather than a clipped divider.
	if state.sidebarW > 0 {
		a.layout.sidebarW = min(max(state.sidebarW, sidebarMinWidth), sidebarMaxWidth)
	}
	if state.browserW > 0 {
		a.layout.browserW = min(max(state.browserW, browserMinWidth), browserMaxWidth)
	}
	if state.terminalH > 0 {
		a.terminal.height = min(max(state.terminalH, terminalMinHeight), terminalMaxHeight)
	}
	a.connections.servers = state.servers
	if !a.hasServer(localServerID) {
		a.connections.servers = append([]serverConn{defaultLocalServer()}, a.connections.servers...)
	}
	a.connections.activeServer = state.active
	if !a.hasServer(a.connections.activeServer) {
		a.connections.activeServer = localServerID
	}
	// A server the app runs itself is stopped until it is the active one.
	for _, s := range a.connections.servers {
		if s.managed && s.id != a.connections.activeServer {
			a.setHealth(s.id, serverHealth{status: healthStopped})
		}
	}
	for id, pins := range state.pins {
		a.scopeFor(id).setPins(pins)
	}
	for id, tabs := range state.tabs {
		a.scopeFor(id).tabs = tabs
	}
	for id, active := range state.activeTabs {
		a.scopeFor(id).activeTab = active
	}
	a.connections.modelOff = state.modelOff
	if a.connections.modelOff == nil {
		a.connections.modelOff = map[string]bool{}
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
	state, err := db.load(context.Background())
	if err != nil {
		db.Close()
		return nil, err
	}
	a.restoreState(state)
	for _, dir := range a.preferences.projects {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			a.preferences.unavailableProjects[dir] = true
		}
	}
	return db, nil
}

func (a *app) closeMetadata() {
	if a.preferences.metadata == nil {
		return
	}
	close(a.preferences.metadata.queue)
	<-a.preferences.metadata.done
}
