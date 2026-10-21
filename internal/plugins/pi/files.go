package pi

import (
	"bufio"
	"cmp"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
)

// Pi stores each session as a JSONL file: a header line, then entries that form
// a tree through id/parentId. The conversation on show is the path from the
// last entry back to the root.

type header struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`
}

type entry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Name      *string         `json:"name"`     // session_info
	Provider  string          `json:"provider"` // model_change
	ModelID   string          `json:"modelId"`  // model_change
	Level     string          `json:"thinkingLevel"`
	Message   json.RawMessage `json:"message"`
}

type message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Provider   string          `json:"provider"`
	Model      string          `json:"model"`
	ToolCallID string          `json:"toolCallId"`
	IsError    bool            `json:"isError"`
	Usage      struct {
		Input      int `json:"input"`
		Output     int `json:"output"` // includes Reasoning
		Reasoning  int `json:"reasoning"`
		CacheRead  int `json:"cacheRead"`
		CacheWrite int `json:"cacheWrite"`
		Cost       struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

type block struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Thinking  string         `json:"thinking"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	MimeType  string         `json:"mimeType"`
}

// sessionFile is what the list needs from one file.
type sessionFile struct {
	path    string
	modTime time.Time
	size    int64
	session agent.Session
}

// readSession reads a whole session file: its summary and its active path.
func readSession(path string) (sessionFile, []entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return sessionFile{}, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return sessionFile{}, nil, err
	}
	sf := sessionFile{path: path, modTime: info.ModTime(), size: info.Size()}
	r := bufio.NewReaderSize(f, 64*1024)
	var h header
	var entries []entry
	first := true
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			if first {
				if json.Unmarshal(line, &h) != nil || h.Type != "session" {
					return sessionFile{}, nil, os.ErrInvalid
				}
				first = false
			} else {
				var e entry
				if json.Unmarshal(line, &e) == nil && e.ID != "" {
					entries = append(entries, e)
				}
			}
		}
		if err != nil {
			break
		}
	}
	if first {
		return sessionFile{}, nil, os.ErrInvalid
	}
	active := activePath(entries)
	s := agent.Session{ID: agent.SessionID(h.ID), Directory: h.Cwd, Updated: info.ModTime().UnixMilli()}
	for _, e := range entries {
		switch e.Type {
		case "session_info":
			if e.Name != nil {
				s.Title = *e.Name
			}
		case "model_change":
			s.Model = agent.ModelRef{Provider: e.Provider, ID: e.ModelID, Variant: s.Model.Variant}
		case "thinking_level_change":
			s.Model.Variant = e.Level
		}
	}
	for _, e := range active {
		if e.Type != "message" {
			continue
		}
		var m message
		if json.Unmarshal(e.Message, &m) != nil {
			continue
		}
		if m.Role == "assistant" {
			s.Cost += m.Usage.Cost.Total
		}
		if s.Title == "" && m.Role == "user" {
			s.Title = firstLine(textOf(m.Content))
		}
	}
	sf.session = s
	return sf, active, nil
}

// activePath walks from the last entry to the root.
func activePath(entries []entry) []entry {
	if len(entries) == 0 {
		return nil
	}
	byID := make(map[string]int, len(entries))
	for i, e := range entries {
		byID[e.ID] = i
	}
	var path []entry
	seen := map[string]bool{}
	for i, ok := len(entries)-1, true; ok && !seen[entries[i].ID]; {
		e := entries[i]
		seen[e.ID] = true
		path = append(path, e)
		if e.ParentID == nil {
			break
		}
		i, ok = byID[*e.ParentID]
	}
	slices.Reverse(path)
	return path
}

// messagesOf converts an active path into contract messages. Tool results
// are folded into the assistant tool call they answer.
func messagesOf(path []entry) []agent.Message {
	var out []agent.Message
	tools := map[string]*agent.Tool{}
	for _, e := range path {
		if e.Type != "message" {
			continue
		}
		var m message
		if json.Unmarshal(e.Message, &m) != nil {
			continue
		}
		switch m.Role {
		case "user":
			out = append(out, agent.Message{ID: agent.MessageID(e.ID), Role: agent.RoleUser, Text: textOf(m.Content),
				Files: imagesOf(m.Content)})
		case "assistant":
			msg := agent.Message{ID: agent.MessageID(e.ID), Role: agent.RoleAssistant,
				Model: agent.ModelRef{Provider: m.Provider, ID: m.Model}, Cost: m.Usage.Cost.Total,
				Tokens: agent.Tokens{Input: m.Usage.Input, Output: max(m.Usage.Output-m.Usage.Reasoning, 0),
					Reasoning: m.Usage.Reasoning, CacheRead: m.Usage.CacheRead, CacheWrite: m.Usage.CacheWrite}}
			var blocks []block
			json.Unmarshal(m.Content, &blocks)
			for _, b := range blocks {
				switch b.Type {
				case "text":
					msg.Parts = append(msg.Parts, agent.Part{Kind: agent.PartText, Text: b.Text})
				case "thinking":
					msg.Parts = append(msg.Parts, agent.Part{Kind: agent.PartReasoning, Text: b.Thinking})
				case "toolCall":
					t := &agent.Tool{ID: b.ID, Name: b.Name, Command: commandOf(b.Arguments)}
					tools[b.ID] = t
					msg.Parts = append(msg.Parts, agent.Part{Kind: agent.PartTool, Tool: t})
				}
			}
			out = append(out, msg)
		case "toolResult":
			if t := tools[m.ToolCallID]; t != nil {
				t.Output = textPieces(m.Content)
				t.Status = "completed"
				if m.IsError {
					t.Status = "error"
				}
			}
		}
	}
	return out
}

// textOf joins a content value's text: a string, or text blocks.
func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.Join(textPieces(raw), "\n")
}

// imagesOf names the images of a user message; Pi keeps no file names.
func imagesOf(raw json.RawMessage) []agent.File {
	var blocks []block
	json.Unmarshal(raw, &blocks)
	var out []agent.File
	for _, b := range blocks {
		if b.Type == "image" {
			out = append(out, agent.File{Name: "image", MIME: b.MimeType})
		}
	}
	return out
}

func textPieces(raw json.RawMessage) []string {
	var blocks []block
	json.Unmarshal(raw, &blocks)
	var out []string
	for _, b := range blocks {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return out
}

func commandOf(args map[string]any) string {
	if c, ok := args["command"].(string); ok {
		return c
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80]) + "…"
	}
	return s
}

// catalog lists session files under dir, newest first, reusing cached
// summaries for files that haven't changed.
type catalog struct {
	dir   string
	cache map[string]sessionFile
}

func (c *catalog) scan() ([]sessionFile, error) {
	if c.cache == nil {
		c.cache = map[string]sessionFile{}
	}
	var files []sessionFile
	seen := map[string]bool{}
	err := filepath.WalkDir(c.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == c.dir && os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[path] = true
		if cached, ok := c.cache[path]; ok && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
			files = append(files, cached)
			return nil
		}
		sf, _, err := readSession(path)
		if err != nil {
			return nil
		}
		c.cache[path] = sf
		files = append(files, sf)
		return nil
	})
	for path := range c.cache {
		if !seen[path] {
			delete(c.cache, path)
		}
	}
	slices.SortFunc(files, func(a, b sessionFile) int { return cmp.Compare(b.session.Updated, a.session.Updated) })
	return files, err
}

const pageSize = 50

// page filters and pages the scanned files. The cursor is an offset.
func page(files []sessionFile, q agent.SessionQuery) agent.SessionPage {
	search := strings.ToLower(strings.TrimSpace(q.Search))
	var match []agent.Session
	for _, f := range files {
		s := f.session
		if search != "" && !strings.Contains(strings.ToLower(s.Title), search) {
			continue
		}
		if q.Directory != "" && filepath.Clean(s.Directory) != filepath.Clean(q.Directory) {
			continue
		}
		match = append(match, s)
	}
	start, _ := strconv.Atoi(q.Cursor)
	start = min(max(start, 0), len(match))
	end := min(start+pageSize, len(match))
	out := agent.SessionPage{Sessions: append([]agent.Session{}, match[start:end]...)}
	if end < len(match) {
		out.Next = strconv.Itoa(end)
	}
	return out
}
