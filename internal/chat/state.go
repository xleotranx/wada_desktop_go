package chat

import (
	"iter"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// BlockKind identifies a transcript row's presentation.
type SessionID string
type MessageID string
type PermissionID string

type BlockKind string

const (
	KindText       BlockKind = "text"       // an answer, through the markdown package
	KindUser       BlockKind = "user"       // the user's own words
	KindStream     BlockKind = "stream"     // an answer still arriving
	KindNote       BlockKind = "note"       // engine trouble under the answers
	KindWorking    BlockKind = "working"    // the loader of a turn in flight
	KindActivity   BlockKind = "activity"   // one tool call of the agent
	KindPermission BlockKind = "permission" // an approval waiting on the user
)

// Block is one paragraph, bubble or activity line of the transcript.
type Block struct {
	ID      uint64
	Kind    BlockKind // what the row is; one of the kind* constants
	Text    string
	Icon    string
	Key     string           // stable key for activity expansion
	Detail  []string         // drawn below the line once expanded
	Request *Permission      // the approval request a permission card carries
	stream  *strings.Builder // amortized storage while text deltas arrive
	// The stored turn this row came from, and what it spent: the usage
	// line only the store knows waits under the answer that earned it.
	Message MessageID
	Cost    float64
	Tokens  int
}

// Permission is an approval request the agent raised mid-turn, held onto so
// its card's buttons know where to answer.
type Permission struct {
	ID        PermissionID
	Session   SessionID
	Action    string
	Resources []string
	Save      []string
}

// State owns transcript mutation. Callers read values and retain UI state
// separately; a stream builder never leaves this package.
type State struct {
	rows                    []Block
	live                    map[string]int
	toolNames, toolCommands map[string]string
	toolRows                map[string]int
	pending                 map[PermissionID]*Permission
	busy                    SessionID
	nextID                  uint64
	completed               bool
}

// Row returns data by value; mutable details and permissions are copied.
func (s *State) Row(i int) Block { return cloneBlock(s.rows[i]) }
func (s *State) Len() int        { return len(s.rows) }
func (s *State) All() iter.Seq2[int, Block] {
	return func(yield func(int, Block) bool) {
		for i := range s.rows {
			if !yield(i, s.Row(i)) {
				return
			}
		}
	}
}
func (s *State) Snapshot() []Block {
	rows := make([]Block, len(s.rows))
	for i := range rows {
		rows[i] = s.Row(i)
	}
	return rows
}
func (s *State) Busy() SessionID { return s.busy }
func clonePermission(p *Permission) *Permission {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Resources = slices.Clone(p.Resources)
	copy.Save = slices.Clone(p.Save)
	return &copy
}
func cloneBlock(b Block) Block {
	b.stream = nil
	b.Detail = slices.Clone(b.Detail)
	b.Request = clonePermission(b.Request)
	return b
}
func (s *State) Pending() map[PermissionID]*Permission {
	if len(s.pending) == 0 {
		return nil
	}
	out := make(map[PermissionID]*Permission, len(s.pending))
	for id, p := range s.pending {
		out[id] = clonePermission(p)
	}
	return out
}
func (s *State) Reset() {
	s.rows = nil
	s.live = map[string]int{}
	s.toolNames = map[string]string{}
	s.toolCommands = map[string]string{}
	s.toolRows = map[string]int{}
	s.pending = map[PermissionID]*Permission{}
	s.busy = ""
	s.completed = false
}
func (s *State) Replace(rows []Block) {
	s.Reset()
	s.rows = make([]Block, len(rows))
	for i, row := range rows {
		s.rows[i] = cloneBlock(row)
		if row.Kind == KindPermission && row.Request != nil {
			s.pending[row.Request.ID] = clonePermission(row.Request)
		}
	}
}
func (s *State) Append(rows ...Block) {
	for _, row := range rows {
		s.rows = append(s.rows, cloneBlock(row))
	}
}
func (s *State) Identity(i int) uint64 {
	if s.rows[i].ID == 0 {
		s.nextID++
		s.rows[i].ID = s.nextID
	}
	return s.rows[i].ID
}
func (s *State) Start(session SessionID) { s.busy = session; s.completed = false }
func (s *State) Finish() bool {
	if s.completed {
		return false
	}
	s.completed = true
	s.busy = ""
	return true
}
func (s *State) Ask(p *Permission) {
	if s.pending == nil {
		s.pending = map[PermissionID]*Permission{}
	}
	if _, ok := s.pending[p.ID]; ok {
		return
	}
	p = clonePermission(p)
	s.pending[p.ID] = p
	s.Append(Block{Kind: KindPermission, Text: "Approval needed", Key: "permission:" + string(p.ID), Request: p})
}
func (s *State) ClearPermissions() { s.pending = map[PermissionID]*Permission{} }
func (s *State) DropPermission(id PermissionID) {
	if _, ok := s.pending[id]; !ok {
		return
	}
	delete(s.pending, id)
	for i, b := range s.rows {
		if b.Key != "permission:"+string(id) {
			continue
		}
		s.rows = slices.Delete(s.rows, i, i+1)
		for key, index := range s.live {
			if index > i {
				s.live[key] = index - 1
			}
		}
		for key, index := range s.toolRows {
			if index > i {
				s.toolRows[key] = index - 1
			}
		}
		return
	}
}
func (s *State) DropSession(id SessionID) {
	for key, p := range s.pending {
		if p.Session == id {
			s.DropPermission(key)
		}
	}
}
func (s *State) StartText(message MessageID, ordinal int) {
	if s.live == nil {
		s.live = map[string]int{}
	}
	key := string(message) + ":" + strconv.Itoa(ordinal)
	if _, ok := s.live[key]; ok {
		return
	}
	s.live[key] = len(s.rows)
	s.Append(Block{Kind: KindStream, Key: "stream:" + key, Message: message})
}
func (s *State) Delta(message MessageID, ordinal int, text string) {
	if index, ok := s.live[string(message)+":"+strconv.Itoa(ordinal)]; ok {
		b := &s.rows[index]
		if b.stream == nil {
			b.stream = new(strings.Builder)
			b.stream.WriteString(b.Text)
		}
		b.stream.WriteString(text)
		b.Text = b.stream.String()
	}
}
func (s *State) EndText(message MessageID, ordinal int, text string) {
	key := string(message) + ":" + strconv.Itoa(ordinal)
	if index, ok := s.live[key]; ok {
		delete(s.live, key)
		b := &s.rows[index]
		b.Kind = KindText
		b.Text = text
		b.stream = nil
	}
}
func (s *State) ToolStarted(id, name string) {
	if s.toolNames == nil {
		s.toolNames = map[string]string{}
	}
	s.toolNames[id] = name
}
func (s *State) ToolCalled(id, command string) {
	if s.toolRows == nil {
		s.toolRows = map[string]int{}
		s.toolCommands = map[string]string{}
	}
	if _, ok := s.toolRows[id]; ok {
		return
	}
	name := s.toolNames[id]
	label, icon := ToolLook(name)
	var lines []string
	if command != "" {
		lines = []string{"$ " + command}
		s.toolCommands[id] = command
	}
	s.toolRows[id] = len(s.rows)
	s.Append(Block{Kind: KindActivity, Text: label, Icon: icon, Key: "activity:" + id, Detail: lines})
}
func (s *State) ToolFinished(id string, lines []string) {
	if index, ok := s.toolRows[id]; ok {
		delete(s.toolRows, id)
		delete(s.toolCommands, id)
		delete(s.toolNames, id)
		s.rows[index].Detail = slices.Clone(lines)
	}
}
func (s *State) ToolCommand(id string) string { return s.toolCommands[id] }
func (s *State) Usage(message MessageID, cost float64, tokens int) {
	for i := len(s.rows) - 1; i >= 0; i-- {
		if s.rows[i].Message == message {
			s.rows[i].Cost = cost
			s.rows[i].Tokens = tokens
			return
		}
	}
}
func (s *State) Cut() {
	for i := range s.rows {
		b := &s.rows[i]
		if b.Kind == KindStream {
			b.Kind = KindText
			b.stream = nil
			if strings.TrimSpace(b.Text) == "" {
				b.Text = "(the answer was cut short)"
			}
		}
	}
	for id := range s.pending {
		s.DropPermission(id)
	}
	s.live = map[string]int{}
	s.toolRows = map[string]int{}
	s.toolNames = map[string]string{}
	s.toolCommands = map[string]string{}
	s.busy = ""
	s.completed = true
	s.ClearPermissions()
}

// toolLook pairs a tool's name with the activity row's label and icon as
// the design draws them.
func ToolLook(name string) (string, string) {
	switch name {
	case "shell":
		return "Ran command", "shell"
	case "read":
		return "Read files", "read"
	case "grep", "glob", "list", "find":
		return "Searched", "read"
	case "":
		return "Used tool", "tool"
	default:
		return "Used " + name, "tool"
	}
}

func (s *State) Streaming() map[string]int { return maps.Clone(s.live) }
