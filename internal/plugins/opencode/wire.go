package opencode

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"wada-desktop-go/internal/contract/agent"
)

// Wire shapes and their conversion to the contract. Nothing here leaves the
// package unconverted.

type wireModel struct {
	ID       string `json:"id"`
	Provider string `json:"providerID"`
	Variant  string `json:"variant,omitempty"`
}

func (m wireModel) ref() agent.ModelRef {
	return agent.ModelRef{Provider: m.Provider, ID: m.ID, Variant: m.Variant}
}

func toWireModel(m agent.ModelRef) wireModel {
	return wireModel{ID: m.ID, Provider: m.Provider, Variant: m.Variant}
}

func sessionPath(id agent.SessionID, rest string) string {
	return "/session/" + url.PathEscape(string(id)) + rest
}

func cleanDir(dir string) string {
	if dir == "" {
		return ""
	}
	return path.Clean(dir)
}

// ---- Sessions

// sessionListReply is the session list (GET /session).
type sessionListReply struct {
	Data []struct {
		ID       string `json:"id"`
		ParentID string `json:"parentID"`
		Title    string `json:"title"`
		Location struct {
			Directory string `json:"directory"`
		} `json:"location"`
		Model wireModel `json:"model"`
		Cost  float64   `json:"cost"`
		Time  struct {
			Created int64 `json:"created"`
			Updated int64 `json:"updated"`
		} `json:"time"`
	} `json:"data"`
	Cursor struct {
		Next string `json:"next"`
	} `json:"cursor"`
}

func (c *client) sessions(ctx context.Context, q agent.SessionQuery) (agent.SessionPage, error) {
	route := "/session?limit=50&parentID=null"
	if q.Cursor != "" {
		route += "&cursor=" + url.QueryEscape(q.Cursor)
	}
	if q.Search != "" {
		route += "&search=" + url.QueryEscape(q.Search)
	}
	if q.Directory != "" {
		route += "&directory=" + url.QueryEscape(q.Directory)
	}
	var out sessionListReply
	if err := c.decode(ctx, "GET", route, nil, &out); err != nil {
		return agent.SessionPage{}, err
	}
	page := agent.SessionPage{Sessions: []agent.Session{}, Next: out.Cursor.Next}
	for _, s := range out.Data {
		if s.ParentID != "" { // a subagent's session belongs to its parent
			continue
		}
		updated := cmp.Or(s.Time.Updated, s.Time.Created)
		page.Sessions = append(page.Sessions, agent.Session{ID: agent.SessionID(s.ID), Title: s.Title,
			Directory: cleanDir(s.Location.Directory), Updated: updated, Model: s.Model.ref(), Cost: s.Cost})
	}
	slices.SortStableFunc(page.Sessions, func(a, b agent.Session) int { return cmp.Compare(b.Updated, a.Updated) })
	return page, nil
}

// sessionCreateReply is a new session (POST /session).
type sessionCreateReply struct {
	Data struct {
		ID       string `json:"id"`
		Location struct {
			Directory string `json:"directory"`
		} `json:"location"`
		Model wireModel `json:"model"`
	} `json:"data"`
}

func (c *client) create(ctx context.Context, req agent.CreateSession) (agent.Session, error) {
	body := map[string]any{}
	if req.Directory != "" {
		body["location"] = map[string]any{"directory": req.Directory}
	}
	if req.Mode != "" {
		body["agent"] = req.Mode
	}
	var out sessionCreateReply
	if err := c.decode(ctx, "POST", "/session", body, &out); err != nil {
		return agent.Session{}, err
	}
	if out.Data.ID == "" {
		return agent.Session{}, errors.New("the server created a session without an ID")
	}
	s := agent.Session{ID: agent.SessionID(out.Data.ID), Directory: cleanDir(cmp.Or(out.Data.Location.Directory, req.Directory)),
		Model: out.Data.Model.ref()}
	if !req.Model.IsZero() {
		if err := c.setModel(ctx, s.ID, req.Model); err != nil {
			return s, fmt.Errorf("the model was not set: %w", err)
		}
		s.Model = req.Model
	}
	return s, nil
}

// send posts a prompt. Files go inline as data URIs, which a remote server
// can read as well as a local one.
func (c *client) send(ctx context.Context, req agent.Send) error {
	if req.Mode != "" {
		if err := c.decode(ctx, "POST", sessionPath(req.Session, "/agent"), map[string]string{"agent": req.Mode}, nil); err != nil {
			return fmt.Errorf("the mode was not set; the prompt was not sent: %w", err)
		}
	}
	body := map[string]any{"text": req.Text}
	if len(req.Files) > 0 {
		files := make([]map[string]string, len(req.Files))
		for i, f := range req.Files {
			files[i] = map[string]string{"name": f.Name,
				"uri": "data:" + cmp.Or(f.MIME, "application/octet-stream") + ";base64," + base64.StdEncoding.EncodeToString(f.Data)}
		}
		body["files"] = files
	}
	return c.decode(ctx, "POST", sessionPath(req.Session, "/prompt"), body, nil)
}

// ---- Files

// fileFindReply is the entries a search found (GET /fs/find).
type fileFindReply struct {
	Data []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"data"`
}

// find asks the server's file index for files and folders whose path matches
// query. A zero limit takes the server's default.
func (c *client) find(ctx context.Context, directory, query string, limit int) ([]agent.FileEntry, error) {
	v := url.Values{"query": {query}}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if directory != "" {
		v.Set("location[directory]", directory)
	}
	var out fileFindReply
	if err := c.decode(ctx, "GET", "/fs/find?"+v.Encode(), nil, &out); err != nil {
		return nil, err
	}
	entries := make([]agent.FileEntry, 0, len(out.Data))
	for _, f := range out.Data {
		rel := strings.TrimSuffix(f.Path, "/") // the contract wants no trailing slash
		if rel == "" {
			continue // an empty path names nothing
		}
		entries = append(entries, agent.FileEntry{Path: rel, IsDir: f.Type == "directory"})
	}
	return entries, nil
}

// read fetches a file of the server's folder. The route takes the path as
// part of the address, so only a clean path inside the folder goes out.
func (c *client) read(ctx context.Context, directory, rel string) ([]byte, error) {
	clean := path.Clean(rel)
	if rel == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, fmt.Errorf("%q is not a path inside the folder", rel)
	}
	segments := strings.Split(clean, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	route := "/fs/read/" + strings.Join(segments, "/")
	if directory != "" {
		route += "?" + url.Values{"location[directory]": {directory}}.Encode()
	}
	raw, header, err := c.do(ctx, "GET", route, nil)
	if err != nil {
		return nil, err
	}
	// A server without this route answers with its web page, not an error.
	if ext := strings.ToLower(path.Ext(clean)); ext != ".html" && ext != ".htm" && strings.HasPrefix(header.Get("Content-Type"), "text/html") {
		return nil, errors.New("the server does not serve files")
	}
	return raw, nil
}

func (c *client) abort(ctx context.Context, id agent.SessionID) error {
	return c.decode(ctx, "POST", sessionPath(id, "/interrupt"), struct{}{}, nil)
}

var decisions = map[agent.Decision]string{agent.Once: "once", agent.Always: "always", agent.Reject: "reject"}

func (c *client) reply(ctx context.Context, req agent.Reply) error {
	decision, ok := decisions[req.Decision]
	if !ok {
		return fmt.Errorf("unknown decision %d", req.Decision)
	}
	return c.decode(ctx, "POST", sessionPath(req.Session, "/permission/"+url.PathEscape(string(req.Approval))+"/reply"),
		map[string]any{"decision": decision}, nil)
}

func (c *client) rename(ctx context.Context, id agent.SessionID, title string) error {
	return c.decode(ctx, "PATCH", sessionPath(id, ""), map[string]any{"title": title}, nil)
}

func (c *client) delete(ctx context.Context, id agent.SessionID) error {
	return c.decode(ctx, "DELETE", sessionPath(id, ""), nil, nil)
}

// rewind stages a revert to before message without touching the files, then
// commits it, which removes the message and everything after it.
func (c *client) rewind(ctx context.Context, id agent.SessionID, message agent.MessageID) error {
	if err := c.decode(ctx, "POST", sessionPath(id, "/revert/stage"),
		map[string]any{"messageID": string(message), "files": false}, nil); err != nil {
		return err
	}
	if err := c.decode(ctx, "POST", sessionPath(id, "/revert/commit"), nil, nil); err != nil {
		return fmt.Errorf("the rewind was staged but not committed: %w", err)
	}
	return nil
}

// sessionDiffReply is a session's diff (GET /session/{id}/diff).
type sessionDiffReply struct {
	Data []struct {
		File      string `json:"file"`
		Patch     string `json:"patch"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		Status    string `json:"status"`
	} `json:"data"`
}

// changes reads the session's diff: each file as it is now against before
// the session changed it.
func (c *client) changes(ctx context.Context, id agent.SessionID) ([]agent.FileChange, error) {
	var out sessionDiffReply
	if err := c.decode(ctx, "GET", sessionPath(id, "/diff"), nil, &out); err != nil {
		return nil, err
	}
	changes := make([]agent.FileChange, 0, len(out.Data))
	for _, d := range out.Data {
		if d.File == "" {
			continue
		}
		ch := agent.FileChange{Path: d.File, Added: d.Additions, Removed: d.Deletions, Patch: d.Patch}
		switch d.Status {
		case "added":
			ch.Status = agent.FileAdded
		case "deleted":
			ch.Status = agent.FileDeleted
		}
		changes = append(changes, ch)
	}
	return changes, nil
}

// toolKind maps OpenCode's tool and permission names onto the shared kinds.
func toolKind(name string) agent.ToolKind {
	switch name {
	case "shell", "bash":
		return agent.ToolShell
	case "read":
		return agent.ToolRead
	case "grep", "glob", "list", "find", "codesearch":
		return agent.ToolSearch
	case "edit", "write", "patch", "multiedit", "apply_patch":
		return agent.ToolEdit
	case "webfetch", "websearch":
		return agent.ToolFetch
	}
	return agent.ToolOther
}

// ---- Messages

type wireOutput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textPieces(out []wireOutput) []string {
	var pieces []string
	for _, o := range out {
		if o.Type == "text" {
			pieces = append(pieces, o.Text)
		}
	}
	return pieces
}

type wireMessage struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Cost   float64 `json:"cost"`
	Tokens struct {
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Text    string     `json:"text"`
	Files   []wireFile `json:"files"`
	Model   wireModel  `json:"model"`
	Content []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		State struct {
			Status string `json:"status"`
			Input  struct {
				Command string `json:"command"`
			} `json:"input"`
			Content  []wireOutput `json:"content"`
			Metadata struct {
				Exit      int  `json:"exit"`
				Truncated bool `json:"truncated"`
			} `json:"metadata"`
		} `json:"state"`
	} `json:"content"`
}

// wireFile is a stored file attachment; its data is left undecoded.
type wireFile struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
}

func (m wireMessage) message() agent.Message {
	out := agent.Message{ID: agent.MessageID(m.ID), Text: m.Text, Model: m.Model.ref(), Cost: m.Cost,
		Tokens: agent.Tokens{Input: m.Tokens.Input, Output: m.Tokens.Output, Reasoning: m.Tokens.Reasoning,
			CacheRead: m.Tokens.Cache.Read, CacheWrite: m.Tokens.Cache.Write}}
	for _, f := range m.Files {
		out.Files = append(out.Files, agent.File{Name: cmp.Or(f.Name, "file"), MIME: f.MIME})
	}
	switch m.Type {
	case "user":
		out.Role = agent.RoleUser
	case "assistant":
		out.Role = agent.RoleAssistant
	}
	for _, p := range m.Content {
		part := agent.Part{Text: p.Text}
		switch p.Type {
		case "text":
			part.Kind = agent.PartText
		case "reasoning":
			part.Kind = agent.PartReasoning
		case "tool":
			part.Kind, part.Text = agent.PartTool, ""
			part.Tool = &agent.Tool{ID: p.ID, Name: p.Name, Kind: toolKind(p.Name), Command: p.State.Input.Command, Output: textPieces(p.State.Content),
				Status: p.State.Status, Exit: p.State.Metadata.Exit, Truncated: p.State.Metadata.Truncated}
		}
		out.Parts = append(out.Parts, part)
	}
	return out
}

// messageListReply is a session's messages (GET /session/{id}/message).
type messageListReply struct {
	Data []wireMessage `json:"data"`
}

func (c *client) messages(ctx context.Context, id agent.SessionID) ([]agent.Message, error) {
	var out messageListReply
	if err := c.decode(ctx, "GET", sessionPath(id, "/message?order=asc"), nil, &out); err != nil {
		return nil, err
	}
	messages := make([]agent.Message, len(out.Data))
	for i, m := range out.Data {
		messages[i] = m.message()
	}
	return messages, nil
}

// ---- Models and modes

// modelListReply is the model catalog (GET /model).
type modelListReply struct {
	Data []struct {
		ID       string `json:"id"`
		Provider string `json:"providerID"`
		Name     string `json:"name"`
		Variants []struct {
			ID string `json:"id"`
		} `json:"variants"`
		Cost []struct {
			Input  float64 `json:"input"`
			Output float64 `json:"output"`
		} `json:"cost"`
		Limit struct {
			Context int `json:"context"`
		} `json:"limit"`
	} `json:"data"`
}

func (c *client) models(ctx context.Context) ([]agent.Model, error) {
	var out modelListReply
	if err := c.decode(ctx, "GET", "/model", nil, &out); err != nil {
		return nil, err
	}
	models := []agent.Model{}
	for _, m := range out.Data {
		row := agent.Model{Ref: agent.ModelRef{Provider: m.Provider, ID: m.ID}, Name: m.Name, Free: true, Context: m.Limit.Context}
		for _, v := range m.Variants {
			row.Variants = append(row.Variants, v.ID)
		}
		for _, cost := range m.Cost {
			if cost.Input != 0 || cost.Output != 0 {
				row.Free = false
			}
		}
		models = append(models, row)
	}
	return models, nil
}

// defaultModelReply is the default model (GET /model/default).
type defaultModelReply struct {
	Data struct {
		ID       string `json:"id"`
		Provider string `json:"providerID"`
	} `json:"data"`
}

func (c *client) defaultModel(ctx context.Context) (agent.ModelRef, error) {
	var out defaultModelReply
	if err := c.decode(ctx, "GET", "/model/default", nil, &out); err != nil {
		return agent.ModelRef{}, err
	}
	if out.Data.ID == "" || out.Data.Provider == "" {
		return agent.ModelRef{}, errors.New("the server named no default model")
	}
	return agent.ModelRef{Provider: out.Data.Provider, ID: out.Data.ID}, nil
}

func (c *client) setModel(ctx context.Context, id agent.SessionID, m agent.ModelRef) error {
	return c.decode(ctx, "POST", sessionPath(id, "/model"), map[string]any{"model": toWireModel(m)}, nil)
}

// agentListReply is the agents (GET /agent).
type agentListReply struct {
	Data []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Mode        string `json:"mode"`
		Hidden      bool   `json:"hidden"`
	} `json:"data"`
}

func (c *client) modes(ctx context.Context) ([]agent.Mode, error) {
	var out agentListReply
	if err := c.decode(ctx, "GET", "/agent", nil, &out); err != nil {
		return nil, err
	}
	modes := []agent.Mode{}
	for _, m := range out.Data {
		if m.Mode == "primary" && !m.Hidden && m.ID != "" {
			modes = append(modes, agent.Mode{ID: m.ID, Name: m.Name, Description: m.Description})
		}
	}
	return modes, nil
}

// ---- Events

// errorText reads an error given as a string or as an object with a message.
func errorText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return cmp.Or(obj.Message, obj.Data.Message)
	}
	return ""
}

// decodeEvent ignores unknown event types, and rejects malformed known ones
// without quoting remote payloads in the error.
// eventKinds names the kind of the events whose case covers several, built
// once rather than for every event of a stream.
var eventKinds = map[string]agent.EventKind{
	"session.text.started": agent.TextStarted, "session.text.delta": agent.TextDelta,
	"session.text.ended": agent.TextEnded, "session.tool.input.started": agent.ToolStarted,
	"session.tool.called": agent.ToolCalled, "session.tool.success": agent.ToolFinished,
	"session.execution.started": agent.TurnStarted, "session.execution.succeeded": agent.TurnSucceeded,
	"session.execution.failed": agent.TurnFailed, "session.execution.interrupted": agent.TurnInterrupted,
	"session.model.selected": agent.ModelChanged, "session.renamed": agent.SessionRenamed,
}

func decodeEvent(ev wireEvent) (agent.Event, bool, error) {
	out := agent.Event{Bytes: len(ev.Data)}
	var err error
	switch ev.Type {
	case "session.text.started", "session.text.delta", "session.text.ended":
		out.Kind = eventKinds[ev.Type]
		var d struct {
			Session string `json:"sessionID"`
			Message string `json:"assistantMessageID"`
			Ordinal int    `json:"ordinal"`
			Delta   string `json:"delta"`
			Text    string `json:"text"`
		}
		err = json.Unmarshal(ev.Data, &d)
		if err == nil && (d.Message == "" || d.Ordinal < 0) {
			err = errors.New("invalid text identity")
		}
		out.Session = agent.SessionID(d.Session)
		out.Text = &agent.TextEvent{Message: agent.MessageID(d.Message), Ordinal: d.Ordinal, Delta: d.Delta, Text: d.Text}
	case "session.tool.input.started", "session.tool.called", "session.tool.success":
		out.Kind = eventKinds[ev.Type]
		var d struct {
			Session string          `json:"sessionID"`
			ID      string          `json:"id"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			Content []wireOutput    `json:"content"`
			Meta    struct {
				Status    string `json:"status"`
				Truncated bool   `json:"truncated"`
				Exit      int    `json:"exit"`
			} `json:"metadata"`
		}
		err = json.Unmarshal(ev.Data, &d)
		if err == nil && d.ID == "" {
			err = errors.New("invalid tool identity")
		}
		var input struct {
			Command string `json:"command"`
		}
		if err == nil && len(d.Input) > 0 {
			err = json.Unmarshal(d.Input, &input)
		}
		out.Session = agent.SessionID(d.Session)
		out.Tool = &agent.Tool{ID: d.ID, Name: d.Name, Kind: toolKind(d.Name), Command: input.Command, Output: textPieces(d.Content),
			Status: d.Meta.Status, Exit: d.Meta.Exit, Truncated: d.Meta.Truncated}
	case "permission.asked":
		out.Kind = agent.ApprovalRequested
		var d struct {
			Session   string   `json:"sessionID"`
			ID        string   `json:"id"`
			Action    string   `json:"action"`
			Resources []string `json:"resources"`
			Save      []string `json:"save"`
		}
		err = json.Unmarshal(ev.Data, &d)
		if err == nil && (d.ID == "" || d.Action == "") {
			err = errors.New("invalid permission identity")
		}
		out.Session = agent.SessionID(d.Session)
		out.Approval = &agent.Approval{ID: agent.ApprovalID(d.ID), Kind: toolKind(d.Action), Action: d.Action, Resources: d.Resources, Save: d.Save}
	case "session.execution.started", "session.execution.succeeded", "session.execution.failed",
		"session.execution.interrupted", "session.model.selected", "session.renamed":
		out.Kind = eventKinds[ev.Type]
		var d struct {
			Session string          `json:"sessionID"`
			Model   wireModel       `json:"model"`
			Title   string          `json:"title"`
			Error   json.RawMessage `json:"error"`
		}
		err = json.Unmarshal(ev.Data, &d)
		if out.Kind == agent.TurnFailed {
			out.Error = errorText(d.Error)
		}
		if err == nil && out.Kind == agent.ModelChanged && (d.Model.ID == "" || d.Model.Provider == "") {
			err = errors.New("invalid model identity")
		}
		out.Session, out.Title = agent.SessionID(d.Session), d.Title
		if out.Kind == agent.ModelChanged {
			out.Model = d.Model.ref()
		}
	default:
		return agent.Event{}, false, nil
	}
	if err != nil || out.Session == "" {
		return agent.Event{}, true, fmt.Errorf("malformed %s event", ev.Type)
	}
	return out, true, nil
}
