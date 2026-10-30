package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
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
	"wada-desktop-go/internal/plugins/folderpicker"
	"wada-desktop-go/internal/plugins/markup"
	"wada-desktop-go/internal/plugins/mentions"
	"wada-desktop-go/internal/plugins/models"
	"wada-desktop-go/internal/plugins/modes"
	"wada-desktop-go/internal/plugins/palette"
	"wada-desktop-go/internal/plugins/tabs"
	"wada-desktop-go/internal/plugins/usage"
	"wada-desktop-go/internal/plugins/workflow"
	"wada-desktop-go/internal/plugins/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/plugins/inspector"
	"github.com/xleotranx/wada-kit/plugins/settings"
	"github.com/xleotranx/wada-kit/shell"
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
	a.ModeList = []agent.Mode{{ID: "build", Name: "Build"}, {ID: "plan", Name: "Plan"}}
	a.Pages["|"] = agent.SessionPage{Sessions: []agent.Session{
		{ID: "s1", Title: "Fix the parser", Directory: "/work/app", Updated: 30},
		{ID: "s2", Title: "Write the docs", Directory: "/work/docs", Updated: 20},
		{ID: "cli", Title: "Started in a terminal", Directory: "/work/app", Updated: 10}}}
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
	s, err := shell.New(shell.Options{Name: wada.Name, Menus: wada.Menus, Post: post, Strict: true, Store: kerneltest.NewStore()},
		a.New, files.New, chat.New, models.New, modes.New, usage.New, filechanges.New, attachments.New, markup.New, mentions.New,
		workspace.New, folderpicker.New, tabs.New, explore.New, workflow.New, flows.New, palette.New, inspector.New, settings.New)
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
	// The app lists only what it started: as if it had started these two.
	for _, c := range []chatcontract.Conversation{
		{Session: "s2", Title: "Write the docs", Directory: "/work/docs"},
		{Session: "s1", Title: "Fix the parser", Directory: "/work/app"},
	} {
		kernel.Emit(s.Kernel, chatcontract.Started, c)
	}
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

// newChatIn switches to a folder with the space switcher, then opens a chat.
func (a *app) newChatIn(folder string) {
	a.t.Helper()
	open, _ := kernel.Use[tabcontract.Service](a.shell.Kernel, tabcontract.ServiceKey)
	current := "No folder"
	if space := open.Space(); space != "" {
		current = path.Base(space)
	}
	for _, step := range []string{"Space " + current, "Switch to " + folder, "New chat"} {
		if err := a.tt.Click(step); err != nil {
			a.t.Fatalf("%s: %v", step, err)
		}
		a.settle()
	}
}

func (a *app) openConversation(s agent.Session) {
	a.t.Helper()
	ctl, _ := kernel.Use[chatcontract.Controller](a.shell.Kernel, chatcontract.ControllerKey)
	ctl.Open(s)
	a.settle()
}

func TestPaletteFindsTheFolderPickersCommand(t *testing.T) {
	a := startApp(t)
	a.command("palette.open")
	a.tt.Type("open folder")
	a.settle()
	if !a.tt.HasText("Open Folder…") || !a.tt.HasText("⌘O") {
		t.Fatalf("Open Folder… is not found: %q", a.tt.Texts())
	}
}

func TestPaletteRunsCommandsOtherPluginsContributed(t *testing.T) {
	a := startApp(t)
	a.command("palette.open")
	for _, want := range []string{"Open in tabs", "Fix the parser", "Write the docs", "Projects", "New Chat", "Close Tab"} {
		if !a.tt.HasText(want) {
			t.Fatalf("%q is missing from the palette: %q", want, a.tt.Texts())
		}
	}
	if a.tt.HasText("All Chats…") {
		t.Fatal("the palette lists itself")
	}
	a.tt.Type("settings")
	a.settle()
	if !a.tt.HasText("Settings…") || !a.tt.HasText("⌘,") {
		t.Fatalf("the Settings command is not found: %q", a.tt.Texts())
	}
	a.tt.Key(0, ui.KeyEnter)
	a.settle()
	if !a.tt.HasText("Color theme") {
		t.Fatalf("the Settings command did not run: %q", a.tt.Texts())
	}
	if _, ok := a.tt.Find("Close Settings"); !ok {
		t.Fatalf("Settings has no tab: %q", a.tt.Texts())
	}
}

func TestAllChatsOpensAConversationInATab(t *testing.T) {
	a := startApp(t)
	a.agent.Stored["s2"] = []agent.Message{{ID: "m1", Role: agent.RoleUser, Text: "document the kernel"}}
	a.command("palette.open")
	a.tt.Type("write")
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
	if _, ok := a.tt.Find("Close Write the docs"); !ok {
		t.Fatalf("the conversation has no tab: %q", a.tt.Texts())
	}
}

func TestEachFolderIsASpaceOfItsOwn(t *testing.T) {
	a := startApp(t)
	a.openConversation(agent.Session{ID: "s1", Title: "Fix the parser", Directory: "/work/app"})
	a.openConversation(agent.Session{ID: "s2", Title: "Write the docs", Directory: "/work/docs"})
	if !a.tt.HasText("docs") || !a.tt.HasText("Write the docs") || a.tt.HasText("Fix the parser") {
		t.Fatalf("the docs space: %q", a.tt.Texts())
	}
	a.newChatIn("docs")
	if err := a.tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	a.tt.Type("outline the guide")
	a.tt.Key(0, ui.KeyEnter)
	a.settle()
	if !slices.Contains(a.agent.Calls(), "create /work/docs  build") {
		t.Fatalf("calls %q", a.agent.Calls())
	}
	ws, _ := kernel.Use[wscontract.Service](a.shell.Kernel, wscontract.ServiceKey)
	if conv, ok := ws.Lookup("new"); !ok || conv.Folder != "/work/docs" {
		t.Fatalf("the new conversation was recorded as %+v, %v", conv, ok)
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

// completeWorkflow scripts the same main session through all phases and a
// distinct reviewer. The protocol is exercised through the composed native UI.
func completeWorkflow(a *plugintest.Agent, req agent.Send) {
	result := `{"status":"ready","summary":"Implemented and checked."}`
	if strings.Contains(string(req.ResultSchema), `"findings"`) {
		result = `{"status":"pass","summary":"Independent review passed.","findings":[]}`
	}
	if strings.Contains(string(req.ResultSchema), `"action"`) {
		result = `{"action":"answer","summary":"Because the parser needs it."}`
	}
	a.Emit(agent.Event{Kind: agent.TurnStarted, Session: req.Session, RequestID: req.ID},
		agent.Event{Kind: agent.TextEnded, Session: req.Session, RequestID: req.ID, Text: &agent.TextEvent{Message: agent.MessageID(req.ID), Text: "Implemented and checked."}},
		agent.Event{Kind: agent.TurnSucceeded, Session: req.Session, RequestID: req.ID, Result: json.RawMessage(result)})
}
func TestPlanCodeReviewCommandSelectsCurrentComposer(t *testing.T) {
	a := startApp(t)
	a.agent.OnSend = func(req agent.Send) { completeWorkflow(a.agent, req) }
	a.command("flows.plan-code-review")
	if a.tt.HasText("Start a workflow") || !a.tt.HasText("Plan, code, review") {
		t.Fatalf("command did not select composer: %q", a.tt.Texts())
	}
	if err := a.tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	a.tt.Type("add a verbose flag")
	a.settle()
	if err := a.tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	a.until("review completion", func() bool { return a.tt.HasText("Plan, code, review · Passed") })
	children := a.agent.SpawnRequests()
	if len(children) != 1 {
		t.Fatalf("children %+v", children)
	}
	if a.agent.Called("send "+string(children[0].Parent)+" ") != 3 {
		t.Fatalf("main execution %v", a.agent.Calls())
	}
	if err := a.tt.Click("Details"); err != nil {
		t.Fatal(err)
	}
	a.settle()
	if !a.tt.HasText("pass · Independent review passed.") {
		t.Fatalf("missing review detail: %q", a.tt.Texts())
	}
}
func TestTheModePickerRunsWorkflowInsideNewAndExistingChat(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			a := startApp(t)
			a.agent.Numbered = true
			a.agent.OnSend = func(req agent.Send) { completeWorkflow(a.agent, req) }
			if existing {
				a.openConversation(agent.Session{ID: "s2", Title: "Write the docs", Directory: "/work/docs"})
			} else {
				a.newChatIn("docs")
			}
			if err := a.tt.Click("Compose"); err != nil {
				t.Fatal(err)
			}
			a.tt.Type("outline the guide")
			a.settle()
			if err := a.tt.Click("Build"); err != nil {
				t.Fatal(err)
			}
			a.settle()
			if err := a.tt.Click("Plan, code, review"); err != nil {
				t.Fatal(err)
			}
			a.settle()
			if err := a.tt.Click("Send"); err != nil {
				t.Fatal(err)
			}
			a.until("review completion", func() bool { return a.tt.HasText("Plan, code, review · Passed") })
			ctl, _ := kernel.Use(a.shell.Kernel, chatcontract.ControllerKey)
			current := ctl.Current()
			if existing && current.Session != "s2" {
				t.Fatal("workflow left existing chat")
			}
			if !a.tt.HasText("Build") || !a.tt.HasText("outline the guide") {
				t.Fatalf("chat and reset picker missing: %q", a.tt.Texts())
			}
			children := a.agent.SpawnRequests()
			if len(children) != 1 || children[0].Parent != current.Session {
				t.Fatalf("children %+v", children)
			}
			ws, _ := kernel.Use(a.shell.Kernel, wscontract.ServiceKey)
			if _, ok := ws.Lookup(children[0].Parent); !ok {
				t.Fatal("main conversation unrecorded")
			}
			if _, ok := ws.Lookup("child-1"); ok {
				t.Fatal("reviewer listed as top-level chat")
			}
			if err := a.tt.Click("Compose"); err != nil {
				t.Fatal(err)
			}
			a.tt.Type("why this approach?")
			a.settle()
			if err := a.tt.Click("Send"); err != nil {
				t.Fatal(err)
			}
			a.until("follow-up answered", func() bool { return a.agent.Called("send "+string(current.Session)+" why this approach?") == 1 })
			a.settle()
			if len(a.agent.SpawnRequests()) != 1 {
				t.Fatal("question restarted review")
			}
		})
	}
}

// TestWadaKeepsItsDataAndMenus pins where Wada's data lives, which a new name
// would quietly leave behind, and gives every command's menu a place in the
// menu bar.
func TestWadaKeepsItsDataAndMenus(t *testing.T) {
	if wada.Name != "Wada" || wada.Folder != "wada-desktop-go" || wada.Env != "WADA" {
		t.Fatalf("app %+v", wada)
	}
	ap := startApp(t)
	for _, e := range kernel.Items(ap.shell.Kernel, kernel.Commands) {
		if m := e.Value.Menu; m != "" && !slices.Contains(wada.Menus, m) {
			t.Errorf("command %s names menu %q, which the menu bar lacks", e.ID, m)
		}
	}
}

func TestWorkPanelPlacesFileChangesBelowContext(t *testing.T) {
	a := startApp(t)
	a.agent.Stored["s1"] = []agent.Message{{ID: "a1", Role: agent.RoleAssistant,
		Tokens: agent.Tokens{Input: 1000, Output: 200},
		Parts: []agent.Part{{Kind: agent.PartTool, Tool: &agent.Tool{
			ID: "t1", Kind: agent.ToolShell, Command: "go test ./...",
		}}}}}
	a.agent.Changed["s1"] = []agent.FileChange{{Path: "src/main.go", Added: 3, Removed: 1,
		Patch: "@@ -1 +1 @@\n-old\n+new\n"}}
	a.openConversation(agent.Session{ID: "s1", Title: "Fix the parser", Directory: "/work/app"})
	var titles []string
	for _, e := range kernel.Items(a.shell.Kernel, chatcontract.Work) {
		titles = append(titles, e.Value.Title)
	}
	if !slices.Equal(titles, []string{"Context", "File changes", "Cost"}) {
		t.Fatalf("work panel order: %q", titles)
	}
	for _, want := range []string{"Context", "File changes", "Cost", "1 file", "+3", "−1"} {
		if !a.tt.HasText(want) {
			t.Fatalf("%q missing: %q", want, a.tt.Texts())
		}
	}
	if a.tt.HasText("Last command") {
		t.Fatal("the work panel still shows the last command")
	}
	if a.tt.HasText("main.go") {
		t.Fatal("the file list should start collapsed")
	}
	if err := a.tt.Click("Expand file list"); err != nil {
		t.Fatal(err)
	}
	a.settle()
	if err := a.tt.Click("src/main.go"); err != nil {
		t.Fatal(err)
	}
	a.settle()
	if !a.tt.HasText("+new") {
		t.Fatalf("file diff did not open: %q", a.tt.Texts())
	}
}

func TestFollowupCanBeQueuedAndRemovedWhilePlanRuns(t *testing.T) {
	a := startApp(t)
	a.openConversation(agent.Session{ID: "s2", Title: "Write the docs", Directory: "/work/docs"})
	a.agent.OnSend = func(req agent.Send) {}
	a.command("flows.plan-code-review")
	if err := a.tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	a.tt.Type("implement a flag")
	a.settle()
	if err := a.tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	a.until("planning", func() bool { return a.agent.Called("send s2 implement a flag") == 1 })
	if err := a.tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	a.tt.Type("why this approach?")
	a.settle()
	if err := a.tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	a.until("queue visible", func() bool { return a.tt.HasText("Queued · handled after the active turn") })
	if a.agent.Called("send s2 why this approach?") != 0 {
		t.Fatal("followup bypassed safe boundary")
	}
	if err := a.tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	a.settle()
	if a.tt.HasText("why this approach?") || a.tt.HasText("Queued · handled after the active turn") {
		t.Fatalf("removed queue input remains visible: %q", a.tt.Texts())
	}
}
