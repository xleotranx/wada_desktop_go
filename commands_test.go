package main

import (
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestCommandPalette drives Cmd+K end to end: the dialog takes the
// keyboard, filters by what is typed, and runs the chosen action.
func TestCommandPalette(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_1", Title: "First conversation"}}
	a.scope().sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !a.layout.commandOpen || !tt.Focused("Command palette") {
		t.Fatalf("Cmd+K left the palette open=%v focused=%v", a.layout.commandOpen, tt.Focused("Command palette"))
	}
	if !tt.HasText("Open Files") || !tt.HasText("First conversation") {
		t.Fatalf("the palette lists neither tool nor chat: %v", tt.Texts())
	}

	tt.Type("files")
	if err := tt.Click("Open Files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.commandOpen || a.layout.browserCollapsed || a.layout.browserTool != "Files" {
		t.Fatalf("activation: open=%v collapsed=%v tool=%q",
			a.layout.commandOpen, a.layout.browserCollapsed, a.layout.browserTool)
	}
}

// TestCommandPaletteOpensAChat checks a chat entry opens its conversation
// and leaves the palette.
func TestCommandPaletteOpensAChat(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_2", Title: "Second conversation"}}
	a.scope().sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	tt.Type("second")
	if err := tt.Click("Second conversation"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.commandOpen || string(a.conversation.target.session) != "ses_2" {
		t.Fatalf("open=%v session=%q", a.layout.commandOpen, a.conversation.target.session)
	}
}
