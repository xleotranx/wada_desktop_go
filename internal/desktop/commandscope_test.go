package desktop

import (
	"testing"
	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"

	"github.com/oneo-me/onevra/source/components/command"
)

// TestCommandScopeFilter narrows by group before matching and keeps the
// Filter rows available.
func TestCommandScopeFilter(t *testing.T) {
	items := []command.Item{
		{ID: "a", Title: "Open Settings", Group: "Tools"},
		{ID: "b", Title: "First conversation", Group: "Chats"},
		{ID: "c", Title: "New chat in wiring", Group: "Projects"},
		{ID: "d", Title: "Show commands", Group: "Filter"},
	}
	a := newTestApp(t)
	commands := a.filterCommandItems(items, ">")
	for _, item := range commands {
		if item.Group == "Chats" || item.Group == "Projects" {
			t.Fatalf("a chat survived the command scope: %+v", item)
		}
	}
	chats := a.filterCommandItems(items, "@")
	if len(chats) != 3 {
		t.Fatalf("chat scope: %+v", chats)
	}
	for _, item := range chats {
		if item.Group == "Tools" {
			t.Fatalf("a tool survived the chat scope: %+v", item)
		}
	}
	if got := a.filterCommandItems(items, "@ first"); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("chat search: %+v", got)
	}
}

// TestCommandPaletteScopeChoice keeps the dialog open when a scope row is
// chosen, then filters the search inside it.
func TestCommandPaletteScopeChoice(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{{ID: "ses_1", Title: "First conversation"}}
	a.scope().sessionsLoaded = true
	a.openCommandPalette()
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Show chats"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.layout.commandOpen || a.layout.commandScope != commandScopeChats {
		t.Fatalf("scope choice: open=%v scope=%q", a.layout.commandOpen, a.layout.commandScope)
	}
	tt.Type("First")
	tt.Frame()
	if !tt.HasText("First conversation") {
		t.Fatalf("the chat scope lost its chat: %v", tt.Texts())
	}
	if tt.HasText("Open Settings") {
		t.Fatal("a tool came through the chat scope")
	}
}
