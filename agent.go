package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// agent drives a headless opencode server: the app spawns `opencode serve`,
// speaks its /api HTTP surface and streams its /api/event SSE feed into the
// transcript, so the real agent answers where the demo copy used to.
type agent struct {
	base string // like http://127.0.0.1:4096/api
	pw   string // the server password this app chose for serve
	http *http.Client
	cmd  *exec.Cmd
}

// agentEvent is one line of the opencode event stream, decoded to its
// envelope; the payload sits in Data and its shape depends on Type.
type agentEvent struct {
	Type    string          `json:"type"`
	Created int64           `json:"created"`
	Data    json.RawMessage `json:"data"`
}

// startAgent spawns opencode serve rooted at dir, waits for its API to
// answer and hands back a client. The server dies with this process
// (Pdeathsig), so dev rebuilds don't leave orphans behind.
func startAgent(ctx context.Context, dir string) (*agent, error) {
	exe, err := exec.LookPath("opencode")
	if err != nil {
		return nil, fmt.Errorf("opencode not found in PATH: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	rand.Read(key)
	pw := base64.RawURLEncoding.EncodeToString(key)

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
		pw:   pw,
		http: &http.Client{},
		cmd:  cmd,
	}
	if err := a.waitReady(ctx, 20*time.Second); err != nil {
		cmd.Process.Kill()
		return nil, err
	}
	return a, nil
}

// waitReady polls /api/info until the server names its version, the
// deadline passes or the app walks away.
func (a *agent) waitReady(ctx context.Context, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		raw, err := a.call(ctx, "GET", "/info", nil)
		if err == nil && strings.Contains(string(raw), "version") {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("opencode serve did not come up in %s", within)
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
// like {"_tag":…,"message":…}, which this turns into Go errors.
func (a *agent) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, &buf)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", a.pw)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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
func (a *agent) createSession(ctx context.Context, dir string) (string, error) {
	var body any = struct{}{}
	if dir != "" {
		body = map[string]any{
			"location": map[string]any{"directory": dir},
		}
	}
	raw, err := a.call(ctx, "POST", "/session", body)
	if err != nil {
		return "", err
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Data.ID, nil
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

// pickModel names one conversation's model; without it, the server runs
// the config default, which in this app is not free.
func (a *agent) pickModel(ctx context.Context, sessionID string) error {
	prov, name := sessionModel()
	_, err := a.call(ctx, "POST", "/session/"+sessionID+"/model",
		map[string]any{"model": map[string]any{"providerID": prov, "id": name}})
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
// folder it works in — the list spans every project the store knows.
type agentSession struct {
	id      string
	title   string
	dir     string
	updated int64
}

// listSessions reads the conversations, newest first. The server pages
// them; cursor "" starts at the newest page and a later page picks up
// where the last one left off, with "" again when the list runs out.
func (a *agent) listSessions(ctx context.Context, cursor string) ([]agentSession, string, error) {
	path := "/session?limit=50"
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	raw, err := a.call(ctx, "GET", path, nil)
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Data []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
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
		row := agentSession{id: s.ID, title: s.Title, dir: s.Location.Directory, updated: s.Time.Updated}
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
	Type    string `json:"type"`
	Text    string `json:"text"`
	Payload struct {
		Text string `json:"text"`
	} `json:"payload"`
	Model struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	} `json:"model"`
	Content []agentPart `json:"content"`
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

// toolRun is the shared outcome of one tool call, as both the live events
// and the stored parts report it.
type toolRun struct {
	command   string
	out       []string
	exit      int
	truncated bool
}

// toolDetailLines cuts a tool run into the lines of its detail box: what
// ran first, then the last of what it printed.
func toolDetailLines(run toolRun) []string {
	var lines []string
	if run.command != "" {
		lines = append(lines, "$ "+run.command)
	}
	out := run.out
	if len(out) > 6 {
		out = out[len(out)-6:]
	}
	lines = append(lines, out...)
	if run.exit != 0 {
		lines = append(lines, fmt.Sprintf("exit %d", run.exit))
	}
	if run.truncated {
		lines = append(lines, "…")
	}
	return lines
}

// restoreBlocks turns a session's stored messages into the transcript
// blocks the live stream would have drawn.
func restoreBlocks(messages []agentMessage) []block {
	var out []block
	for _, m := range messages {
		switch m.Type {
		case "user":
			t := m.Payload.Text
			if t == "" {
				t = m.Text
			}
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, block{kind: "user", text: t})
			}
		case "assistant":
			for i, part := range m.Content {
				switch part.Type {
				case "text":
					if t := strings.TrimSpace(part.Text); t != "" {
						out = append(out, block{kind: "text", text: t})
					}
				case "tool":
					label, icon := toolLook(part.Name)
					id := part.ID
					if id == "" {
						id = fmt.Sprintf("part-%d", i)
					}
					var cmd struct {
						Command string `json:"command"`
					}
					json.Unmarshal(part.State.Input, &cmd)
					run := toolRun{command: cmd.Command,
						exit: part.State.Metadata.Exit, truncated: part.State.Metadata.Truncated}
					run.out = textOf(part.State.Content)
					out = append(out, block{kind: "activity", text: label, icon: icon,
						key: "activity:" + id, detail: commandDetail(toolDetailLines(run)...)})
				}
			}
		}
	}
	return out
}

// textOf joins the text pieces of tool output into lines.
func textOf(content []toolOut) []string {
	var out []string
	for _, o := range content {
		if o.Type == "text" {
			out = append(out, strings.Split(strings.TrimRight(o.Text, "\n"), "\n")...)
		}
	}
	return out
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

// agentJob is one request for the runner: a prompt to send, a conversation
// to open, a brand-new chat to create, a running turn to stop, an
// approval to answer or a list change to make.
type agentJob struct {
	kind     string // "prompt", "open", "new", "stop", "answer", "rename", "delete" or "more"
	sid      string // the session to act on; empty for a new one
	dir      string // where a new chat should work; empty for the app's
	text     string // the user's words or a new title, for "prompt" and "rename"
	cursor   string // the next page of conversations, for "more"
	request  string // the permission request being answered
	decision string // "once", "always" or "reject"
}

// connect runs the pipe for one app lifetime: spawn the server, subscribe
// to its events and then take jobs and stream events in, one task at a
// time between frames.
func (a *app) connect(win *mygo.Window, jobs <-chan agentJob) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The server dies with the window, even while startAgent is still
	// bringing it up.
	win.OnClosed(func() { cancel() })

	ag, err := startAgent(ctx, workdir())
	if err != nil {
		why := err.Error()
		win.Update(func() { a.streamLine("Agent not connected: " + why) })
		return
	}

	// The stream feeds straight into Update; helpers fill the sidebar
	// from the stored conversations while the loop runs.
	done := make(chan error, 1)
	go func() {
		done <- ag.events(ctx, func(ev agentEvent) { win.Update(func() { a.apply(ev) }) })
	}()
	go func() {
		ls, nxt, lerr := ag.listSessions(ctx, "")
		win.Update(func() {
			if lerr == nil {
				a.sessions = ls
				a.nextCursor = nxt
			}
			a.sessionsLoaded = true
		})
	}()

	// sid is a conversation created here, reused until the UI learns of
	// it from its own update.
	var sid string
	// open brings a fresh conversation up under the app's free model,
	// working in the directory asked for; a model ref refusing to set does
	// not stop the turn.
	open := func(dir string) bool {
		nid, nerr := ag.createSession(ctx, dir)
		if nerr != nil {
			why := nerr.Error()
			win.Update(func() { a.streamLine("The agent did not open a chat: " + why) })
			return false
		}
		if perr := ag.pickModel(ctx, nid); perr != nil {
			why := perr.Error()
			win.Update(func() { a.streamLine("The app's model was not set, running the config's: " + why) })
		}
		sid = nid
		home := dir
		if home == "" {
			home = workdir()
		}
		win.Update(func() {
			a.selectedSession = sid
			// The untitled row stands at the top; its title fills in
			// over the stream once words run.
			a.sessions = append([]agentSession{{id: sid, dir: home}}, a.sessions...)
		})
		return true
	}
	alive := true
	for alive {
		select {
		case <-done:
			alive = false
		case job := <-jobs:
			switch job.kind {
			case "open":
				msgs, oerr := ag.sessionMessages(ctx, job.sid)
				if oerr != nil {
					why := oerr.Error()
					win.Update(func() { a.streamLine("Could not open that conversation: " + why) })
					continue
				}
				win.Update(func() {
					a.freshTranscript()
					a.blocks = restoreBlocks(msgs)
					a.selectedSession = job.sid
				})
			case "new":
				if sid == "" && !open(job.dir) {
					continue
				}
			case "stop":
				who := job.sid
				if who == "" {
					who = sid
				}
				if who == "" {
					continue
				}
				if serr := ag.interrupt(ctx, who); serr != nil {
					why := serr.Error()
					win.Update(func() { a.streamLine("The turn refused to stop: " + why) })
				}
			case "answer":
				if aerr := ag.replyPermission(ctx, job.sid, job.request, job.decision); aerr != nil {
					why := aerr.Error()
					win.Update(func() { a.streamLine("The approval did not go through: " + why) })
					continue
				}
				win.Update(func() { a.dropPerm(job.request) })
			case "rename":
				if rerr := ag.renameSession(ctx, job.sid, job.text); rerr != nil {
					why := rerr.Error()
					win.Update(func() { a.streamLine("The rename did not take: " + why) })
				}
				// The new title folds in over the session.renamed event.
			case "delete":
				if derr := ag.deleteSession(ctx, job.sid); derr != nil {
					why := derr.Error()
					win.Update(func() { a.streamLine("The chat did not delete: " + why) })
					continue
				}
				win.Update(func() { a.dropSession(job.sid) })
			case "more":
				rows, cur, merr := ag.listSessions(ctx, job.cursor)
				win.Update(func() {
					if merr == nil {
						a.sessions = dedupeSessions(a.sessions, rows)
						a.nextCursor = cur
					} else {
						why := merr.Error()
						a.streamLine("The older chats did not load: " + why)
					}
					a.moreLoading = false
				})
			default: // "prompt"
				target := job.sid
				if target == "" {
					if sid == "" && !open(job.dir) {
						continue
					}
					target = sid
				}
				if perr := ag.prompt(ctx, target, job.text); perr != nil {
					why := perr.Error()
					win.Update(func() { a.streamLine("The agent could not take that: " + why) })
				}
			}
		}
	}
}

// streamLine appends a faint line to the transcript, the way the live
// preview renders, for engine trouble worth showing in place.
func (a *app) streamLine(text string) {
	a.blocks = append(a.blocks, block{kind: "stream", text: text})
	a.feed.Y = 1e9
}

// workdir is the app's own directory, the one the agent falls back to
// when a chat does not name its own.
func workdir() string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	return dir
}

// expandPath turns a typed folder into the path a session can sit in:
// ~ grows into the home directory and the path must exist.
func expandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = home + strings.TrimPrefix(path, "~")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if st, serr := os.Stat(abs); serr != nil || !st.IsDir() {
		return "", fmt.Errorf("that folder does not exist")
	}
	return abs, nil
}

// dirBase is a directory's name for showing, its last segment.
func dirBase(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Base(dir)
}

// evTextData is the payload of the session.text.* events: one running text
// block inside an assistant message, addressed by message and ordinal.
type evTextData struct {
	SessionID string `json:"sessionID"`
	MessageID string `json:"assistantMessageID"`
	Ordinal   int    `json:"ordinal"`
	Delta     string `json:"delta"`
	Text      string `json:"text"`
}

// evToolData is the payload of the session.tool.* events: one tool call,
// addressed by its id, whose name, input and output fill in progressively.
type evToolData struct {
	SessionID string          `json:"sessionID"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   []toolOut       `json:"content"`
	Metadata  struct {
		Status    string `json:"status"`
		Truncated bool   `json:"truncated"`
		Exit      int    `json:"exit"`
	} `json:"metadata"`
}

// toolOut is one piece of a tool's output, of which text pieces render.
type toolOut struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// evPermissionData is the payload of permission.asked: one approval the
// agent needs before acting, with what it wants and what "always" saves.
type evPermissionData struct {
	SessionID string   `json:"sessionID"`
	ID        string   `json:"id"`
	Action    string   `json:"action"`
	Resources []string `json:"resources"`
	Save      []string `json:"save"`
}

// agentPerm is an approval request the agent raised mid-turn, held onto so
// its card's buttons know where to answer.
type agentPerm struct {
	id        string
	session   string
	action    string
	resources []string
	save      []string
}

// apply folds one agent event into the transcript. Kinds reuse the
// design's vocabulary: assistant words stream as a faint preview line and
// settle into a text block when the sentence ends, and each tool call is an
// activity row that gains its detail once something ran.
func (a *app) apply(ev agentEvent) {
	switch ev.Type {
	case "session.text.started":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		key := d.MessageID + ":" + strconv.Itoa(d.Ordinal)
		a.live[key] = len(a.blocks)
		a.blocks = append(a.blocks, block{kind: "stream", key: "stream:" + key})
	case "session.text.delta":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		if idx, ok := a.live[d.MessageID+":"+strconv.Itoa(d.Ordinal)]; ok {
			a.blocks[idx].text += d.Delta
		}
	case "session.text.ended":
		var d evTextData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		key := d.MessageID + ":" + strconv.Itoa(d.Ordinal)
		if idx, ok := a.live[key]; ok {
			delete(a.live, key)
			a.blocks[idx] = block{kind: "text", text: d.Text}
		}
	case "session.tool.input.started":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.toolNames[d.ID] = d.Name
	case "session.tool.called":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		label, icon := toolLook(a.toolNames[d.ID])
		var in struct {
			Command string `json:"command"`
		}
		json.Unmarshal(d.Input, &in)
		var lines []string
		if in.Command != "" {
			lines = append(lines, "$ "+in.Command)
			a.toolCmds[d.ID] = in.Command
		}
		a.toolBlocks[d.ID] = len(a.blocks)
		a.blocks = append(a.blocks, block{kind: "activity", text: label, icon: icon,
			key: "activity:" + d.ID, detail: commandDetail(lines...)})
	case "session.tool.success":
		var d evToolData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		idx, ok := a.toolBlocks[d.ID]
		if !ok {
			return
		}
		delete(a.toolBlocks, d.ID)
		run := toolRun{command: a.toolCmds[d.ID],
			exit: d.Metadata.Exit, truncated: d.Metadata.Truncated}
		delete(a.toolCmds, d.ID)
		run.out = textOf(d.Content)
		a.blocks[idx].detail = commandDetail(toolDetailLines(run)...)
	case "permission.asked":
		var d evPermissionData
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		if _, ok := a.pending[d.ID]; ok {
			return
		}
		perm := &agentPerm{id: d.ID, session: d.SessionID,
			action: d.Action, resources: d.Resources, save: d.Save}
		a.pending[d.ID] = perm
		a.blocks = append(a.blocks, block{kind: "permission",
			text: "Approval needed", key: "permission:" + d.ID, req: perm})
	case "session.execution.started":
		var d struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		a.busySession = d.SessionID
	case "session.execution.succeeded", "session.execution.failed", "session.execution.interrupted":
		var d struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.Data, &d) != nil || !a.belongs(d.SessionID) {
			return
		}
		if a.busySession == d.SessionID {
			a.busySession = ""
		}
	case "session.renamed":
		var d struct {
			SessionID string `json:"sessionID"`
			Title     string `json:"title"`
		}
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		for i := range a.sessions {
			if a.sessions[i].id == d.SessionID {
				a.sessions[i].title = d.Title
			}
		}
	}
}

// belongs reports whether a session's stream belongs to the conversation
// on show: only when the transcripts match, or when nothing real is open.
func (a *app) belongs(sid string) bool {
	return a.selectedSession == "" || sid == "" || sid == a.selectedSession
}

// dropPerm takes an approval card out of the transcript once its reply
// has landed; the tool row below it carries the story from there.
func (a *app) dropPerm(requestID string) {
	if _, ok := a.pending[requestID]; !ok {
		return
	}
	delete(a.pending, requestID)
	for i, b := range a.blocks {
		if b.key == "permission:"+requestID {
			a.blocks = append(a.blocks[:i], a.blocks[i+1:]...)
			break
		}
	}
}

// dropSession takes one conversation out of the list and, when it was
// the one on show, leaves a fresh transcript behind.
func (a *app) dropSession(sid string) {
	for i, s := range a.sessions {
		if s.id == sid {
			a.sessions = append(a.sessions[:i], a.sessions[i+1:]...)
			break
		}
	}
	for id, p := range a.pending {
		if p.session == sid {
			delete(a.pending, id)
		}
	}
	if a.selectedSession != sid {
		return
	}
	a.selectedSession = ""
	a.selected = ""
	a.freshTranscript()
}

// dedupeSessions adds the rows the list does not have yet, keeping what
// came first, since later pages hold the older conversations.
func dedupeSessions(all, add []agentSession) []agentSession {
	seen := map[string]bool{}
	for _, s := range all {
		seen[s.id] = true
	}
	out := all
	for _, s := range add {
		if !seen[s.id] {
			seen[s.id] = true
			out = append(out, s)
		}
	}
	return out
}

// toolLook pairs a tool's name with the activity row's label and icon as
// the design draws them.
func toolLook(name string) (string, *ui.SVG) {
	switch name {
	case "shell":
		return "Ran command", icTerminal
	case "read":
		return "Read files", icBook
	case "grep", "glob", "list", "find":
		return "Searched", icBook
	case "":
		return "Used tool", icPlug
	default:
		return "Used " + name, icPlug
	}
}
