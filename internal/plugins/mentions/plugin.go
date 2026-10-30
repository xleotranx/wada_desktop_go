// Package mentions lets a message name files and folders of the conversation's
// folder. Typing "@" in the composer lists matching files and folders, choosing
// one writes its path into the message, and sending attaches the contents of
// every file still named in the text. A folder is only named; the backend
// explores it itself. The backend finds and reads the files, so it works the
// same on a server that runs somewhere else.
package mentions

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Limits for one message. A mentioned file goes into the model's context, so
// the bounds are tighter than an attached file's.
const (
	findLimit       = 8 // what the composer's list shows
	maxMentions     = 10
	maxMentionBytes = 1 << 20
)

// Plugin is the file mentions plugin.
type Plugin struct {
	ctx   *kernel.Context
	files agent.Files
	chat  chat.Controller
	host  shell.Host

	found    []agent.FileEntry // the files and folders the latest lookup returned, best first
	failed   bool              // a lookup failed since the word was started, and said so
	lookup   kernel.Task
	mentions map[string]*mention // chosen files by path, until the message goes out
	session  agent.SessionID     // the conversation the mentions belong to
	// delay waits before a lookup goes out, so typing sends one.
	delay time.Duration
}

// mention is a file chosen from the list, read for the message.
type mention struct {
	reading bool
	file    agent.File
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "mentions", Title: "File mentions",
		Description: "Type @ to mention a file or folder of the project; a file's contents go with the message.",
		Requires:    []kernel.Named{agent.FilesKey, chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.files = kernel.Require(c, agent.FilesKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.mentions, p.session = map[string]*mention{}, p.chat.Current().Session
	if p.delay == 0 {
		p.delay = 60 * time.Millisecond
	}
	kernel.On(c, chat.Changed, func(conv chat.Conversation) {
		if conv.Session != p.session { // another conversation has its own draft
			p.session = conv.Session
			clear(p.mentions)
		}
	})
	kernel.Transform(c, chat.Outgoing, 1001, p.outgoing)
	return kernel.Contribute(c, chat.Completions, "mentions.files", 0, chat.Completion{
		Trigger: '@', Hint: "mentions a file", Query: p.search, Candidates: p.candidates, Accept: p.accept,
	})
}

// ---- Finding

// search looks for files that match what is typed after "@", replacing a
// lookup in flight. Once the word is over nothing is offered.
func (p *Plugin) search(typed string, active bool) {
	if !active {
		p.lookup.Cancel()
		p.found, p.failed = nil, false
		return
	}
	files, dir, delay := p.files, p.chat.Current().Directory, p.delay
	kernel.Go(p.ctx, &p.lookup, func(ctx context.Context) ([]agent.FileEntry, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if err := ctx.Err(); err != nil { // replaced while waiting
			return nil, err
		}
		return files.Find(ctx, dir, typed, findLimit)
	}, func(entries []agent.FileEntry, err error) {
		if err != nil {
			p.found = nil
			if !p.failed { // one notice per word, not one per keystroke
				p.failed = true
				p.host.Toast(ui.Toast{Title: "Files could not be listed", Description: err.Error(), Type: components.ToastError})
			}
			return
		}
		p.found, p.failed = entries, false
	})
}

// candidates lists the files and folders found, best first. A folder's ID
// carries a trailing slash, which is what accept reads back to tell the two
// apart.
func (p *Plugin) candidates() []chat.Candidate {
	out := make([]chat.Candidate, len(p.found))
	for i, entry := range p.found {
		dir, name := path.Split(entry.Path)
		id, label, icon := entry.Path, name, lucide.FileText
		if entry.IsDir {
			id, label, icon = id+"/", name+"/", lucide.Folder
		}
		out[i] = chat.Candidate{ID: id, Label: label, Detail: strings.TrimSuffix(dir, "/"), Icon: icon}
	}
	return out
}

// ---- Choosing

// accept writes the entry's path into the message. A file starts reading, so
// its contents can go with the message; a folder is only named, for the
// backend to explore. A folder's ID ends in the slash candidates gave it.
func (p *Plugin) accept(c chat.Candidate) string {
	if strings.HasSuffix(c.ID, "/") {
		return "@" + c.ID + " "
	}
	if _, again := p.mentions[c.ID]; !again {
		p.read(c.ID)
	}
	return "@" + c.ID + " "
}

// read gets a chosen file off the UI thread. A file that can't go with the
// message is reported and dropped; its path stays in the text, and the agent
// may still open it itself.
func (p *Plugin) read(file string) {
	if len(p.mentions) >= maxMentions {
		p.refuse(file, fmt.Sprintf("a message takes at most %d mentioned files", maxMentions))
		return
	}
	m := &mention{reading: true}
	p.mentions[file] = m
	files, dir := p.files, p.chat.Current().Directory
	var task kernel.Task // reads don't replace each other
	kernel.Go(p.ctx, &task, func(ctx context.Context) ([]byte, error) {
		return files.Read(ctx, dir, file)
	}, func(data []byte, err error) {
		if p.mentions[file] != m { // the message went out, or the conversation changed
			return
		}
		m.reading = false
		if err == nil {
			err = p.check(file, data)
		}
		if err != nil {
			delete(p.mentions, file)
			p.refuse(file, err.Error())
			return
		}
		m.file = agent.File{Name: file, MIME: mimeOf(data), Data: data}
	})
}

// check says why a file's contents can't go with the message.
func (p *Plugin) check(file string, data []byte) error {
	mime := mimeOf(data)
	switch {
	case len(data) > maxMentionBytes:
		return fmt.Errorf("larger than %d MB", maxMentionBytes>>20)
	case mime == "":
		return fmt.Errorf("only text and images can be mentioned")
	}
	if types, limited := kernel.Use(p.ctx, agent.FileTypesKey); limited && !types.Accepts(mime) {
		return fmt.Errorf("the agent takes only %s", types.Summary())
	}
	return nil
}

func (p *Plugin) refuse(file, why string) {
	p.host.Toast(ui.Toast{Title: path.Base(file) + " was not attached", Description: why, Type: components.ToastError})
}

// mimeOf types file contents: text, or an image by its first bytes. Anything
// else is "".
func mimeOf(data []byte) string {
	if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
		return "text/plain"
	}
	if typ := http.DetectContentType(data); strings.HasPrefix(typ, "image/") {
		return typ
	}
	return ""
}

// ---- Sending

// outgoing attaches the files the text still mentions, in the order it names
// them. A file still being read holds the message back.
func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	type named struct {
		at int
		m  *mention
	}
	var used []named
	for file, m := range p.mentions {
		if at := mentionedAt(d.Text, file); at >= 0 {
			if m.reading {
				return d, nil, fmt.Errorf("%s is still being read", path.Base(file))
			}
			used = append(used, named{at, m})
		}
	}
	slices.SortFunc(used, func(a, b named) int { return a.at - b.at })
	for _, u := range used {
		d.Files = append(d.Files, u.m.file)
	}
	return d, func() { clear(p.mentions) }, nil // what the message didn't name was dropped from it
}

// mentionedAt returns the byte offset of "@file" in text, or -1. The mention
// starts a word and ends one: whitespace, the end, or the punctuation that
// closes a sentence follows it.
func mentionedAt(text, file string) int {
	token := "@" + file
	for from := 0; ; {
		i := strings.Index(text[from:], token)
		if i < 0 {
			return -1
		}
		i += from
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after := text[i+len(token):]
		if (i == 0 || unicode.IsSpace(before)) && endsWord(after) {
			return i
		}
		from = i + 1
	}
}

// endsWord reports whether what follows a path ends it.
func endsWord(after string) bool {
	if after == "" {
		return true
	}
	r, n := utf8.DecodeRuneInString(after)
	switch {
	case unicode.IsSpace(r), strings.ContainsRune(",;:!?)]}", r):
		return true
	case r == '.': // the end of a sentence, not the start of an extension
		next, _ := utf8.DecodeRuneInString(after[n:])
		return n == len(after) || unicode.IsSpace(next)
	}
	return false
}
