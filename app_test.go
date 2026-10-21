package main

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	chatcontract "wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugins/attachments"
	"wada-desktop-go/internal/plugins/chat"
	"wada-desktop-go/internal/plugins/inspector"
	"wada-desktop-go/internal/plugins/mentions"
	"wada-desktop-go/internal/plugins/models"
	"wada-desktop-go/internal/plugins/palette"
	"wada-desktop-go/internal/plugins/sessions"
	"wada-desktop-go/internal/plugins/settings"
	"wada-desktop-go/internal/plugintest"
	"wada-desktop-go/internal/shell"

	"github.com/egoist/mygo/ui"
)

// The app tests run the window the way main composes it, on ui.Tester with
// fake backends, to check what the plugins do together: the palette over the
// commands the others contribute, and a mention from the list to the send.

type app struct {
	t     *testing.T
	shell *shell.Shell
	tt    *ui.Tester
	agent *plugintest.Agent
	files *plugintest.Files
	run   func()
}

func startApp(t *testing.T) *app {
	t.Helper()
	a := plugintest.NewAgent()
	a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{
		{ID: "s1", Title: "Fix the parser", Directory: "/work/app", Updated: 30},
		{ID: "s2", Title: "Write the docs", Directory: "/work/docs", Updated: 20}}}
	files := plugintest.NewFiles(map[string]string{
		"src/main.go": "package main\n",
		"src/mail.go": "package main // mail\n",
		"docs/map.md": "# map\n",
	})
	// Workers post from their own goroutines; the test goroutine is the UI thread.
	var mu sync.Mutex
	var queue []func()
	post := func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		queue = append(queue, fn)
	}
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
	s, err := shell.New(shell.Options{Post: post, Strict: true, Store: kerneltest.NewStore()},
		a, files, &chat.Plugin{}, &models.Plugin{}, &attachments.Plugin{}, &mentions.Plugin{},
		&sessions.Plugin{}, &palette.Plugin{}, &inspector.Plugin{}, &settings.Plugin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	tt := ui.NewTester(s.View, 1200, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	ap := &app{t: t, shell: s, tt: tt, agent: a, files: files, run: run}
	ap.settle()
	return ap
}

// settle lets background work finish and lands what it posted, then draws.
func (a *app) settle() {
	a.t.Helper()
	for range 40 {
		a.run()
		time.Sleep(time.Millisecond)
	}
	a.tt.Frame()
}

// until draws frames, landing what workers post, until ok holds. Lookups wait
// a moment before they go out, so the tests wait for what they expect.
func (a *app) until(what string, ok func() bool) {
	a.t.Helper()
	for range 400 {
		a.run()
		a.tt.Frame()
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.t.Fatalf("%s never happened: %q", what, a.tt.Texts())
}

// command runs a registered command the way the menu bar would.
func (a *app) command(id string) {
	a.t.Helper()
	for _, e := range kernel.Items(a.shell.Kernel, kernel.Commands) {
		if e.ID == id {
			kernel.Invoke(a.shell.Kernel, e, func(c kernel.Command) { c.Run() })
			a.settle()
			return
		}
	}
	a.t.Fatalf("no command %q", id)
}

func (a *app) openConversation(s agent.Session) {
	a.t.Helper()
	ctl, _ := kernel.Use[chatcontract.Controller](a.shell.Kernel, chatcontract.ControllerKey)
	ctl.Open(s)
	a.settle()
}

func TestPaletteRunsCommandsOtherPluginsContributed(t *testing.T) {
	a := startApp(t)
	a.command("palette.open")
	for _, want := range []string{"Fix the parser", "Write the docs", "New Chat", "Settings…", "⌘,", "Toggle Sidebar", "Attach Files…"} {
		if !a.tt.HasText(want) {
			t.Fatalf("%q is missing from the palette: %q", want, a.tt.Texts())
		}
	}
	if a.tt.HasText("Command Palette…") {
		t.Fatal("the palette lists itself")
	}
	a.tt.Type("settings")
	a.settle()
	a.tt.Key(0, ui.KeyEnter)
	a.settle()
	if !a.tt.HasText("Back to chat") {
		t.Fatalf("the Settings command did not run: %q", a.tt.Texts())
	}
}

func TestPaletteOpensAConversationWithoutTheSidebar(t *testing.T) {
	a := startApp(t)
	a.agent.Stored["s2"] = []agent.Message{{ID: "m1", Role: agent.RoleUser, Text: "document the kernel"}}
	a.command("palette.open")
	a.tt.Type("docs")
	a.settle()
	if a.tt.HasText("/work/app") { // the folder shows only in the palette
		t.Fatalf("the filter kept an unrelated conversation: %q", a.tt.Texts())
	}
	a.tt.Key(0, ui.KeyEnter)
	a.settle()
	if !a.tt.HasText("document the kernel") {
		t.Fatalf("the conversation did not open: %q", a.tt.Texts())
	}
	if !slices.Contains(a.agent.Calls(), "messages s2") {
		t.Fatalf("calls %q", a.agent.Calls())
	}
}

func TestMentionedFilesGoWithTheMessage(t *testing.T) {
	a := startApp(t)
	a.openConversation(agent.Session{ID: "s1", Title: "Fix the parser", Directory: "/work/app"})
	if err := a.tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	a.tt.Type("review @ma")
	a.until("the suggestions", func() bool { return a.tt.HasText("mail.go") })
	for _, want := range []string{"mail.go", "main.go", "src"} {
		if !a.tt.HasText(want) {
			t.Fatalf("%q is missing from the suggestions: %q", want, a.tt.Texts())
		}
	}
	if got := a.files.Calls(); !slices.Contains(got, "find /work/app|ma") {
		t.Fatalf("files were looked up in the wrong folder: %q", got)
	}
	// The list is docs/map.md, src/mail.go, src/main.go.
	a.tt.Key(0, ui.KeyDown)
	a.tt.Key(0, ui.KeyDown)
	a.tt.Key(0, ui.KeyEnter) // chooses; the second Enter sends
	a.settle()
	a.tt.Type("now")
	a.settle()
	a.tt.Key(0, ui.KeyEnter)
	a.settle()
	if got := a.agent.Calls(); !slices.Contains(got, "send s1 review @src/main.go now +src/main.go") {
		t.Fatalf("calls %q", got)
	}
	if !a.tt.HasText("review @src/main.go now") || !a.tt.HasText("src/main.go") {
		t.Fatalf("the transcript does not show the message and its file: %q", a.tt.Texts())
	}
}
