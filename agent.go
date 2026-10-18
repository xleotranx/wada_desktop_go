package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// agent drives a headless opencode server: the app spawns `opencode serve`
// for the local one or dials a server it was pointed at, speaks its /api
// HTTP surface and streams its /api/event SSE feed into the transcript, so
// the real agent answers where the demo copy used to.
type agent struct {
	base    string // like http://127.0.0.1:4096/api
	pw      string // the password this app connects with
	paired  bool   // that password is a pairing token, which lapses on its own
	http    *http.Client
	cmd     *exec.Cmd   // the process the app started; nil for a dialed server
	addr    string      // where it listens, for Settings to show
	facts   serverFacts // what its /info said about itself
	bodyCap int64       // how much of one response to keep; 0 for defaultBodyCap
}

// defaultBodyCap is how much of one API response the client keeps: a stored
// conversation with images in it is megabytes, but not endless.
const defaultBodyCap int64 = 32 << 20

// agentEvent is one line of the opencode event stream, decoded to its
// envelope; the payload sits in Data and its shape depends on Type.
type agentEvent struct {
	Type    string          `json:"type"`
	Created int64           `json:"created"`
	Data    json.RawMessage `json:"data"`
}

// standaloneAgent is one connection's client without a managed process:
// the server the app dials stands on its own.
func standaloneAgent(cfg serverConn) (*agent, error) {
	base, err := apiBase(cfg.url)
	if err != nil {
		return nil, err
	}
	return &agent{base: base, pw: cfg.password, paired: cfg.paired, http: httpClient(),
		addr: strings.TrimSuffix(base, "/api")}, nil
}

// startAgentFor brings one configured server up: it spawns opencode serve
// for a connection the app manages, and dials any other, waiting for its
// API to answer either way.
func startAgentFor(ctx context.Context, cfg serverConn, dir string) (*agent, error) {
	if cfg.managed {
		return startLocalAgent(ctx, cfg, dir)
	}
	a, err := standaloneAgent(cfg)
	if err != nil {
		return nil, err
	}
	facts, err := a.waitReady(ctx, 8*time.Second)
	if err != nil {
		return nil, err
	}
	a.facts = facts
	return a, nil
}

// startLocalAgent spawns opencode serve on a free port with the app's password
// (generated when settings hold none). The server dies with this process
// (Pdeathsig), so dev rebuilds don't leave orphans behind.
func startLocalAgent(ctx context.Context, cfg serverConn, dir string) (*agent, error) {
	exe, err := exec.LookPath("opencode")
	if err != nil {
		return nil, fmt.Errorf("opencode not found in PATH: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	pw := cfg.password
	if pw == "" {
		pw = serverKey()
	}

	cmd := exec.CommandContext(ctx, exe, "serve",
		"--hostname", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+pw)
	// serve prints its random password only when none is set; ours is
	// already in cmd.Env, so the app never parses that line.
	ownChild(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	a := &agent{
		base: "http://127.0.0.1:" + strconv.Itoa(port) + "/api",
		addr: "http://127.0.0.1:" + strconv.Itoa(port),
		pw:   pw,
		http: httpClient(),
		cmd:  cmd,
	}
	facts, err := a.waitReady(ctx, 20*time.Second)
	if err != nil {
		cmd.Process.Kill()
		return nil, err
	}
	a.facts = facts
	return a, nil
}

// stop ends the server the app started; a server it was only pointed at
// keeps running.
func (a *agent) stop() {
	if a.cmd != nil && a.cmd.Process != nil {
		a.cmd.Process.Kill()
	}
}

// waitReady polls /api/info until the server names its version, the
// deadline passes or the app walks away.
func (a *agent) waitReady(ctx context.Context, within time.Duration) (serverFacts, error) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return serverFacts{}, ctx.Err()
		default:
		}
		raw, err := a.call(ctx, "GET", "/info", nil)
		if err == nil {
			if facts := readInfo(raw); facts.version != "" {
				return facts, nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return serverFacts{}, fmt.Errorf("the server did not come up in %s", within)
}

// freePort finds a port for serve by reserving and releasing one.
func freePort() (int, error) {
	row, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer row.Close()
	return row.Addr().(*net.TCPAddr).Port, nil
}

// call is one API round trip. opencode answers errors as tagged JSON bodies
// like {"_tag":…,"message":…}, which this turns into Go errors. A response
// is read up to bodyCap, or defaultBodyCap when the client carries none: a
// stored conversation with images in it is megabytes, but not endless, and
// a body cut short is refused rather than handed to the JSON decoder.
func (a *agent) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	limit := a.bodyCap
	if limit <= 0 {
		limit = defaultBodyCap
	}
	var buf bytes.Buffer
	// Raw bytes go out as the server's file API asks for them.
	_, isRaw := body.(json.RawMessage)
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, &buf)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", a.pw)
	if isRaw {
		req.Header.Set("Content-Type", "application/octet-stream")
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s %s: the response is larger than %d MiB", method, path, limit>>20)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s", refusedLine(a.paired))
	}
	if resp.StatusCode >= 300 {
		var tagged struct {
			Tag     string `json:"_tag"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &tagged) == nil && tagged.Message != "" {
			return nil, fmt.Errorf("%s: %s", tagged.Tag, tagged.Message)
		}
		return nil, fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	return raw, nil
}

// createSession opens a fresh conversation for this app, working in the
// directory given: the server's own when none is named.
func (a *agent) createSession(ctx context.Context, dir string) (string, agentModelRef, error) {
	var body any = struct{}{}
	if dir != "" {
		body = map[string]any{
			"location": map[string]any{"directory": dir},
		}
	}
	raw, err := a.call(ctx, "POST", "/session", body)
	if err != nil {
		return "", agentModelRef{}, err
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
			Model struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
				Variant    string `json:"variant"`
			} `json:"model"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", agentModelRef{}, err
	}
	return out.Data.ID, agentModelRef{providerID: out.Data.Model.ProviderID,
		id: out.Data.Model.ID, variant: out.Data.Model.Variant}, nil
}

// prompt queues one user turn; the answer streams back over events, so the
// call only needs to enqueue, not wait.
func (a *agent) prompt(ctx context.Context, sessionID, text string) error {
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/prompt",
		map[string]any{"text": text})
	return err
}

// sessionModel is the model every conversation this app opens runs on: a
// zero-cost one, so live turns never spend tokens, unless WADA_MODEL asks
// for something else ("provider/model").
func sessionModel() (string, string) {
	if env := os.Getenv("WADA_MODEL"); env != "" {
		prov, name, ok := strings.Cut(env, "/")
		if ok && prov != "" && name != "" {
			return prov, name
		}
	}
	return "opencode-go", "longcat-2.5-preview-free"
}

// setModel points one conversation at a model of the catalog, with its
// reasoning effort when it has one. The switch folds back over the
// session.model.selected event.
func (a *agent) setModel(ctx context.Context, sessionID, providerID, id, variant string) error {
	ref := map[string]any{"providerID": providerID, "id": id}
	if variant != "" {
		ref["variant"] = variant
	}
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/model",
		map[string]any{"model": ref})
	return err
}

// interrupt stops the conversation's running turn; the server answers
// {"interrupted":…} and says so again over the event stream.
func (a *agent) interrupt(ctx context.Context, sessionID string) error {
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/interrupt", struct{}{})
	return err
}

// replyPermission answers one approval the agent asked for. Once the
// server takes it, the turn carries on ("always" remembers the pattern),
// and the card leaves the transcript.
func (a *agent) replyPermission(ctx context.Context, sessionID, requestID, decision string) error {
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/permission/"+requestID+"/reply",
		map[string]any{"decision": decision})
	return err
}

// renameSession sets one conversation's title; the rename comes back as
// an event, which folds it into the sidebar row.
func (a *agent) renameSession(ctx context.Context, sessionID, title string) error {
	_, err := a.call(ctx, "PATCH", "/session/"+sessionID, map[string]any{"title": title})
	return err
}

// deleteSession drops one conversation from the store for good.
func (a *agent) deleteSession(ctx context.Context, sessionID string) error {
	_, err := a.call(ctx, "DELETE", "/session/"+sessionID, nil)
	return err
}

// agentSession is the sidebar row of one real conversation, with the
// folder it works in — the list spans every project the store knows — and
// what it has spent so far.
type agentSession struct {
	id      string
	title   string
	dir     string
	updated int64
	model   agentModelRef // what it runs on
	cost    float64       // what its turns have spent, in dollars
}

// agentModelRef is the model a conversation points at, as stored rows
// and events both name it: a provider detail of which reasoning effort.
type agentModelRef struct {
	providerID string
	id         string
	variant    string
}

// agentModel is one entry of the server's catalog, with whether running
// it costs anything — the picker marks free models.
type agentModel struct {
	providerID string
	id         string
	name       string
	variants   []string
	free       bool
}

// listModels reads the catalog the signed-in server offers.
func (a *agent) listModels(ctx context.Context) ([]agentModel, error) {
	raw, err := a.call(ctx, "GET", "/model", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
			Name       string `json:"name"`
			Variants   []struct {
				ID string `json:"id"`
			} `json:"variants"`
			Cost []struct {
				Input  float64 `json:"input"`
				Output float64 `json:"output"`
			} `json:"cost"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	all := []agentModel{}
	for _, m := range out.Data {
		row := agentModel{id: m.ID, providerID: m.ProviderID, name: m.Name, free: true}
		for _, v := range m.Variants {
			row.variants = append(row.variants, v.ID)
		}
		for _, c := range m.Cost {
			if c.Input != 0 || c.Output != 0 {
				row.free = false
			}
		}
		all = append(all, row)
	}
	return all, nil
}

// defaultModel reads the model the server itself would run, for a client
// whose own pick has no home in the catalog: a remote server's default
// belongs to that box the way its providers do.
func (a *agent) defaultModel(ctx context.Context) (agentModelRef, error) {
	raw, err := a.call(ctx, "GET", "/model/default", nil)
	if err != nil {
		return agentModelRef{}, err
	}
	var out struct {
		Data struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
			Variant    string `json:"variant"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return agentModelRef{}, err
	}
	if out.Data.ID == "" || out.Data.ProviderID == "" {
		return agentModelRef{}, fmt.Errorf("the server named no default model")
	}
	return agentModelRef{providerID: out.Data.ProviderID, id: out.Data.ID, variant: out.Data.Variant}, nil
}

// listProjects reads the folders the server works in: one directory per
// project it knows, the ones its own activity tracked most recently
// first, kept in that order. opencode answers the list behind its data
// envelope or as a bare array, depending on the build; both read the
// same. The server remembers every directory any of its callers named —
// a draft pointed at another machine's folder, a probe of a path that is
// not there, another client's look around — and keeps the row with no
// way to drop it, so each name is asked of the server's disk once here,
// and the ones that are not on it step aside.
func (a *agent) listProjects(ctx context.Context) ([]string, error) {
	raw, err := a.call(ctx, "GET", "/project", nil)
	if err != nil {
		return nil, err
	}
	type projectRow struct {
		Canonical string `json:"canonical"`
		Time      struct {
			Active int64 `json:"active"`
		} `json:"time"`
	}
	var rows []projectRow
	var out struct {
		Data []projectRow `json:"data"`
	}
	err = json.Unmarshal(raw, &rows)
	if err != nil {
		err = json.Unmarshal(raw, &out)
		rows = out.Data
	}
	if err != nil {
		return nil, fmt.Errorf("the project list did not decode")
	}
	// The activity the server tracks is what the section shows by: a
	// folder a chat has just run in moves up, and rows it wrote no
	// activity for keep the order it listed them in.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Time.Active > rows[j].Time.Active })
	seen := map[string]bool{}
	uniq := []string{}
	for _, p := range rows {
		dir := canonDir(p.Canonical)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		uniq = append(uniq, dir)
	}
	// The glances go out side by side: a dial waits on all of them, and a
	// slow round trip the list's length times would hold the door.
	exists := make([]bool, len(uniq))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, dir := range uniq {
		wg.Add(1)
		go func(i int, dir string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			exists[i] = a.locationExists(ctx, dir)
		}(i, dir)
	}
	wg.Wait()
	dirs := []string{}
	for i, dir := range uniq {
		if exists[i] {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// locationExists asks the server's file system whether one workspace
// lives on its disk. Only directories the project list already carries
// come asking, so the glance itself registers nothing the list lacks.
func (a *agent) locationExists(ctx context.Context, dir string) bool {
	_, err := a.call(ctx, "GET", "/fs/list?location[directory]="+url.QueryEscape(dir), nil)
	return err == nil
}

// agentConfigEntry is one configuration document a workspace stacks up,
// lowest precedence first; Info is the config it parses, nil when absent.
type agentConfigEntry struct {
	Type string         `json:"type"`
	Path string         `json:"path"`
	Info map[string]any `json:"info"`
}

// listConfig reads the configuration documents and sources of one
// workspace, the project's own opencode.json among them at the end.
func (a *agent) listConfig(ctx context.Context, dir string) ([]agentConfigEntry, error) {
	raw, err := a.call(ctx, "GET", "/config?location[directory]="+url.QueryEscape(dir), nil)
	if err != nil {
		return nil, err
	}
	var out []agentConfigEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// escapeFileRoute is one file path for an fs/read route: each folder and
// file escapes as its own segment, slashes kept as the route's separators.
func escapeFileRoute(rel string) string {
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// fsEntry is one child of a directory the server listed: its path relative
// to the listing's location, the name a row shows, and whether it is a
// folder.
type fsEntry struct {
	rel  string
	name string
	dir  bool
}

// listDir reads one directory of the server's disk. The location is where
// the browse was opened and stays put while rel walks from it, so a look
// around files no project row the folder did not already have; the entries
// come back relative to that location, the way the server spells them.
func (a *agent) listDir(ctx context.Context, location, rel string) ([]fsEntry, error) {
	route := "/fs/list?location[directory]=" + url.QueryEscape(location)
	if rel != "" && rel != "." {
		route += "&path=" + url.QueryEscape(rel)
	}
	raw, err := a.call(ctx, "GET", route, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	entries := []fsEntry{}
	for _, e := range out.Data {
		rel := strings.TrimSuffix(e.Path, "/")
		if rel == "" {
			continue
		}
		entries = append(entries, fsEntry{rel: rel, name: path.Base(rel), dir: e.Type == "directory"})
	}
	return entries, nil
}

// location asks the server which folder it works in: the one named, else
// the server's own default.
func (a *agent) location(ctx context.Context, dir string) (string, error) {
	route := "/location"
	if dir != "" {
		route += "?location[directory]=" + url.QueryEscape(dir)
	}
	raw, err := a.call(ctx, "GET", route, nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Directory string `json:"directory"`
		Data      struct {
			Directory string `json:"directory"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", fmt.Errorf("the location did not read")
	}
	if out.Directory == "" {
		out.Directory = out.Data.Directory
	}
	if out.Directory == "" {
		return "", fmt.Errorf("the server named no folder")
	}
	return out.Directory, nil
}

// vcsInfo is the version control one workspace sits in, as the server
// reads it: the provider, the branch on show and the branch it comes off.
type vcsInfo struct {
	provider string
	branch   string
	base     string
}

// vcsChange is one file a workspace has changed: its path as the repo
// spells it, what happened to it, the two counts and the patch itself.
type vcsChange struct {
	file      string
	status    string // "added", "modified" or "deleted"
	additions int
	deletions int
	patch     string
}

// workspaceVcs reads the version control a workspace sits in.
func (a *agent) workspaceVcs(ctx context.Context, dir string) (vcsInfo, error) {
	raw, err := a.call(ctx, "GET", "/vcs?location[directory]="+url.QueryEscape(dir), nil)
	if err != nil {
		return vcsInfo{}, err
	}
	var out struct {
		Data struct {
			Provider string `json:"provider"`
			Branch   struct {
				Current string `json:"current"`
				Default string `json:"default"`
			} `json:"branch"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return vcsInfo{}, fmt.Errorf("the version control did not read")
	}
	return vcsInfo{provider: out.Data.Provider, branch: out.Data.Branch.Current,
		base: out.Data.Branch.Default}, nil
}

// workspaceChanges reads the files a workspace has changed in one mode:
// "working" for what is not committed yet, "branch" for everything the
// branch holds beyond its base. The patches come with them.
func (a *agent) workspaceChanges(ctx context.Context, dir, mode string) ([]vcsChange, error) {
	route := "/vcs/diff?location[directory]=" + url.QueryEscape(dir) + "&mode=" + url.QueryEscape(mode)
	raw, err := a.call(ctx, "GET", route, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			File      string `json:"file"`
			Patch     string `json:"patch"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil, fmt.Errorf("the diff did not read")
	}
	files := []vcsChange{}
	for _, f := range out.Data {
		files = append(files, vcsChange{file: f.File, status: f.Status,
			additions: f.Additions, deletions: f.Deletions, patch: f.Patch})
	}
	return files, nil
}

// findFiles searches one workspace's disk for files whose paths match a
// query, ranked by the server, cut at the limit asked for: the way to a
// file in a deep tree without unfolding it folder by folder.
func (a *agent) findFiles(ctx context.Context, dir, query string, limit int) ([]fsEntry, error) {
	route := "/fs/find?location[directory]=" + url.QueryEscape(dir) +
		"&query=" + url.QueryEscape(query) + "&type=file"
	if limit > 0 {
		route += "&limit=" + strconv.Itoa(limit)
	}
	raw, err := a.call(ctx, "GET", route, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil, fmt.Errorf("the search did not read")
	}
	entries := []fsEntry{}
	for _, e := range out.Data {
		rel := strings.TrimSuffix(e.Path, "/")
		if rel == "" {
			continue
		}
		entries = append(entries, fsEntry{rel: rel, name: path.Base(rel), dir: e.Type == "directory"})
	}
	return entries, nil
}

// readWorkspaceFile streams one file named relative to the workspace.
func (a *agent) readWorkspaceFile(ctx context.Context, dir, rel string) ([]byte, error) {
	raw, err := a.call(ctx, "GET", "/fs/read/"+escapeFileRoute(rel)+"?location[directory]="+url.QueryEscape(dir), nil)
	return []byte(raw), err
}

// writeWorkspaceFile posts one file of the workspace, its parent folders
// created as needed; the server re-reads it without a restart.
func (a *agent) writeWorkspaceFile(ctx context.Context, dir, name string, data []byte) error {
	_, err := a.call(ctx, "POST",
		"/experimental/fs/write?path="+url.QueryEscape(name)+"&location[directory]="+url.QueryEscape(dir),
		json.RawMessage(data))
	return err
}

// listSessions reads the conversations, newest first, root ones only: a
// subagent's session belongs to the conversation that spawned it, not to
// the sidebar. The server pages them; cursor "" starts at the newest page
// and a later page picks up where the last one left off, with "" again
// when the list runs out. A search narrows the whole store to what the
// titles match, and a directory asks for one folder's own chats; both page
// the same way.
func (a *agent) listSessions(ctx context.Context, cursor, search, dir string) ([]agentSession, string, error) {
	path := "/session?limit=50&parentID=null"
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	if search != "" {
		path += "&search=" + url.QueryEscape(search)
	}
	if dir != "" {
		path += "&directory=" + url.QueryEscape(dir)
	}
	raw, err := a.call(ctx, "GET", path, nil)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Data []struct {
			ID       string `json:"id"`
			ParentID string `json:"parentID"`
			Title    string `json:"title"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
			Model struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
				Variant    string `json:"variant"`
			} `json:"model"`
			Cost float64 `json:"cost"`
			Time struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			} `json:"time"`
		} `json:"data"`
		Cursor struct {
			Next string `json:"next"`
		} `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	all := []agentSession{}
	for _, s := range out.Data {
		if s.ParentID != "" {
			// A build that hands children over anyway: they are the
			// spawning conversation's business, never the sidebar's.
			continue
		}
		row := agentSession{id: s.ID, title: s.Title, dir: canonDir(s.Location.Directory), updated: s.Time.Updated,
			cost:  s.Cost,
			model: agentModelRef{providerID: s.Model.ProviderID, id: s.Model.ID, variant: s.Model.Variant}}
		if row.updated == 0 {
			row.updated = s.Time.Created
		}
		all = append(all, row)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].updated > all[j].updated })
	return all, out.Cursor.Next, nil
}

// agentMessage is a stored turn of a session: what the user said or what
// the assistant answered in parts. The user's words arrive at the top
// level; the queued turn's payload shape decodes too. Shapes the
// transcript ignores (such as the "idle" marker) decode fine and get
// skipped.
type agentMessage struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Cost   float64 `json:"cost"`
	Tokens struct {
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
	} `json:"tokens"`
	Text    string `json:"text"`
	Payload struct {
		Text string `json:"text"`
	} `json:"payload"`
	Model struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
		Variant    string `json:"variant"`
	} `json:"model"`
	Content []agentPart `json:"content"`
}

// tokenSpend is what one stored turn used, as the store counts it: what
// the model wrote, and what it thought before writing.
type tokenSpend struct {
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
}

// agentPart is one piece of an assistant message: text or one tool call.
type agentPart struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	State struct {
		Status   string          `json:"status"`
		Input    json.RawMessage `json:"input"`
		Content  []toolOut       `json:"content"`
		Metadata struct {
			Exit      int  `json:"exit"`
			Truncated bool `json:"truncated"`
		} `json:"metadata"`
	} `json:"state"`
}

// sessionMessages reads one conversation's stored turns, oldest first.
func (a *agent) sessionMessages(ctx context.Context, id string) ([]agentMessage, error) {
	raw, err := a.call(ctx, "GET", "/session/"+id+"/message?order=asc", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []agentMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// sessionMessage reads one stored turn by its id: what a finished turn
// spent, without reading the whole conversation back for it.
func (a *agent) sessionMessage(ctx context.Context, id, msgID string) (agentMessage, error) {
	raw, err := a.call(ctx, "GET", "/session/"+id+"/message/"+msgID, nil)
	if err != nil {
		return agentMessage{}, err
	}
	var out struct {
		Data agentMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return agentMessage{}, err
	}
	return out.Data, nil
}

// events follows the SSE feed and calls h per event until ctx ends.
func (a *agent) events(ctx context.Context, h func(agentEvent)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", a.base+"/event", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", a.pw)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream: %s", resp.Status)
	}
	// One data line is a whole event envelope; tool outputs can make them
	// chunky, so size the scanner accordingly.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimPrefix(sc.Text(), "data: ")
		if line == sc.Text() { // comments like ": heartbeat" carry no data:
			continue
		}
		var ev agentEvent
		if json.Unmarshal([]byte(line), &ev) == nil {
			h(ev)
		}
	}
	return sc.Err()
}

// The app holds only what the transcript draws. The agent connection lives
// inside connect's goroutine, which publishes every state change through
// Window.Update so all writes happen on the UI thread.

// agentJobKind names what one queued job asks the runner to do.
type agentJobKind string

const (
	jobPrompt  agentJobKind = "prompt"  // send the user's words
	jobOpen    agentJobKind = "open"    // open a conversation's stored transcript
	jobStop    agentJobKind = "stop"    // interrupt the running turn
	jobAnswer  agentJobKind = "answer"  // reply to an approval
	jobRename  agentJobKind = "rename"  // retitle a conversation
	jobDelete  agentJobKind = "delete"  // delete a conversation
	jobModel   agentJobKind = "model"   // switch the conversation's model
	jobMore    agentJobKind = "more"    // load the next page of the list
	jobRefresh agentJobKind = "refresh" // read the newest page again
	jobUsage   agentJobKind = "usage"   // read one stored turn's spending
	jobServer  agentJobKind = "server"  // dial the server the settings chose
)

// agentJob is one request for the runner: a prompt to send, a conversation
// to open, a brand-new chat to create, a running turn to stop, an
// approval to answer, a list change to make or the server to dial.
type agentJob struct {
	kind     agentJobKind // what to do; one of the job* constants
	sid      string       // the session to act on; empty for a new one
	dir      string       // where a new chat should work; empty for the app's
	text     string       // the user's words or a new title, for jobPrompt and jobRename
	cursor   string       // the next page of conversations, for jobMore
	provider string       // the model's provider, for jobModel and jobPrompt
	model    string       // the model id, for jobModel and jobPrompt
	variant  string       // its reasoning effort, when it has one
	msg      string       // the stored turn whose spending to read, for jobUsage
	request  string       // the permission request being answered
	decision string       // "once", "always" or "reject"
	draft    uint64       // identifies a blank draft until its server session arrives
	server   serverConn   // the connection to dial, for jobServer
	pins     []string     // the chats pinned on that server, to load past its first page
}

// connect runs the pipe for one app lifetime: dial the server the settings
// chose, subscribe to its events and then take jobs and stream events in,
// one task at a time between frames. The server going down ends one
// attempt, not the session: the pipe dials again with a stretch of waiting
// between tries, and a server switch jumps the line.
func (a *app) connect(win *mygo.Window, jobs <-chan agentJob, cfg serverConn, pinnedIDs []string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The server dies with the window, even while startAgentFor is still
	// bringing it up.
	win.OnClosed(func() { cancel() })
	go watchPolicy(ctx, a.policyDirs, a.policyWrites, func(result policyResult) {
		win.Update(func() {
			if ctx.Err() == nil {
				a.acceptPolicy(result)
			}
		})
	})

	pins := pinnedIDs
	var last *agent
	booted := false // the first refill of a server settles onto its newest chat
	switching := false
	waitDue := false
	reconnect := 0
	note := ""           // the last trouble line shown, so a retry does not repeat it
	var stash []agentJob // jobs that landed while nothing was connected

	// say writes a transcript note for the attempt's server only: a
	// connection the user switched away from stays quiet.
	say := func(id, text string) {
		win.Update(func() {
			if a.activeServer == id {
				a.streamLine(text)
			}
		})
	}

	// applyForServer applies one server's events while it is still the
	// active one: a switch stops its stream short of writing over the
	// server the app opened next.
	applyForServer := func(srv serverConn) func(agentEvent) {
		return func(ev agentEvent) {
			if a.activeServer != srv.id {
				return
			}
			a.apply(ev)
		}
	}

	for {
		if last != nil {
			// The seat the server sat in frees itself for the next one,
			// in case the process lingers while its stream died.
			last.stop()
			last = nil
		}
		if switching {
			// The settings chose another server: dial it at once, and let
			// its own boot settle onto its newest chat.
			switching = false
			waitDue = false
			reconnect = 0
			note = ""
			stash = nil
			win.Update(func() { booted = false })
		}
		if waitDue {
			// Jobs queued while the pipe heals replay against the
			// respawned server; the asks of a dead one are gone. A server
			// switch queued during the wait skips the rest of it.
			waitDue = false
			win.Update(func() {
				a.finishCutTurns()
				a.streamLine("The agent went down; reconnecting…")
			})
			wait := []time.Duration{time.Second, 3 * time.Second, 7 * time.Second, 15 * time.Second}
			if reconnect-1 < len(wait) {
				wait = wait[reconnect-1:]
			} else {
				wait = wait[len(wait)-1:]
			}
			timer := time.NewTimer(wait[0])
		waiting:
			for {
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
					break waiting
				case job := <-jobs:
					if job.kind == jobServer {
						cfg, pins = job.server, job.pins
						switching = true
						timer.Stop()
						break waiting
					}
					stash = append(stash, job)
				}
			}
			if switching {
				continue
			}
		}

		win.Update(func() { a.setHealth(cfg.id, serverHealth{status: healthConnecting, addr: cfg.url}) })
		ag, err := startAgentFor(ctx, cfg, workdir())
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			win.Update(func() {
				a.setHealth(cfg.id, serverHealth{status: healthDown, detail: err.Error(), addr: cfg.url})
			})
			if text := "The agent would not come up: " + err.Error(); text != note {
				note = text
				say(cfg.id, text)
			}
			last = nil // nothing to free
			reconnect++
			waitDue = true
			continue
		}
		note = ""
		last = ag
		win.Update(func() {
			a.setHealth(cfg.id, serverHealth{status: healthReady, detail: ag.facts.version, addr: ag.addr})
			a.setFacts(cfg.id, ag.facts)
		})
		if reconnect > 0 {
			say(cfg.id, cfg.name+" is back.")
		}
		reconnect = 0
		// srv is this attempt's server: everything the attempt publishes
		// checks the view still shows it before writing.
		srv := cfg
		attemptPins := pins
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		go searchChats(attemptCtx, a.searches, func(ctx context.Context, query string) ([]agentSession, error) {
			rows, _, err := ag.listSessions(ctx, "", query, "")
			return rows, err
		}, win.Update, a.acceptSearch)
		win.Update(func() {
			if a.searchText != "" {
				a.reviseSearch()
			}
		})

		// The stream publishes bounded batches; helpers fill the sidebar
		// from the stored conversations while the loop runs.
		done := make(chan error, 1)
		go func() {
			done <- agentUpdates(attemptCtx, ag.events, win.Update, applyForServer(srv))
		}()
		go func() {
			ls, nxt, lerr := ag.listSessions(ctx, "", "", "")
			win.Update(func() {
				if a.activeServer != srv.id {
					return
				}
				if !booted {
					a.settleBoot(ls, nxt, lerr)
					booted = true
					return
				}
				// A refill keeps what the user was reading: the newest
				// page lands at the head of the list, the transcript stays.
				if lerr == nil {
					a.scope().sessions = freshSessions(a.scope().sessions, ls)
					a.scope().nextCursor = nxt
				}
				a.scope().sessionsLoaded = true
			})
			if lerr == nil {
				extra, err := loadPinnedSessions(attemptCtx, attemptPins, ls, nxt,
					func(ctx context.Context, cursor, search string) ([]agentSession, string, error) {
						return ag.listSessions(ctx, cursor, search, "")
					})
				win.Update(func() {
					if attemptCtx.Err() != nil || a.activeServer != srv.id {
						return
					}
					if err != nil {
						a.streamLine("Pinned chats could not all load: " + err.Error())
						return
					}
					// Ignore pins removed while the lookup was in flight.
					for _, s := range extra {
						if a.pinned[s.id] {
							a.scope().sessions = dedupeSessions(a.scope().sessions, []agentSession{s})
						}
					}
				})
			}
		}()
		go func() {
			ms, merr := ag.listModels(ctx)
			def, derr := ag.defaultModel(ctx)
			win.Update(func() {
				if a.activeServer != srv.id {
					return
				}
				if merr == nil {
					a.scope().models = ms
				} else {
					why := merr.Error()
					a.streamLine("The model list did not load: " + why)
				}
				if derr == nil {
					a.scope().serverDefault = def
				}
				a.scope().modelsLoaded = true
			})
		}()
		go func() {
			ps, perr := ag.listProjects(ctx)
			win.Update(func() {
				if a.activeServer != srv.id || perr != nil {
					return
				}
				// The server's own list, in its own order, is what the
				// section shows; the folders a chat sits in are unioned
				// where the list is read, so nothing a session named is
				// lost by the swap.
				a.scope().projects = ps
			})
		}()

		// A draft reuses its pending server chat until the UI learns its ID.
		var sid string
		var sidDraft uint64
		// pend is a model picked while nothing was open: it waits for the
		// next conversation open creates.
		var pend agentJob
		// open brings a fresh conversation up under the model wanted --
		// the app's default unless the job names another -- working in
		// the directory asked for; a model ref refusing to set does not
		// stop the turn.
		open := func(dir string, draft uint64, want agentModelRef) bool {
			nid, ref, nerr := ag.createSession(ctx, dir)
			if nerr != nil {
				say(srv.id, "The agent did not open a chat: "+nerr.Error())
				return false
			}
			if want.id == "" {
				prov, name := sessionModel()
				want = agentModelRef{providerID: prov, id: name}
			}
			if perr := ag.setModel(ctx, nid, want.providerID, want.id, want.variant); perr != nil {
				say(srv.id, "The app's model was not set, running the config's: "+perr.Error())
			}
			// A model picked while nothing was open applies to this new
			// conversation instead of the default.
			if pend.kind != "" && pend.draft == draft {
				if perr := ag.setModel(ctx, nid, pend.provider, pend.model, pend.variant); perr != nil {
					say(srv.id, "The model did not switch: "+perr.Error())
				}
				ref = agentModelRef{providerID: pend.provider, id: pend.model, variant: pend.variant}
				pend = agentJob{}
			}
			sid = nid
			sidDraft = draft
			home := canonDir(dir)
			if home == "" {
				home = canonDir(workdir())
			}
			win.Update(func() {
				if a.activeServer != srv.id {
					return
				}
				if a.draftVersion == draft && a.selectedSession == "" {
					a.selectedSession = nid
					a.selected = "New chat"
				}
				// The untitled row stands at the top; its title fills in
				// over the stream once words run.
				a.scope().sessions = append([]agentSession{{id: nid, dir: home, model: ref}}, a.scope().sessions...)
			})
			return true
		}

		// handle runs one job against the connected server; it reports the
		// one job that asks for another server instead.
		handle := func(job agentJob) bool {
			switch job.kind {
			case jobOpen:
				msgs, oerr := ag.sessionMessages(ctx, job.sid)
				if oerr != nil {
					say(srv.id, "Could not open that conversation: "+oerr.Error())
					return false
				}
				win.Update(func() {
					if a.activeServer != srv.id {
						return
					}
					a.freshTranscript()
					a.blocks = restoreBlocks(msgs)
					a.selectedSession = job.sid
				})
			case jobStop:
				who := job.sid
				if who == "" {
					who = sid
				}
				if who == "" {
					return false
				}
				if serr := ag.interrupt(ctx, who); serr != nil {
					say(srv.id, "The turn refused to stop: "+serr.Error())
				}
			case jobAnswer:
				if aerr := ag.replyPermission(ctx, job.sid, job.request, job.decision); aerr != nil {
					say(srv.id, "The approval did not go through: "+aerr.Error())
					return false
				}
				win.Update(func() {
					if a.activeServer == srv.id {
						a.dropPerm(job.request)
					}
				})
			case jobRename:
				if rerr := ag.renameSession(ctx, job.sid, job.text); rerr != nil {
					say(srv.id, "The rename did not take: "+rerr.Error())
				}
				// The new title folds in over the session.renamed event.
			case jobDelete:
				if derr := ag.deleteSession(ctx, job.sid); derr != nil {
					say(srv.id, "The chat did not delete: "+derr.Error())
					return false
				}
				win.Update(func() {
					if a.activeServer == srv.id {
						a.dropSession(job.sid)
					}
				})
			case jobMore:
				rows, cur, merr := ag.listSessions(ctx, job.cursor, "", "")
				win.Update(func() {
					if a.activeServer != srv.id {
						return
					}
					if merr == nil {
						a.scope().sessions = dedupeSessions(a.scope().sessions, rows)
						a.scope().nextCursor = cur
					} else {
						a.streamLine("The older chats did not load: " + merr.Error())
					}
					a.scope().moreLoading = false
				})
			case jobUsage:
				m, uerr := ag.sessionMessage(ctx, job.sid, job.msg)
				win.Update(func() {
					if a.activeServer != srv.id || uerr != nil {
						return
					}
					a.acceptUsage(m)
				})
			case jobRefresh:
				// The server's world moved on — a turn of this app's, or
				// another client's chat: the newest page lands at the head
				// of the list, and what the user was reading stays.
				rows, cur, rerr := ag.listSessions(ctx, "", "", "")
				win.Update(func() {
					if a.activeServer != srv.id || rerr != nil {
						return
					}
					a.scope().sessions = freshSessions(a.scope().sessions, rows)
					a.scope().nextCursor = cur
					a.scope().sessionsLoaded = true
				})
			case jobModel:
				who := job.sid
				if who == "" && sidDraft == job.draft {
					who = sid
				}
				if who == "" {
					pend = job
					return false
				}
				if merr := ag.setModel(ctx, who, job.provider, job.model, job.variant); merr != nil {
					say(srv.id, "The model did not switch: "+merr.Error())
				}
				// The switch folds back over session.model.selected.
			case jobPrompt:
				target := job.sid
				if target == "" {
					want := agentModelRef{providerID: job.provider, id: job.model, variant: job.variant}
					if (sid == "" || sidDraft != job.draft) && !open(job.dir, job.draft, want) {
						return false
					}
					target = sid
				}
				if perr := ag.prompt(ctx, target, job.text); perr != nil {
					say(srv.id, "The agent could not take that: "+perr.Error())
				}
			case jobServer:
				cfg, pins = job.server, job.pins
				switching = true
				return true
			}
			return false
		}

		alive := true
		for alive {
			var job agentJob
			if len(stash) > 0 {
				job, stash = stash[0], stash[1:]
			} else {
				select {
				case <-ctx.Done():
					cancelAttempt()
					return
				case <-done:
					alive = false
					continue
				case job = <-jobs:
				}
			}
			if handle(job) {
				alive = false
			}
		}
		cancelAttempt()
		if ctx.Err() != nil {
			return
		}
		if !switching {
			reconnect++
			waitDue = true
			win.Update(func() {
				a.setHealth(srv.id, serverHealth{status: healthDown, detail: "the stream ended", addr: ag.addr})
			})
		}
	}
}
