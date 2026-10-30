// Package filechanges shows session changes or local uncommitted Git changes
// with line counts and expandable diffs below Context in the work panel.
package filechanges

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"unicode"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/xleotranx/wada-kit/kernel"
)

type changeSource uint8

const (
	sessionChanges changeSource = iota
	uncommittedChanges
)

func (source changeSource) label() string {
	if source == uncommittedChanges {
		return "Uncommitted Git changes"
	}
	return "Session changes"
}

// Plugin is the work panel's file changes view.
type Plugin struct {
	ctx       *kernel.Context
	sourced   bool // a provider of agent.ChangesKey was there at the last read
	session   agent.SessionID
	directory string
	mode      changeSource
	changes   []agent.FileChange
	load      kernel.Task
	known     bool
	problem   string
	open      map[string]bool // files whose diff is shown, by path
	expanded  bool            // the file list; collapsed by default, independent of diffs
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "filechanges", Title: "File Changes",
		Description: "Session changes or local uncommitted Git changes, with line counts and diffs.",
		Requires:    []kernel.Named{agent.ServiceKey, chat.ControllerKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.open = map[string]bool{}
	kernel.On(c, chat.Changed, p.follow)
	kernel.On(c, agent.Events, p.events)
	// Session changes come from an optional provider; read again when one
	// comes or goes.
	kernel.On(c, kernel.PluginsChanged, func([]kernel.Status) {
		if _, ok := kernel.Use(c, agent.ChangesKey); ok != p.sourced && p.session != "" && p.mode == sessionChanges {
			p.readChanges()
		}
	})
	// A read before the backend connected failed; read again once it has.
	attempt := kernel.Require(c, agent.ServiceKey).Status().Attempt
	kernel.On(c, agent.StatusChanged, func(st agent.Status) {
		if st.State == agent.Ready && st.Attempt != attempt && p.session != "" {
			p.readChanges()
		}
		if st.State == agent.Ready {
			attempt = st.Attempt
		}
	})
	p.follow(kernel.Require(c, chat.ControllerKey).Current())
	return kernel.Contribute(c, chat.Work, "filechanges.files", 25, chat.WorkSection{Title: "File changes",
		Shown: func() bool { return p.session != "" }, Render: p.changesView})
}

func (p *Plugin) follow(conv chat.Conversation) {
	if conv.Session == p.session && conv.Directory == p.directory {
		return
	}
	p.session, p.directory = conv.Session, conv.Directory
	p.reset()
	if p.session != "" {
		p.readChanges()
	}
}

// reset drops results and expansion when their source or folder changes.
func (p *Plugin) reset() {
	p.load.Cancel()
	p.changes, p.known, p.problem = nil, false, ""
	clear(p.open)
}

func (p *Plugin) chooseSource() {
	p.reset()
	p.readChanges()
}

func (p *Plugin) events(batch []agent.Event) {
	for _, ev := range batch {
		if ev.Session == p.session && p.session != "" &&
			(ev.Kind == agent.TurnSucceeded || ev.Kind == agent.TurnFailed || ev.Kind == agent.TurnInterrupted) {
			p.readChanges()
			return
		}
	}
}

// A failed refresh keeps the last known changes visible.
func (p *Plugin) readChanges() {
	source, ok := kernel.Use(p.ctx, agent.ChangesKey)
	p.sourced = ok
	id, mode, directory := p.session, p.mode, p.directory
	kernel.Go(p.ctx, &p.load, func(ctx context.Context) ([]agent.FileChange, error) {
		if mode == uncommittedChanges {
			return gitChanges(ctx, directory)
		}
		if !ok {
			return nil, errors.New("session changes aren’t available with this backend")
		}
		changes, err := source.Changes(ctx, id)
		if err != nil {
			return nil, errors.New("couldn’t load file changes")
		}
		return changes, nil
	}, func(changes []agent.FileChange, err error) {
		if id != p.session {
			return
		}
		p.problem = ""
		if err != nil {
			message := []rune(err.Error())
			if len(message) > 0 {
				message[0] = unicode.ToUpper(message[0])
				p.problem = string(message)
			}
		} else {
			p.changes, p.known = slices.Clone(changes), true
			slices.SortFunc(p.changes, func(a, b agent.FileChange) int {
				if dir := strings.Compare(path.Dir(a.Path), path.Dir(b.Path)); dir != 0 {
					return dir
				}
				return strings.Compare(a.Path, b.Path)
			})
		}
	})
}
