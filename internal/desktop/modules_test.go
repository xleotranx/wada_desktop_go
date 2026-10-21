package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo/ui"
)

// A backend without filesystem or configuration capabilities exercises the shell
// without an OpenCode implementation or either built-in feature module.
type chatBackend struct{ agent.Backend }

func (chatBackend) ID() string { return "chat-test" }
func (chatBackend) Client(agent.Config) (agent.Client, error) {
	return nil, errors.New("no workspace client")
}
func (chatBackend) ValidateAddress(address string) (string, error) { return address, nil }
func newCoreTestApp(t *testing.T, factories ...feature.Factory) *app {
	t.Helper()
	a := newApp(chatBackend{}, factories...)
	a.runtime.background = func(work func() func()) { work()() }
	a.runtime.post = func(fn func()) { fn() }
	installCommandFixture(a, 32)
	t.Cleanup(func() { a.closeWork(); testCommandQueues.Delete(a) })
	return a
}
func TestChatOnlyCompositionRestoresStreamsStopsAndKeepsDrafts(t *testing.T) {
	a := newCoreTestApp(t)
	a.scope().sessionsLoaded = true
	sessions := []agent.Session{{ID: "a", Title: "First"}, {ID: "b", Title: "Second"}}
	a.scope().sessions = sessions
	tt := ui.NewTester(a.view, 1400, 920)
	for _, label := range []string{"Projects", "Choose project", "Changes", "Quick open file", "Terminal", "Asks first", "Explain this codebase"} {
		if tt.HasText(label) {
			t.Fatalf("chat-only view includes %q", label)
		}
	}
	if !tt.HasText("What would you like to discuss?") || !tt.HasText("First") {
		t.Fatalf("chat view missing: %v", tt.Texts())
	}
	a.openConversation(sessions[0])
	runnerSink{a}.Opened(runner.Opened{Session: "a", Revision: a.conversation.target.revision, Messages: []agent.Message{{Type: "assistant", Content: []agent.Part{{Type: "text", Text: "restored answer"}}}}})
	if a.conversation.transcript.Len() != 1 || a.conversation.transcript.Row(0).Text != "restored answer" {
		t.Fatal("normalized history was not restored")
	}
	a.conversation.draft = "unsent words"
	a.openConversation(sessions[1])
	a.openConversation(sessions[0])
	if a.conversation.draft != "unsent words" {
		t.Fatal("navigation lost the draft")
	}
	if !a.send() {
		t.Fatal("chat-only send was rejected")
	}
	a.applyUpdate(agent.Update{Kind: agent.ExecutionStarted, SessionID: "a"})
	a.applyUpdate(agent.Update{Kind: agent.TextStarted, SessionID: "a", Text: &agent.TextData{MessageID: "m"}})
	a.applyUpdate(agent.Update{Kind: agent.TextDelta, SessionID: "a", Text: &agent.TextData{MessageID: "m", Delta: "new answer"}})
	a.applyUpdate(agent.Update{Kind: agent.PermissionAsked, SessionID: "a", Permission: &agent.PermissionData{SessionID: "a", ID: "approval", Action: "external action"}})
	if a.conversation.transcript.Busy() != chat.SessionID("a") || len(a.conversation.transcript.Pending()) != 1 {
		t.Fatal("turn or approval state missing")
	}
	a.stopTurn()
	stopped := false
	for len(commandsOf(a)) > 0 {
		if (<-commandsOf(a)).kind == "stop" {
			stopped = true
		}
	}
	if !stopped {
		t.Fatal("stop was not admitted")
	}
	a.openTool("Files")
	if a.layout.browserTool == "Files" {
		t.Fatal("uninstalled panel opened")
	}
	for _, item := range a.commandItems() {
		for _, id := range []string{"action:folder", "action:files", "action:changes", "action:terminal"} {
			if item.ID == id {
				t.Fatalf("uninstalled action %q", id)
			}
		}
	}
	for _, command := range a.slashCommands() {
		if command.name == "/files" || command.name == "/terminal" {
			t.Fatalf("uninstalled slash action %q", command.name)
		}
	}
	for _, spec := range a.shortcutSpecs() {
		if spec.id == "terminal" || spec.id == "quick-open" {
			t.Fatalf("uninstalled shortcut %q", spec.id)
		}
	}
	for _, menu := range appMenu(a).Items() {
		for _, item := range menu.Submenu {
			if strings.Contains(item.Label, "Terminal") || strings.Contains(item.Label, "Folder") || strings.Contains(item.Label, "Quick Open") {
				t.Fatalf("uninstalled menu %q", item.Label)
			}
		}
	}
}

type notesModule struct {
	actionReads, panelReads int
	host                    feature.Host
	count                   int
	closed                  bool
}

func (n *notesModule) ID() string { return "notes" }
func (n *notesModule) Close()     { n.closed = true }
func (n *notesModule) Actions() []feature.Action {
	n.actionReads++
	return []feature.Action{{ID: "notes", Title: "Open notes", Shortcut: "notes", Mods: ui.Cmd | ui.Alt, Key: ui.KeyN, MenuGroup: "View", MenuLabel: "Notes", Slash: "/notes", Run: func() { n.host.OpenPanel("Notes") }}}
}
func (n *notesModule) Panels() []feature.Panel {
	n.panelReads++
	return []feature.Panel{{ID: "Notes", Title: "Notes", Open: func() {}, Refresh: func() {}, Render: func(c *ui.Context) { ui.Button(c, "Add note").OnClick(func() { n.count++ }) }}}
}
func TestIndependentModuleRegistersPanelCommandsAndInput(t *testing.T) {
	var notes *notesModule
	a := newCoreTestApp(t, func(host feature.Host) feature.Module { notes = &notesModule{host: host}; return notes })
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd|ui.Alt, ui.KeyN)
	tt.Frame()
	if a.layout.browserTool != "Notes" {
		t.Fatal("module shortcut did not open its panel")
	}
	if err := tt.Click("Add note"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if notes.count != 1 {
		t.Fatal("module lost its owned state")
	}
	a.conversation.draft = "/notes"
	if !a.send() || a.conversation.draft != "" {
		t.Fatal("module slash command was not handled")
	}
	a.openSettings(tabShortcuts)
	tt.Frame()
	if !tt.HasText("Open notes") {
		t.Fatal("registered module shortcut is missing from keyboard settings")
	}
	if notes.actionReads != 1 || notes.panelReads != 1 {
		t.Fatalf("contributions rebuilt during frames: actions=%d panels=%d", notes.actionReads, notes.panelReads)
	}
	a.closeWork()
	if !notes.closed {
		t.Fatal("module was not closed")
	}
}
func TestDisabledModuleSettingsSurviveCoreSave(t *testing.T) {
	a := newCoreTestApp(t)
	state := appMetadata{modules: map[string]json.RawMessage{"optional": json.RawMessage(`{"saved":"keep"}`)}}
	a.restoreState(state)
	path := filepath.Join(t.TempDir(), "metadata.db")
	db, err := openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.save(context.Background(), a.stateSnapshot()); err != nil {
		t.Fatal(err)
	}
	restored, err := db.load(context.Background())
	if err != nil || string(restored.modules["optional"]) != `{"saved":"keep"}` {
		t.Fatalf("disabled module state lost: %+v, %v", restored.modules, err)
	}
}
func TestModuleRegistrationRejectsDuplicateIdentity(t *testing.T) {
	factory := func(host feature.Host) feature.Module { return &notesModule{host: host} }
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate module identity was accepted")
		}
	}()
	newCoreTestApp(t, factory, factory)
}
func TestBackendSettingsUseSeparateStores(t *testing.T) {
	t.Setenv("WADA_DATA_DIR", t.TempDir())
	first, err := metadataPath("first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := metadataPath("second")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || filepath.Base(first) != "metadata.db" {
		t.Fatalf("backend store paths: %q and %q", first, second)
	}
	if _, err := metadataPath("../escape"); err == nil {
		t.Fatal("backend id escaped its data directory")
	}
}

func TestNewBackendAttemptInvalidatesPendingModuleRequest(t *testing.T) {
	var module *notesModule
	a := newCoreTestApp(t, func(host feature.Host) feature.Module { module = &notesModule{host: host}; return module })
	runnerSink{a}.Status(runner.Status{Attempt: 1, State: healthConnecting})
	var worker func() func()
	a.runtime.background = func(work func() func()) { worker = work }
	applied := false
	feature.Run(module.host, new(feature.Request), func(context.Context) (string, error) { return "earlier attempt", nil }, func(string, error) { applied = true })
	publish := worker()
	old := a.runtime.workCtx
	runnerSink{a}.Status(runner.Status{Attempt: 2, State: healthConnecting})
	publish()
	if applied || old.Err() == nil {
		t.Fatalf("attempt replacement: applied=%v old work canceled=%v", applied, old.Err() != nil)
	}
}

func TestChatOnlyKeyboardOmitsUninstalledFeatures(t *testing.T) {
	a := newCoreTestApp(t)
	a.openSettings(tabShortcuts)
	tt := ui.NewTester(a.view, 1400, 920)
	for _, label := range []string{"Toggle terminal", "Quick open file", "Open a folder", "Open Changes", "Toggle Files"} {
		if tt.HasText(label) {
			t.Fatalf("keyboard settings advertise uninstalled feature %q", label)
		}
	}
}
