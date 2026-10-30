package design

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/kernel"
)

// design remembers a conversation in design mode, under the backend origin
// it was made in. Dir is the design's own folder, whose artboards its canvas
// shows. System is the design system the brief it last had named, so a new
// choice sends the brief again.
type design struct {
	Origin  string
	Session agent.SessionID
	Dir     string
	System  string
}

// designsDir is where new designs go in the conversation's folder, each in
// a folder of its own.
const designsDir = "designs"

func (p *Plugin) designIndex(s agent.SessionID) int {
	origin := p.agent.Status().Origin
	return slices.IndexFunc(p.designs, func(d design) bool { return d.Session == s && d.Origin == origin })
}

// designDir is the folder of the design the conversation is: a new chat
// made with New Design, or a conversation one became; empty for others.
func (p *Plugin) designDir(conv chat.Conversation) string {
	if conv.Session == "" {
		if p.draft {
			return p.draftDir
		}
		return ""
	}
	if i := p.designIndex(conv.Session); i >= 0 {
		return p.designs[i].Dir
	}
	return ""
}

func (p *Plugin) remember(d design) {
	if d.Session == "" || d.Dir == "" || slices.ContainsFunc(p.designs, func(existing design) bool {
		return existing.Session == d.Session && existing.Origin == d.Origin
	}) {
		return
	}
	p.designs = append(p.designs, d)
	p.saveDesigns()
}

func (p *Plugin) saveDesigns() { p.ctx.Settings().Set(designsKey, p.designs) }

// canDesign reports that a new design can start: the agent is ready and no
// other is being made.
func (p *Plugin) canDesign() bool {
	return p.agent.Status().State == agent.Ready && !p.making.Running()
}

// newDesign opens a new chat in design mode, in the open conversation's
// folder, or the folder whose tabs are on show, or a folder the user picks.
func (p *Plugin) newDesign() {
	folder := p.chat.Current().Directory
	if folder == "" {
		if t, ok := p.tabs(); ok {
			folder = t.Space()
		}
	}
	if folder == "" {
		p.ws.ChooseFolder(func(path string) {
			if path != "" {
				p.newDesignIn(path)
			}
		})
		return
	}
	p.newDesignIn(folder)
}

// newDesignIn makes the design's own folder, designs/design-N, the first
// N free, and opens a new chat beside its empty canvas.
func (p *Plugin) newDesignIn(folder string) {
	kernel.Go(p.ctx, &p.making, func(ctx context.Context) (string, error) {
		base := filepath.Join(folder, designsDir)
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
		for n := 1; n < 10000; n++ {
			dir := filepath.Join(base, "design-"+strconv.Itoa(n))
			err := os.Mkdir(dir, 0o755)
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return dir, err
		}
		return "", errors.New("the folder has too many designs")
	}, func(dir string, err error) {
		if err != nil {
			p.host.Toast(ui.Toast{Title: "The design did not start", Description: err.Error(), Type: components.ToastError})
			return
		}
		p.opening = dir
		p.chat.NewChat(folder)
		p.opening = ""
	})
}

func (p *Plugin) tabs() (tabs.Service, bool) { return kernel.Use(p.ctx, tabs.ServiceKey) }

// isDesignTab reports that the tab shows a design: a conversation that is
// one, or the new design on show.
func (p *Plugin) isDesignTab(t tabs.Tab) bool {
	switch t.Kind {
	case tabs.Conversation:
		return p.designIndex(t.Session) >= 0
	case tabs.Draft:
		if !p.draft {
			return false
		}
		s, ok := p.tabs()
		if !ok {
			return false
		}
		active, ok := s.Active()
		return ok && active.ID == t.ID
	case tabs.Page:
	}
	return false
}

// leaving keeps the unsent comments of a design conversation the chat
// leaves, and ends the design draft.
func (p *Plugin) leaving(left chat.Unsent) {
	conv := left.Conversation
	if conv.Session == "" && p.draft {
		p.draft, p.draftDir = false, ""
		if !conv.Starting {
			delete(p.starts, conv.DraftID)
		}
	}
	p.marks = nil
	p.closeCard()
}

// changed follows the conversation on show: a new design's conversation
// becomes a design once it has a session, and the canvas shows beside
// designs only.
func (p *Plugin) changed(conv chat.Conversation) {
	if p.opening != "" {
		p.draft, p.draftDir = true, p.opening
	}
	p.complete(conv)
	if conv.Session != "" {
		p.draft, p.draftDir = false, ""
	}
	p.update(conv)
}

// started records the design a new design's first message created after
// the user left it.
func (p *Plugin) started(conv chat.Conversation) {
	p.complete(conv)
	p.update(p.chat.Current())
}

// complete matches a creation to the design whose first message started it.
func (p *Plugin) complete(conv chat.Conversation) {
	if d, ok := p.starts[conv.DraftID]; ok && conv.Session != "" {
		delete(p.starts, conv.DraftID)
		d.Session = conv.Session
		p.remember(d)
	}
}

// update shows the canvas beside a design and puts it away elsewhere.
func (p *Plugin) update(conv chat.Conversation) {
	dir := p.designDir(conv)
	if conv.Directory == "" || dir == "" {
		if p.open {
			p.stopPresenting()
			p.closeCard()
			p.open = false
		}
		p.follow("", "")
		return
	}
	wasOpen := p.open
	p.open = true
	p.follow(conv.Directory, dir)
	if !wasOpen {
		p.scan()
	}
}

// forgetGone forgets the designs whose conversations the workspace record
// no longer has.
func (p *Plugin) forgetGone() {
	origin := p.agent.Status().Origin
	if origin == "" || !p.ws.Loaded() {
		return
	}
	kept := slices.DeleteFunc(slices.Clone(p.designs), func(d design) bool {
		if d.Origin != origin {
			return false
		}
		_, ok := p.ws.Lookup(d.Session)
		return !ok
	})
	if len(kept) != len(p.designs) {
		p.designs = kept
		p.saveDesigns()
	}
	p.feedback = slices.DeleteFunc(p.feedback, func(f savedFeedback) bool {
		if f.Origin != origin || f.Folder == p.draftDir {
			return false
		}
		for _, d := range p.designs {
			if d.Origin == origin && d.Dir == f.Folder {
				return false
			}
		}
		for _, d := range p.starts {
			if d.Origin == origin && d.Dir == f.Folder {
				return false
			}
		}
		return true
	})
	p.ctx.Settings().Set(feedbackKey, p.feedback)
}
