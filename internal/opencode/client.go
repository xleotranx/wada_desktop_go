package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/workspace"

	"time"
)

// agent drives a headless opencode server: the app spawns `opencode serve`
// for the local one or dials a server it was pointed at, speaks its /api
// HTTP surface and streams its /api/event SSE feed into the transcript, so
// the real agent answers where the demo copy used to.
type Client struct {
	stopOnce sync.Once
	Local    bool

	Base     string // like http://127.0.0.1:4096/api
	Password string // the password this app connects with
	Paired   bool   // that password is a pairing token, which lapses on its own
	HTTP     *http.Client
	Cmd      *exec.Cmd   // the process the app started; nil for a dialed server
	Address  string      // where it listens, for Settings to show
	Facts    agent.Facts // what its /info said about itself
	BodyCap  int64       // how much of one response to keep; 0 for defaultBodyCap
}

// defaultBodyCap is how much of one API response the client keeps: a stored
// conversation with images in it is megabytes, but not endless.
const defaultBodyCap int64 = 32 << 20

// agentEvent is one line of the opencode event stream, decoded to its
// envelope; the payload sits in Data and its shape depends on Type.
type Event struct {
	Type    string          `json:"type"`
	Created int64           `json:"created"`
	Data    json.RawMessage `json:"data"`
}

// standaloneAgent is one connection's client without a managed process:
// the server the app dials stands on its own.
func NewClient(cfg agent.Config) (*Client, error) {
	base, err := APIBase(cfg.URL)
	if err != nil {
		if cfg.Managed {
			// The app runs this server itself: the spawn reports the
			// address once it is up, and until then no dial can go out.
			return nil, errors.New("the app's server is not up yet")
		}
		return nil, err
	}
	return &Client{Local: cfg.Local, Base: base, Password: cfg.Password, Paired: cfg.Paired, HTTP: HTTPClient(),
		Address: strings.TrimSuffix(base, "/api")}, nil
}

// startAgentFor brings one configured server up: it spawns opencode serve
// for a connection the app manages, and dials any other, waiting for its
// API to answer either way.
func StartClient(ctx context.Context, cfg agent.Config, dir string) (*Client, error) {
	if cfg.Managed {
		return startLocalClient(ctx, cfg, dir)
	}
	a, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	facts, err := a.WaitReady(ctx, 8*time.Second)
	if err != nil {
		return nil, err
	}
	a.Facts = facts
	return a, nil
}

// startLocalAgent spawns opencode serve on a free port with the app's password
// (generated when settings hold none). The server dies with this process
// (Pdeathsig), so dev rebuilds don't leave orphans behind.
func startLocalClient(ctx context.Context, cfg agent.Config, dir string) (*Client, error) {
	exe, err := exec.LookPath("opencode")
	if err != nil {
		return nil, fmt.Errorf("opencode not found in PATH: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	pw := cfg.Password
	if pw == "" {
		pw = Secret()
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

	a := &Client{
		Base:     "http://127.0.0.1:" + strconv.Itoa(port) + "/api",
		Address:  "http://127.0.0.1:" + strconv.Itoa(port),
		Password: pw,
		HTTP:     HTTPClient(),
		Cmd:      cmd,
	}
	facts, err := a.WaitReady(ctx, 20*time.Second)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	a.Facts = facts
	return a, nil
}

// stop ends the server the app started; a server it was only pointed at
// keeps running.
func (a *Client) Stop() {
	a.stopOnce.Do(func() {
		if a.Cmd != nil && a.Cmd.Process != nil {
			a.Cmd.Process.Kill()
			a.Cmd.Wait()
		}
		if a.HTTP != nil {
			a.HTTP.CloseIdleConnections()
		}
	})
}

// waitReady polls /api/info until the server names its version, the
// deadline passes or the app walks away.
func (a *Client) WaitReady(ctx context.Context, within time.Duration) (agent.Facts, error) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return agent.Facts{}, ctx.Err()
		default:
		}
		raw, err := a.Call(ctx, "GET", "/info", nil)
		if err == nil {
			if facts := ReadInfo(raw); facts.Version != "" {
				return facts, nil
			}
		}
		select {
		case <-ctx.Done():
			return agent.Facts{}, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return agent.Facts{}, fmt.Errorf("the server did not come up in %s", within)
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
func (a *Client) Call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	limit := a.BodyCap
	if limit <= 0 {
		limit = defaultBodyCap
	}
	var buf bytes.Buffer
	// Raw bytes go out as the server's file API asks for them.
	_, isRaw := body.(json.RawMessage)
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.Base+path, &buf)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", a.Password)
	if isRaw {
		req.Header.Set("Content-Type", "application/octet-stream")
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
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
		return nil, fmt.Errorf("%s", RefusedLine(a.Paired))
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
// directory given: the server's own when none is named. The agent given
// rides the creation, since the server folds the choice into the session
// at birth ("plan" keeps its answers read-only) and not per turn.
func (a *Client) CreateSession(ctx context.Context, dir, persona string) (string, agent.ModelRef, error) {
	body := map[string]any{}
	if dir != "" {
		body["location"] = map[string]any{"directory": dir}
	}
	if persona != "" {
		body["agent"] = persona
	}
	raw, err := a.Call(ctx, "POST", "/session", body)
	if err != nil {
		return "", agent.ModelRef{}, err
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
		return "", agent.ModelRef{}, err
	}
	return out.Data.ID, agent.ModelRef{ProviderID: out.Data.Model.ProviderID,
		ID: out.Data.Model.ID, Variant: out.Data.Model.Variant}, nil
}

// prompt queues one user turn; the answer streams back over events, so the
// call only needs to enqueue, not wait.
func (a *Client) Prompt(ctx context.Context, sessionID, text string) error {
	_, err := a.Call(ctx, "POST", "/session/"+sessionID+"/prompt",
		map[string]any{"text": text})
	return err
}

// setModel points one conversation at a model of the catalog, with its
// reasoning effort when it has one. The switch folds back over the
// session.model.selected event.
func (a *Client) SetModel(ctx context.Context, sessionID, providerID, id, variant string) error {
	ref := map[string]any{"providerID": providerID, "id": id}
	if variant != "" {
		ref["variant"] = variant
	}
	_, err := a.Call(ctx, "POST", "/session/"+sessionID+"/model",
		map[string]any{"model": ref})
	return err
}

// interrupt stops the conversation's running turn; the server answers
// {"interrupted":…} and says so again over the event stream.
func (a *Client) Interrupt(ctx context.Context, sessionID string) error {
	_, err := a.Call(ctx, "POST", "/session/"+sessionID+"/interrupt", struct{}{})
	return err
}

// replyPermission answers one approval the agent asked for. Once the
// server takes it, the turn carries on ("always" remembers the pattern),
// and the card leaves the transcript.
func (a *Client) ReplyPermission(ctx context.Context, sessionID, requestID, decision string) error {
	_, err := a.Call(ctx, "POST", "/session/"+sessionID+"/permission/"+requestID+"/reply",
		map[string]any{"decision": decision})
	return err
}

// renameSession sets one conversation's title; the rename comes back as
// an event, which folds it into the sidebar row.
func (a *Client) RenameSession(ctx context.Context, sessionID, title string) error {
	_, err := a.Call(ctx, "PATCH", "/session/"+sessionID, map[string]any{"title": title})
	return err
}

// deleteSession drops one conversation from the store for good.
func (a *Client) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := a.Call(ctx, "DELETE", "/session/"+sessionID, nil)
	return err
}

// agentSession is the sidebar row of one real conversation, with the
// folder it works in — the list spans every project the store knows — and
// what it has spent so far.
// ListAgents reads the personas a conversation can run under, keeping
// the chat-pickable ones in the server's own order.
func (a *Client) ListAgents(ctx context.Context) ([]agent.Persona, error) {
	raw, err := a.Call(ctx, "GET", "/agent", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Mode        string `json:"mode"`
			Hidden      bool   `json:"hidden"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	var all []agent.Persona
	for _, row := range out.Data {
		if row.Mode != "primary" || row.Hidden || row.ID == "" {
			continue
		}
		all = append(all, agent.Persona{ID: row.ID, Name: row.Name, Description: row.Description})
	}
	return all, nil
}

// listModels reads the catalog the signed-in server offers.
func (a *Client) ListModels(ctx context.Context) ([]agent.Model, error) {
	raw, err := a.Call(ctx, "GET", "/model", nil)
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
	all := []agent.Model{}
	for _, m := range out.Data {
		row := agent.Model{ID: m.ID, ProviderID: m.ProviderID, Name: m.Name, Free: true}
		for _, v := range m.Variants {
			row.Variants = append(row.Variants, v.ID)
		}
		for _, c := range m.Cost {
			if c.Input != 0 || c.Output != 0 {
				row.Free = false
			}
		}
		all = append(all, row)
	}
	return all, nil
}

// defaultModel reads the model the server itself would run, for a client
// whose own pick has no home in the catalog: a remote server's default
// belongs to that box the way its providers do.
func (a *Client) DefaultModel(ctx context.Context) (agent.ModelRef, error) {
	raw, err := a.Call(ctx, "GET", "/model/default", nil)
	if err != nil {
		return agent.ModelRef{}, err
	}
	var out struct {
		Data struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
			Variant    string `json:"variant"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return agent.ModelRef{}, err
	}
	if out.Data.ID == "" || out.Data.ProviderID == "" {
		return agent.ModelRef{}, fmt.Errorf("the server named no default model")
	}
	return agent.ModelRef{ProviderID: out.Data.ProviderID, ID: out.Data.ID, Variant: out.Data.Variant}, nil
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
func (a *Client) ListProjects(ctx context.Context) ([]string, error) {
	raw, err := a.Call(ctx, "GET", "/project", nil)
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
		dir := remoteDir(p.Canonical)
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
			exists[i] = a.LocationExists(ctx, dir)
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
func (a *Client) LocationExists(ctx context.Context, dir string) bool {
	_, err := a.Call(ctx, "GET", "/fs/list?location[directory]="+url.QueryEscape(dir), nil)
	return err == nil
}

// agentConfigEntry is one configuration document a workspace stacks up,
// lowest precedence first; Info is the config it parses, nil when absent.
type ConfigEntry struct {
	Type string         `json:"type"`
	Path string         `json:"path"`
	Info map[string]any `json:"info"`
}

// listConfig reads the configuration documents and sources of one
// workspace, the project's own opencode.json among them at the end.
func (a *Client) ListConfig(ctx context.Context, dir string) ([]ConfigEntry, error) {
	raw, err := a.Call(ctx, "GET", "/config?location[directory]="+url.QueryEscape(dir), nil)
	if err != nil {
		return nil, err
	}
	var out []ConfigEntry
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
// listDir reads one directory of the server's disk. The location is where
// the browse was opened and stays put while rel walks from it, so a look
// around files no project row the folder did not already have; the entries
// come back relative to that location, the way the server spells them.
func (a *Client) ListDir(ctx context.Context, location, rel string) ([]workspace.FileEntry, error) {
	route := "/fs/list?location[directory]=" + url.QueryEscape(location)
	if rel != "" && rel != "." {
		route += "&path=" + url.QueryEscape(rel)
	}
	raw, err := a.Call(ctx, "GET", route, nil)
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
	entries := []workspace.FileEntry{}
	for _, e := range out.Data {
		rel := strings.TrimSuffix(e.Path, "/")
		if rel == "" {
			continue
		}
		entries = append(entries, workspace.FileEntry{Path: rel, Name: path.Base(rel), Directory: e.Type == "directory"})
	}
	return entries, nil
}

// location asks the server which folder it works in: the one named, else
// the server's own default.
func (a *Client) Location(ctx context.Context, dir string) (string, error) {
	route := "/location"
	if dir != "" {
		route += "?location[directory]=" + url.QueryEscape(dir)
	}
	raw, err := a.Call(ctx, "GET", route, nil)
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
// vcsChange is one file a workspace has changed: its path as the repo
// spells it, what happened to it, the two counts and the patch itself.
// workspaceVcs reads the version control a workspace sits in.
func (a *Client) WorkspaceVcs(ctx context.Context, dir string) (workspace.VCSInfo, error) {
	raw, err := a.Call(ctx, "GET", "/vcs?location[directory]="+url.QueryEscape(dir), nil)
	if err != nil {
		return workspace.VCSInfo{}, err
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
		return workspace.VCSInfo{}, fmt.Errorf("the version control did not read")
	}
	return workspace.VCSInfo{Provider: out.Data.Provider, Branch: out.Data.Branch.Current,
		Base: out.Data.Branch.Default}, nil
}

// workspaceChanges reads the files a workspace has changed in one mode:
// "working" for what is not committed yet, "branch" for everything the
// branch holds beyond its base. The patches come with them.
func (a *Client) WorkspaceChanges(ctx context.Context, dir, mode string) ([]workspace.Change, error) {
	route := "/vcs/diff?location[directory]=" + url.QueryEscape(dir) + "&mode=" + url.QueryEscape(mode)
	raw, err := a.Call(ctx, "GET", route, nil)
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
	files := []workspace.Change{}
	for _, f := range out.Data {
		files = append(files, workspace.Change{File: f.File, Status: f.Status,
			Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch})
	}
	return files, nil
}

// findFiles searches one workspace's disk for files whose paths match a
// query, ranked by the server, cut at the limit asked for: the way to a
// file in a deep tree without unfolding it folder by folder.
func (a *Client) FindFiles(ctx context.Context, dir, query string, limit int) ([]workspace.FileEntry, error) {
	route := "/fs/find?location[directory]=" + url.QueryEscape(dir) +
		"&query=" + url.QueryEscape(query) + "&type=file"
	if limit > 0 {
		route += "&limit=" + strconv.Itoa(limit)
	}
	raw, err := a.Call(ctx, "GET", route, nil)
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
	entries := []workspace.FileEntry{}
	for _, e := range out.Data {
		rel := strings.TrimSuffix(e.Path, "/")
		if rel == "" {
			continue
		}
		entries = append(entries, workspace.FileEntry{Path: rel, Name: path.Base(rel), Directory: e.Type == "directory"})
	}
	return entries, nil
}

// readWorkspaceFile streams one file named relative to the workspace.
func (a *Client) ReadWorkspaceFile(ctx context.Context, dir, rel string) ([]byte, error) {
	raw, err := a.Call(ctx, "GET", "/fs/read/"+escapeFileRoute(rel)+"?location[directory]="+url.QueryEscape(dir), nil)
	return []byte(raw), err
}

// writeWorkspaceFile posts one file of the workspace, its parent folders
// created as needed; the server re-reads it without a restart.
func (a *Client) WriteWorkspaceFile(ctx context.Context, dir, name string, data []byte) error {
	_, err := a.Call(ctx, "POST",
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
func (a *Client) ListSessions(ctx context.Context, cursor, search, dir string) ([]agent.Session, string, error) {
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
	raw, err := a.Call(ctx, "GET", path, nil)
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
	all := []agent.Session{}
	for _, s := range out.Data {
		if s.ParentID != "" {
			// A build that hands children over anyway: they are the
			// spawning conversation's business, never the sidebar's.
			continue
		}
		row := agent.Session{ID: s.ID, Title: s.Title, Directory: remoteDir(s.Location.Directory), Updated: s.Time.Updated,
			Cost:  s.Cost,
			Model: agent.ModelRef{ProviderID: s.Model.ProviderID, ID: s.Model.ID, Variant: s.Model.Variant}}
		if row.Updated == 0 {
			row.Updated = s.Time.Created
		}
		all = append(all, row)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Updated > all[j].Updated })
	return all, out.Cursor.Next, nil
}

// agentMessage is a stored turn of a session: what the user said or what
// the assistant answered in parts. The user's words arrive at the top
// level; the queued turn's payload shape decodes too. Shapes the
// transcript ignores (such as the "idle" marker) decode fine and get
// skipped.
type storedMessage struct {
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
	Content []storedPart `json:"content"`
}

// agentPart is one piece of an assistant message: text or one tool call.
type storedPart struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	State struct {
		Status string `json:"status"`
		Input  struct {
			Command string `json:"command"`
		} `json:"input"`
		Content  []ToolOutput `json:"content"`
		Metadata struct {
			Exit      int  `json:"exit"`
			Truncated bool `json:"truncated"`
		} `json:"metadata"`
	} `json:"state"`
}

// sessionMessages reads one conversation's stored turns, oldest first.
func (a *Client) SessionMessages(ctx context.Context, id string) ([]agent.Message, error) {
	raw, err := a.Call(ctx, "GET", "/session/"+id+"/message?order=asc", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []storedMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	messages := make([]agent.Message, len(out.Data))
	for i, message := range out.Data {
		messages[i] = message.normalized()
	}
	return messages, nil
}

// sessionMessage reads one stored turn by its id: what a finished turn
// spent, without reading the whole conversation back for it.
func (a *Client) SessionMessage(ctx context.Context, id, msgID string) (agent.Message, error) {
	raw, err := a.Call(ctx, "GET", "/session/"+id+"/message/"+msgID, nil)
	if err != nil {
		return agent.Message{}, err
	}
	var out struct {
		Data storedMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return agent.Message{}, err
	}
	return out.Data.normalized(), nil
}

// events follows the SSE feed and calls h per event until ctx ends.
func (a *Client) Events(ctx context.Context, h func(Event)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", a.Base+"/event", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", a.Password)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := a.HTTP.Do(req)
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
		var ev Event
		if json.Unmarshal([]byte(line), &ev) == nil {
			h(ev)
		}
	}
	return sc.Err()
}

// toolOut is one piece of a tool's output, of which text pieces render.
type ToolOutput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func remoteDir(dir string) string {
	if dir == "" {
		return ""
	}
	return path.Clean(dir)
}

func (a *Client) Info() agent.Facts { return a.Facts }
func (a *Client) Endpoint() string  { return a.Address }
