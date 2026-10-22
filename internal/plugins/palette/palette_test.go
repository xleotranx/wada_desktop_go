package palette

import (
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

// commands contributes two commands, as another plugin would.
type commands struct {
	ran     []string
	stopped bool // "Stop Everything" is enabled while this is set
}

func (*commands) Manifest() kernel.Manifest { return kernel.Manifest{ID: "ext"} }

func (x *commands) Setup(c *kernel.Context) error {
	for _, err := range []error{
		kernel.Contribute(c, kernel.Commands, "ext.hello", 0, kernel.Command{
			Title: "Say Hello", Menu: "Chat", Keys: []string{"cmd+shift+h"}, Run: func() { x.ran = append(x.ran, "hello") }}),
		kernel.Contribute(c, kernel.Commands, "ext.stop", 1, kernel.Command{
			Title: "Stop Everything", Enabled: func() bool { return x.stopped }, Run: func() { x.ran = append(x.ran, "stop") }}),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Workspace, *plugintest.Chat, *commands, *ui.Tester) {
	t.Helper()
	ws := &plugintest.Workspace{Convs: []workspace.Conversation{
		{ID: "s1", Title: "Fix the parser", Folder: "/w/app", Updated: 30},
		{ID: "s2", Title: "Write docs", Updated: 20},
		{ID: "s3", Title: "Plan the release", Updated: 10},
		{ID: "s4", Title: "Tidy the tests", Updated: 9},
		{ID: "s5", Title: "Bump the deps", Updated: 8},
		{ID: "s9", Title: "Old migration notes", Updated: 1}}}
	chat, ext, p := &plugintest.Chat{}, &commands{}, &Plugin{}
	h := kerneltest.New(t, ws, chat, ext, p)
	h.Flush()
	tt := ui.NewTester(p.view, 800, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	return h, p, ws, chat, ext, tt
}

// open shows the palette the way its command does and settles the frame.
func open(h *kerneltest.Harness, p *Plugin, tt *ui.Tester) {
	p.toggle()
	h.Flush()
	tt.Frame()
}

func TestListsRecentConversationsThenEveryCommand(t *testing.T) {
	h, p, _, _, _, tt := start(t)
	tt.Frame()
	if tt.HasText("Say Hello") {
		t.Fatal("the palette is drawn while closed")
	}
	open(h, p, tt)
	for _, want := range []string{"Fix the parser", "/w/app", "Write docs", "Say Hello", "⇧⌘H", "Stop Everything"} {
		if !tt.HasText(want) {
			t.Fatalf("%q is missing from %q", want, tt.Texts())
		}
	}
	if tt.HasText("Command Palette…") {
		t.Fatal("the palette lists its own command")
	}
	if !tt.Focused("Search commands and conversations") {
		t.Fatal("the search field does not have the focus")
	}
}

func TestEnterRunsTheMatchingCommandAfterTheFrame(t *testing.T) {
	h, p, _, _, ext, tt := start(t)
	open(h, p, tt)
	tt.Type("hello")
	tt.Frame()
	if tt.HasText("Fix the parser") || !tt.HasText("Say Hello") {
		t.Fatalf("filtered rows %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if p.open {
		t.Fatal("the palette stays open after a command")
	}
	if len(ext.ran) != 0 {
		t.Fatal("the command ran inside the activating handler")
	}
	h.Flush()
	if !slices.Equal(ext.ran, []string{"hello"}) {
		t.Fatalf("ran %v", ext.ran)
	}
}

func TestADisabledCommandDoesNotRun(t *testing.T) {
	h, p, _, _, ext, tt := start(t)
	open(h, p, tt)
	tt.Type("stop")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	h.Flush()
	if len(ext.ran) != 0 || !p.open {
		t.Fatalf("a disabled command ran %v or closed the palette (open=%v)", ext.ran, p.open)
	}
	ext.stopped = true
	p.run("ext.stop") // the state can change between the click and the run
	if !slices.Equal(ext.ran, []string{"stop"}) {
		t.Fatalf("an enabled command did not run: %v", ext.ran)
	}
}

func TestChoosingAConversationOpensIt(t *testing.T) {
	h, p, _, chat, _, tt := start(t)
	open(h, p, tt)
	if err := tt.Click("Write docs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	if chat.Conv.Session != "s2" || p.open {
		t.Fatalf("conversation %+v open=%v", chat.Conv, p.open)
	}
}

func TestTypingFindsConversationsBeyondTheNewest(t *testing.T) {
	h, p, _, chat, _, tt := start(t)
	open(h, p, tt)
	if tt.HasText("Old migration notes") {
		t.Fatalf("more than the newest five before typing: %q", tt.Texts())
	}
	tt.Type("old")
	tt.Frame()
	if !tt.HasText("Old migration notes") || tt.HasText("Fix the parser") {
		t.Fatalf("rows %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	h.Flush()
	if chat.Conv.Session != "s9" {
		t.Fatalf("opened %+v", chat.Conv)
	}
	// Reopening starts from nothing: the old query is gone.
	open(h, p, tt)
	if tt.HasText("Old migration notes") || p.state.Query != "" {
		t.Fatalf("stale search after reopening: %q", tt.Texts())
	}
}

func TestListsOnlyTheRecordedConversations(t *testing.T) {
	h, p, ws, _, _, tt := start(t)
	ws.Convs = ws.Convs[:1]
	open(h, p, tt)
	if !tt.HasText("Fix the parser") || tt.HasText("Write docs") {
		t.Fatalf("rows %q", tt.Texts())
	}
}

func TestCommandsWorkWithoutABackend(t *testing.T) {
	ext, p := &commands{}, &Plugin{}
	h := kerneltest.New(t, ext, p)
	h.Flush()
	tt := ui.NewTester(p.view, 800, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	p.toggle()
	h.Flush()
	tt.Frame()
	if !tt.HasText("Say Hello") || tt.HasText("Conversations") {
		t.Fatalf("rows %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	h.Flush()
	if !slices.Equal(ext.ran, []string{"hello"}) {
		t.Fatalf("ran %v", ext.ran)
	}
}

func TestShortcutLabels(t *testing.T) {
	for in, want := range map[string]string{
		"cmd+n": "⌘N", "cmd+shift+a": "⇧⌘A", "cmd+.": "⌘.", "cmd+,": "⌘,", "ctrl+alt+cmd+shift+x": "⌃⌥⇧⌘X",
		"cmd+enter": "⌘↩", "f5": "F5", "cmd+left": "⌘←",
	} {
		if got := shortcutLabel(in); got != want {
			t.Errorf("%q is %q, want %q", in, got, want)
		}
	}
}

// openTabs stands in for the tabs plugin with a fixed list.
type openTabs struct{ list []tabs.Tab }

func (*openTabs) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "tabs", Provides: []kernel.Named{tabs.ServiceKey}}
}
func (o *openTabs) Setup(c *kernel.Context) error {
	return kernel.Provide[tabs.Service](c, tabs.ServiceKey, o)
}
func (o *openTabs) Tabs() []tabs.Tab         { return o.list }
func (o *openTabs) Active() (tabs.Tab, bool) { return tabs.Tab{}, false }
func (o *openTabs) Select(string)            {}
func (o *openTabs) Close(string)             {}
func (o *openTabs) Move(string, int)         {}
func (o *openTabs) Space() string            { return "" }

func TestGroupsOpenTabsEarlierChatsAndProjects(t *testing.T) {
	ws := &plugintest.Workspace{
		FolderSet: []workspace.Folder{{Path: "/w/app"}, {Path: "/w/web"}},
		Convs: []workspace.Conversation{
			{ID: "s1", Title: "Fix the parser", Folder: "/w/app"},
			{ID: "s2", Title: "Write docs", Folder: "/w/app"},
		}}
	chat, p := &plugintest.Chat{}, &Plugin{}
	open := &openTabs{list: []tabs.Tab{{ID: "chat:s2", Kind: tabs.Conversation, Session: "s2"}}}
	h := kerneltest.New(t, ws, chat, open, p)
	h.Flush()
	got := map[string]string{}
	for _, it := range p.items() {
		got[it.Title] = it.Group
	}
	for title, group := range map[string]string{"Write docs": groupOpen, "Fix the parser": groupEarlier, "app": groupFolders, "web": groupFolders} {
		if got[title] != group {
			t.Fatalf("%q is in %q, want %q (%v)", title, got[title], group, got)
		}
	}
	p.run(folderID + "/w/web")
	if chat.Conv.Directory != "/w/web" || chat.Conv.Session != "" {
		t.Fatalf("a project opened %+v", chat.Conv)
	}
}

func TestOnlyTheLatestFoldersShowBeforeTyping(t *testing.T) {
	ws := &plugintest.Workspace{}
	for i := range 8 {
		path := "/w/f" + string(rune('a'+i))
		ws.FolderSet = append(ws.FolderSet, workspace.Folder{Path: path, Added: int64(i)})
	}
	ws.Convs = []workspace.Conversation{{ID: "s1", Title: "Old work", Folder: "/w/fa", Updated: 100}}
	p := &Plugin{}
	h := kerneltest.New(t, ws, &plugintest.Chat{}, p)
	h.Flush()
	var shown []string
	for _, it := range filter(p.items(), "") {
		if it.Group == groupFolders {
			shown = append(shown, it.Title)
		}
	}
	// fa was worked in last, then the newest added.
	if want := []string{"fa", "fh", "fg", "ff", "fe"}; !slices.Equal(shown, want) {
		t.Fatalf("folders %q", shown)
	}
	found := false
	for _, it := range filter(p.items(), "fb") {
		found = found || it.Title == "fb"
	}
	if !found {
		t.Fatal("typing does not find a folder past the first few")
	}
}
