// Package skills lets a message use an agent skill. Typing "/" in the composer
// lists the skills the backend has for the conversation's folder; choosing one
// writes its name into the message, and sending attaches every listed skill
// whose "/name" the text still names. A skill removed from the text is not
// attached. The backend discovers and runs the skills, so this works the same
// on a server that runs somewhere else.
package skills

import (
	"context"
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

// Plugin is the composer's skill list.
type Plugin struct {
	ctx    *kernel.Context
	skills agent.Skills
	chat   chat.Controller
	host   shell.Host

	found     []agent.Skill // the skills the latest lookup returned, sorted by name
	typed     string        // what follows "/" under the caret
	active    bool          // a lookup already ran for the current word
	failed    bool          // a lookup failed since the word was started, and said so
	lookup    kernel.Task
	session   agent.SessionID // the conversation the skills belong to
	directory string          // the conversation's folder the skills were listed for
	// delay waits before a lookup goes out, so typing sends one.
	delay time.Duration
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "skills", Title: "Skills",
		Description: "Type / to attach an agent skill to a message.",
		Requires:    []kernel.Named{agent.SkillsKey, chat.ControllerKey, shell.HostKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.skills = kernel.Require(c, agent.SkillsKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.host = kernel.Require(c, shell.HostKey)
	if p.delay == 0 {
		p.delay = 60 * time.Millisecond
	}
	conv := p.chat.Current()
	p.session, p.directory = conv.Session, conv.Directory
	kernel.On(c, chat.Changed, func(conv chat.Conversation) {
		if conv.Session == p.session && conv.Directory == p.directory {
			return
		}
		p.session, p.directory = conv.Session, conv.Directory
		p.reset()
	})
	// A skill named by the text goes with the message. It follows the
	// attachments (1000) and mentions (1001), which attach files.
	kernel.Transform(c, chat.Outgoing, 1002, p.outgoing)
	return kernel.Contribute(c, chat.Completions, "skills.list", 10, chat.Completion{
		Trigger: '/', Hint: "uses a skill", Query: p.search, Candidates: p.candidates, Accept: p.accept,
	})
}

// reset drops the list when its conversation or folder changes.
func (p *Plugin) reset() {
	p.lookup.Cancel()
	p.found, p.typed, p.active, p.failed = nil, "", false, false
}

// ---- Finding

// search lists the skills for the conversation's folder once per word. Once
// the word is over the next one lists them again.
func (p *Plugin) search(typed string, active bool) {
	p.typed = typed
	if !active {
		p.active = false
		return
	}
	if p.active {
		return
	}
	p.active, p.failed = true, false
	p.read()
}

// read gets the skills off the UI thread.
func (p *Plugin) read() {
	skills, directory, delay := p.skills, p.directory, p.delay
	kernel.Go(p.ctx, &p.lookup, func(ctx context.Context) ([]agent.Skill, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if err := ctx.Err(); err != nil { // replaced while waiting
			return nil, err
		}
		return skills.Skills(ctx, directory)
	}, func(list []agent.Skill, err error) {
		if err != nil {
			p.found = nil
			if !p.failed { // one notice per word, not one per keystroke
				p.failed = true
				p.host.Toast(ui.Toast{Title: "Skills could not be listed", Description: err.Error(), Type: components.ToastError})
			}
			return
		}
		p.found, p.failed = slices.Clone(list), false
		slices.SortFunc(p.found, func(a, b agent.Skill) int {
			if name := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); name != 0 {
				return name
			}
			return strings.Compare(a.ID, b.ID)
		})
	})
}

// candidates lists the skills whose names contain what is typed, by name.
func (p *Plugin) candidates() []chat.Candidate {
	typed := strings.ToLower(p.typed)
	out := make([]chat.Candidate, 0, len(p.found))
	for _, s := range p.found {
		if !strings.Contains(strings.ToLower(s.Name), typed) {
			continue
		}
		out = append(out, chat.Candidate{ID: s.ID, Label: s.Name, Detail: s.Description, Icon: lucide.Sparkles})
	}
	return out
}

// ---- Choosing

// accept writes the skill's name into the message. What is typed after the
// slash is replaced by the skill's canonical name.
func (p *Plugin) accept(c chat.Candidate) string {
	for _, s := range p.found {
		if s.ID == c.ID {
			return "/" + s.Name + " "
		}
	}
	return ""
}

// outgoing attaches every listed skill the text still names as a word of its
// own, in the order the text names them. The text itself is unchanged.
func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	type named struct {
		at  int
		ref agent.SkillRef
	}
	var used []named
	for _, s := range p.found {
		at, ok := skillAt(d.Text, s.Name)
		if !ok || slices.ContainsFunc(used, func(u named) bool { return u.ref.ID == s.ID }) {
			continue
		}
		used = append(used, named{at, agent.SkillRef{ID: s.ID}})
	}
	slices.SortFunc(used, func(a, b named) int { return a.at - b.at })
	for _, u := range used {
		d.Skills = append(d.Skills, u.ref)
	}
	return d, nil, nil
}

// skillAt returns the byte offset of "/name" in text as a word of its own,
// and whether it found one. The name starts a word and ends one: whitespace,
// the end, or the punctuation that closes a sentence follows it.
func skillAt(text, name string) (int, bool) {
	token := "/" + name
	for from := 0; ; {
		i := strings.Index(text[from:], token)
		if i < 0 {
			return 0, false
		}
		i += from
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after := text[i+len(token):]
		if (i == 0 || unicode.IsSpace(before)) && endsWord(after) {
			return i, true
		}
		from = i + 1
	}
}

// endsWord reports whether what follows a skill name ends it.
func endsWord(after string) bool {
	if after == "" {
		return true
	}
	r, n := utf8.DecodeRuneInString(after)
	switch {
	case unicode.IsSpace(r), strings.ContainsRune(",;:!?)]}", r):
		return true
	case r == '.': // the end of a sentence, not the start of a longer name
		next, _ := utf8.DecodeRuneInString(after[n:])
		return n == len(after) || unicode.IsSpace(next)
	}
	return false
}
