// Package replay feeds the window synthetic agent events at a steady rate, so
// that its CPU, frames and memory can be measured on the real window with every
// feature installed. Only builds tagged wadareplay install it (replay.go in the
// main package); scripts/measure.sh drives it.
//
// WADA_REPLAY picks what it replays:
//
//   - text: an answer streaming into the open conversation, 60 updates a second
//     of about 52 bytes each, a fresh text block every 64 updates;
//   - working: a turn whose tool runs for the whole time, with no text;
//   - background: text streaming into another open tab's conversation while
//     the open one stays still.
//
// It waits WADA_REPLAY_DELAY (default 6s) for the window to settle, replays for
// WADA_REPLAY_FOR (default 30s), ends the turn, waits WADA_REPLAY_SETTLE
// (default 10s) and quits the app. It prints "replay: start", "replay: end" and
// "replay: quit" lines to standard error as it goes. WADA_REPLAY_HZ (default 60,
// at most 60) is how many batches a second carry the text, as a backend batching
// its events that often does; the text comes at the same pace whatever it is.
// WADA_REPLAY_PROFILE, a file path, records a CPU profile of the replay phase.
// WADA_REPLAY_PARAGRAPH, a number of updates, ends a paragraph after that many,
// as answers do; without it a text block is one paragraph, the costliest case.
package replay

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/tabs"

	"github.com/egoist/mygo"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// words make up the replayed text. Updates draw from them at random, with a
// fixed seed, so that lines never repeat as a repeated sentence would, which
// would let text layouts be reused as real answers never do.
var words = strings.Fields(`the a an of to in on for with that this it is was are be as at by from or
and not but which when where how what why who all any each other more most some such only own same
than too very can will just should now kernel plugin scope frame layout window render event batch
backend session transcript message answer stream token model provider reveal paragraph markdown list
row column text width height cache memory budget profile measure replay quiet invalidate post`)

// delta is the next update of about 52 bytes: whole words and a space after
// each.
func (p *Plugin) delta() string {
	var b strings.Builder
	for b.Len() < 48 {
		p.seed = p.seed*6364136223846793005 + 1442695040888963407
		b.WriteString(words[(p.seed>>33)%uint64(len(words))])
		b.WriteByte(' ')
	}
	return b.String()
}

// Plugin is the replay plugin.
type Plugin struct {
	ctx     *kernel.Context
	chat    chat.Controller
	tabs    tabs.Service
	host    shell.Host
	mode    string
	session agent.SessionID
	updates int
	per     int    // updates a batch carries
	seed    uint64 // of the words of the text
	// paragraph is how many updates a paragraph takes; 0 for one a block.
	paragraph int
	profile   *os.File // the CPU profile being recorded
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "replay", Title: "Replay", Description: "Synthetic agent events for measurements.",
		Requires: []kernel.Named{chat.ControllerKey, tabs.ServiceKey, shell.HostKey}}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.tabs = kernel.Require(c, tabs.ServiceKey)
	p.host = kernel.Require(c, shell.HostKey)
	p.mode = os.Getenv("WADA_REPLAY")
	switch p.mode {
	case "text", "working", "background":
	default:
		return fmt.Errorf("replay: WADA_REPLAY is %q, not text, working or background", p.mode)
	}
	delay, length, settle := duration("WADA_REPLAY_DELAY", 6*time.Second), duration("WADA_REPLAY_FOR", 30*time.Second),
		duration("WADA_REPLAY_SETTLE", 10*time.Second)
	ticking := p.mode != "working" // a tool that runs sends nothing until it ends
	hz := 60
	if n, err := strconv.Atoi(os.Getenv("WADA_REPLAY_HZ")); err == nil && n > 0 && n <= 60 {
		hz = n
	}
	p.per = 60 / hz
	p.paragraph, _ = strconv.Atoi(os.Getenv("WADA_REPLAY_PARAGRAPH"))
	c.Spawn(func(ctx context.Context) {
		if !sleep(ctx, delay) {
			return
		}
		c.PostQuiet(p.start)
		tick := time.NewTicker(time.Second / time.Duration(hz))
		defer tick.Stop()
		end := time.After(length)
	replay:
		for {
			select {
			case <-ctx.Done():
				return
			case <-end:
				break replay
			case <-tick.C:
				if ticking {
					c.PostQuiet(p.tick)
				}
			}
		}
		c.PostQuiet(p.end)
		if sleep(ctx, settle) {
			c.Post(func() { fmt.Fprintln(os.Stderr, "replay: quit"); mygo.App.Quit() })
		}
	})
	return nil
}

// start picks the conversation and begins its turn.
func (p *Plugin) start() {
	p.session = p.chat.Current().Session
	if p.mode == "background" {
		p.session = ""
		for _, t := range p.tabs.Tabs() {
			if t.Kind == tabs.Conversation && t.Session != "" && t.Session != p.chat.Current().Session {
				p.session = t.Session
				break
			}
		}
	}
	if path := os.Getenv("WADA_REPLAY_PROFILE"); path != "" {
		if f, err := os.Create(path); err == nil && pprof.StartCPUProfile(f) == nil {
			p.profile = f
		}
	}
	fmt.Fprintf(os.Stderr, "replay: start %s %s at %d, window in front: %v\n", p.mode, p.session, time.Now().UnixMilli(),
		p.host.Foreground())
	batch := []agent.Event{{Kind: agent.TurnStarted, Session: p.session}}
	if p.mode == "working" {
		tool := &agent.Tool{ID: "replay-tool", Name: "bash", Kind: agent.ToolShell, Command: "sleep 30"}
		batch = append(batch, agent.Event{Kind: agent.ToolStarted, Session: p.session, Tool: tool},
			agent.Event{Kind: agent.ToolCalled, Session: p.session, Tool: tool})
	} else {
		batch = append(batch, p.text(agent.TextStarted, ""))
	}
	p.emit(batch)
}

// tick is one batch of streamed text: per updates of it.
func (p *Plugin) tick() {
	var batch []agent.Event
	for range p.per {
		p.updates++
		if p.updates%64 == 0 {
			batch = append(batch, p.text(agent.TextEnded, ""), p.textAt(agent.TextStarted, p.updates/64, ""))
		} else {
			d := p.delta()
			if p.paragraph > 0 && p.updates%p.paragraph == 0 {
				d += "\n\n"
			}
			batch = append(batch, p.text(agent.TextDelta, d))
		}
	}
	p.emit(batch)
}

func (p *Plugin) end() {
	batch := []agent.Event{p.text(agent.TextEnded, "")}
	if p.mode == "working" {
		batch = []agent.Event{{Kind: agent.ToolFinished, Session: p.session,
			Tool: &agent.Tool{ID: "replay-tool", Name: "bash", Kind: agent.ToolShell, Command: "sleep 30"}}}
	}
	p.emit(append(batch, agent.Event{Kind: agent.TurnSucceeded, Session: p.session}))
	if p.profile != nil {
		pprof.StopCPUProfile()
		p.profile.Close()
	}
	fmt.Fprintf(os.Stderr, "replay: end at %d after %d updates\n", time.Now().UnixMilli(), p.updates)
}

func (p *Plugin) text(kind agent.EventKind, d string) agent.Event {
	return p.textAt(kind, p.updates/64, d)
}

func (p *Plugin) textAt(kind agent.EventKind, ordinal int, d string) agent.Event {
	return agent.Event{Kind: kind, Session: p.session, Bytes: len(d) + 128,
		Text: &agent.TextEvent{Message: "replay", Ordinal: ordinal, Delta: d}}
}

// emit delivers a batch as the backend's event pump does, in a quiet post.
func (p *Plugin) emit(batch []agent.Event) { kernel.Emit(p.ctx, agent.Events, batch) }

func duration(name string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(name)); err == nil && d > 0 {
		return d
	}
	return fallback
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
