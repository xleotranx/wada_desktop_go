package desktop

import (
	"testing"
	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

// TestCommandPalette drives Cmd+K end to end: the dialog takes the
// keyboard, filters by what is typed, and runs the chosen action.
func TestCommandPalette(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{{ID: "ses_1", Title: "First conversation"}}
	a.scope().sessionsLoaded = true
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !a.layout.commandOpen || !tt.Focused("Command palette") {
		t.Fatalf("Cmd+K left the palette open=%v focused=%v", a.layout.commandOpen, tt.Focused("Command palette"))
	}
	if !tt.HasText("Find in chat") {
		t.Fatalf("the palette lists no action: %v", tt.Texts())
	}

	tt.Type("files")
	if err := tt.Click("Quick open file"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.commandOpen || !codingModule(a).QuickOpen {
		t.Fatalf("activation: open=%v quick open=%v",
			a.layout.commandOpen, codingModule(a).QuickOpen)
	}
}

// TestCommandPaletteOpensAChat checks a chat entry opens its conversation
// and leaves the palette.
func TestCommandPaletteOpensAChat(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{{ID: "ses_2", Title: "Second conversation"}}
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
