package main

import (
	"context"
	"fmt"
	"path"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	chatcontract "wada-desktop-go/internal/contract/chat"
	tabcontract "wada-desktop-go/internal/contract/tabs"
	wscontract "wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugins/attachments"
	"wada-desktop-go/internal/plugins/chat"
	"wada-desktop-go/internal/plugins/explore"
	"wada-desktop-go/internal/plugins/filechanges"
	"wada-desktop-go/internal/plugins/flows"
	"wada-desktop-go/internal/plugins/markup"
	"wada-desktop-go/internal/plugins/mentions"
	"wada-desktop-go/internal/plugins/models"
	"wada-desktop-go/internal/plugins/notifications"
	"wada-desktop-go/internal/plugins/palette"
	"wada-desktop-go/internal/plugins/skills"
	"wada-desktop-go/internal/plugins/tabs"
	"wada-desktop-go/internal/plugins/usage"
	"wada-desktop-go/internal/plugins/workflow"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/plugins/inspector"
	"github.com/xleotranx/wada-kit/plugins/settings"
	"github.com/xleotranx/wada-kit/shell"
)

// The benchmarks draw the whole app — a strip of thirteen tabs (twelve in
// three folders, and the page's own), the page, composer and shell
// chrome — on ui.Tester with the real plugins and a fake backend. Run with:
//
//	go test -run NONE -bench . -benchtime 5x ./...
//
// The stream benchmark advances simulated time by one refresh per update,
// including the software frames due for text reveal and other animations.

const benchSession = agent.SessionID("bench")

// benchMarkdown is one realistic assistant answer: prose, a code block, a
// list and a link.
var benchMarkdown = []string{
	"The build is wired through the adapter. Here is the flow:\n\n" +
		"1. The client posts the request.\n2. The wire decodes events.\n3. Rows fold into the transcript.\n\n" +
		"- **Busy** keeps the shimmer\n- **Stream** shows words as they land\n\n```go\nfunc (p *Plugin) view(c *ui.Context) {\n\tfor _, row := range p.t.rows {\n\t\tdrawRow(c, row)\n\t}\n}\n```\n\n" +
		"The [pinned module](https://example.com/mygo) owns layout; we own the rows.",
	"A short answer with `inline code` and a trailing thought that wraps over two lines of the transcript column.",
}

func benchMessages(n int) []agent.Message {
	out := make([]agent.Message, 0, n)
	for i := range n {
		id := agent.MessageID(fmt.Sprintf("m%d", i))
		switch i % 4 {
		case 0: // the user's words
			out = append(out, agent.Message{ID: id, Role: agent.RoleUser,
				Text: fmt.Sprintf("Please check row %d and summarize what the code does with the registry.", i)})
		case 2: // an answer with a tool call
			tool := agent.Tool{ID: fmt.Sprintf("t%d", i), Name: "shell", Command: fmt.Sprintf("go test -run TestRow%d ./...", i),
				Output: []string{"ok  \t./internal/kernel\t0.412s", "ok  \t./internal/plugins/chat\t1.203s", "PASS"}, Status: "completed"}
			out = append(out, agent.Message{ID: id, Role: agent.RoleAssistant, Parts: []agent.Part{
				{Kind: agent.PartTool, Tool: &tool},
				{Kind: agent.PartText, Text: benchMarkdown[(i/2)%len(benchMarkdown)]},
			}})
		default: // a plain markdown answer
			out = append(out, agent.Message{ID: id, Role: agent.RoleAssistant,
				Parts: []agent.Part{{Kind: agent.PartText, Text: benchMarkdown[i%len(benchMarkdown)]}}})
		}
	}
	return out
}

// benchApp draws the app at the real window's default size, with the fake
// backend holding the sessions and conversation above. run flushes posts from
// background workers back onto the calling goroutine (the UI thread).
func benchApp(b *testing.B, messages []agent.Message) (*shell.Shell, *ui.Tester, func()) {
	b.Helper()
	f := plugintest.NewAgent()
	f.State = agent.Status{State: agent.Ready, Attempt: 1}
	f.Stored[benchSession] = messages
	for i := range 12 { // the open tabs' conversations
		f.Stored[agent.SessionID(fmt.Sprintf("t%d", i))] = messages
	}
	page := agent.SessionPage{Sessions: make([]agent.Session, 24)}
	for i := range page.Sessions {
		page.Sessions[i] = agent.Session{ID: agent.SessionID(fmt.Sprintf("s%d", i)),
			Title: fmt.Sprintf("Conversation %d about the registry", i), Updated: 1760000000000 + int64(i)}
	}
	f.Pages["|"] = page
	f.Catalog = []agent.Model{{Ref: agent.ModelRef{Provider: "p", ID: "bench-1"}, Name: "Bench 1"},
		{Ref: agent.ModelRef{Provider: "p", ID: "bench-2"}, Name: "Bench 2"}}

	// The record holds twelve conversations in three folders, all open as
	// tabs, and the benchmark's own.
	ws := &plugintest.Workspace{}
	for i, folder := range []string{"/work/app", "/work/web", "/work/docs"} {
		ws.FolderSet = append(ws.FolderSet, wscontract.Folder{Path: folder})
		for j := range 4 {
			ws.Convs = append(ws.Convs, wscontract.Conversation{ID: agent.SessionID(fmt.Sprintf("t%d", i*4+j)),
				Title: fmt.Sprintf("Tab %d of %s", j+1, path.Base(folder)), Folder: folder})
		}
	}
	ws.Convs = append(ws.Convs, wscontract.Conversation{ID: benchSession, Title: "Benchmark", Folder: "/work/app"})

	// Workers post from their own goroutines; run drains on this one.
	var mu sync.Mutex
	var queue []func()
	run := func() {
		for {
			mu.Lock()
			if len(queue) == 0 {
				mu.Unlock()
				return
			}
			fn := queue[0]
			queue = queue[1:]
			mu.Unlock()
			fn()
		}
	}
	post := func(fn func()) {
		mu.Lock()
		queue = append(queue, fn)
		mu.Unlock()
	}
	s, err := shell.New(shell.Options{Post: post, Strict: true,
		Store: kerneltest.NewStore()}, f.New, ws.New, plugintest.NewFiles(nil).New, plugintest.NewSkills().New,
		chat.New, models.New, usage.New, filechanges.New, attachments.New, markup.New, mentions.New, skills.New,
		tabs.New, explore.New, workflow.New, flows.New, palette.New, notifications.New, inspector.New, settings.New)
	if err != nil {
		b.Fatal(err)
	}
	if err := s.Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			b.Error(err)
		}
	})

	// Background workers run on real goroutines: run posts until quiet.
	flush := func(rounds int) {
		for i := 0; i < rounds; i++ {
			run()
			time.Sleep(time.Millisecond)
		}
	}
	flush(50) // startup: the session list loads
	for _, plugin := range s.Kernel.Plugins() {
		if plugin.State != kernel.Active {
			b.Fatalf("benchmark plugin %s is %s: %v", plugin.Manifest.ID, plugin.State, plugin.Err)
		}
	}

	tt := ui.NewTester(s.View, 1400, 900)
	tt.Frame()

	controller, ok := kernel.Use[chatcontract.Controller](s.Kernel, chatcontract.ControllerKey)
	if !ok {
		b.Fatal("the chat controller did not install")
	}
	for _, conv := range ws.Convs[:12] {
		controller.Open(conv.Session())
		flush(2)
	}
	if len(messages) > 0 {
		controller.Open(agent.Session{ID: benchSession, Title: "Benchmark", Directory: "/work/app"})
	} else {
		controller.NewChat("/work/app")
	}
	flush(50)
	run()
	tt.Frame()
	if open, ok := kernel.Use[tabcontract.Service](s.Kernel, tabcontract.ServiceKey); !ok || len(open.Tabs()) != 13 {
		b.Fatal("the twelve tabs and the benchmark's did not open")
	}
	return s, tt, run
}

// BenchmarkEmptyPageIdle draws a frame of the new-chat screen beside twelve
// tabs, with no transcript: the floor every invalidation pays.
func BenchmarkEmptyPageIdle(b *testing.B) {
	_, tt, _ := benchApp(b, nil)
	b.ReportAllocs()
	for b.Loop() {
		tt.Frame()
	}
}

// BenchmarkTranscriptIdle draws one settled frame of a long conversation:
// the cost of every invalidation while nothing streams (hovering, typing,
// caret blinks, window events).
func BenchmarkTranscriptIdle(b *testing.B) {
	_, tt, _ := benchApp(b, benchMessages(120))
	b.ReportAllocs()
	for b.Loop() {
		tt.Frame()
	}
}

// BenchmarkStreamDelta measures a text update and one refresh of simulated
// time, including reveal animations. It starts a fresh block every 64 deltas.
func BenchmarkStreamDelta(b *testing.B) {
	s, tt, _ := benchApp(b, benchMessages(60))
	const session = benchSession
	emit := kernel.Emit[[]agent.Event]
	emit(s.Kernel, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: session},
		{Kind: agent.TextStarted, Session: session, Text: &agent.TextEvent{Message: "sm"}}})
	tt.Frame()
	emit(s.Kernel, agent.Events, []agent.Event{{Kind: agent.TextDelta, Session: session,
		Text: &agent.TextEvent{Message: "sm", Delta: "stream benchmark is visible "}}})
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if !tt.HasText("stream benchmark is visible") {
		b.Fatal("the benchmark is not rendering streamed text")
	}
	b.ReportAllocs()
	// Words drawn at random with a fixed seed: a repeated sentence would make
	// lines repeat, whose layouts real answers never get to reuse.
	words := strings.Fields("the a of to in for with that this it is was be as at by from and not but which " +
		"when kernel plugin scope frame layout window render event batch backend session transcript " +
		"message answer stream token model reveal paragraph markdown list row text width cache memory")
	seed := uint64(7)
	next := func() string {
		var d strings.Builder
		for d.Len() < 48 {
			seed = seed*6364136223846793005 + 1442695040888963407
			d.WriteString(words[(seed>>33)%uint64(len(words))])
			d.WriteByte(' ')
		}
		return d.String()
	}
	for i := 0; b.Loop(); i++ {
		if i%64 == 63 { // end one block, start the next
			emit(s.Kernel, agent.Events, []agent.Event{{Kind: agent.TextEnded, Session: session,
				Text: &agent.TextEvent{Message: "sm", Ordinal: i / 64, Text: "complete"}}, {Kind: agent.TextStarted, Session: session,
				Text: &agent.TextEvent{Message: "sm", Ordinal: i/64 + 1}}})
		} else if i != 0 {
			emit(s.Kernel, agent.Events, []agent.Event{{Kind: agent.TextDelta, Session: session,
				Text: &agent.TextEvent{Message: "sm", Ordinal: i / 64, Delta: next()}}})
		}
		tt.Advance(time.Second / 60)
		tt.Frame()
	}
}

// BenchmarkTranscriptScroll scrolls the conversation a wheel notch per frame,
// alternating directions, so rows leave and enter the viewport every frame.
func BenchmarkTranscriptScroll(b *testing.B) {
	_, tt, _ := benchApp(b, benchMessages(120))
	dy := float32(-120)
	b.ReportAllocs()
	for b.Loop() {
		tt.Scroll(700, 450, 0, dy)
		dy = -dy
		tt.Frame()
	}
}

// BenchmarkSwitchTab shows another open conversation of 120 messages, as a
// click on its tab does: the transcript is read again from the backend and
// drawn, with the loading frame in between.
func BenchmarkSwitchTab(b *testing.B) {
	s, tt, run := benchApp(b, benchMessages(120))
	controller, _ := kernel.Use[chatcontract.Controller](s.Kernel, chatcontract.ControllerKey)
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		controller.Open(agent.Session{ID: agent.SessionID(fmt.Sprintf("t%d", i%2)), Directory: "/work/app"})
		for {
			run()
			tt.Frame()
			if !tt.HasText("Loading the conversation…") {
				break
			}
			runtime.Gosched()
		}
	}
}
