package chat

import (
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// selectText drags across the words of s in the window, as a user selects
// them.
func selectText(t *testing.T, tt *ui.Tester, s string) {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no %q in %q", s, tt.Texts())
	}
	y := r.Y + r.H/2
	tt.Press(r.X+1, y)
	tt.Frame()
	tt.Move(r.X+r.W/2, y)
	tt.Frame()
	tt.Move(r.X+r.W-1, y)
	tt.Frame()
	tt.Release(r.X+r.W-1, y)
	tt.Frame()
}

func TestSelectedTextOffersActionsAndReplyQuotesIt(t *testing.T) {
	var got []chat.Selection
	side := &extension{setup: func(c *kernel.Context) error {
		kernel.Contribute(c, chat.SelectionActions, "extension.side", 0, chat.SelectionAction{
			Title: "Start a side chat", Run: func(s chat.Selection) { got = append(got, s) }})
		return kernel.Contribute(c, chat.Drawers, "extension.drawer", 0, shell.View{Render: func(c *ui.Context) {
			ui.Column(c).Width(240).Shrink(0).Children(func() { ui.Text(c, "Drawer beside the chat") })
		}})
	}}
	h, p, f, _ := start(t, side.New)
	f.Stored["s1"] = []agent.Message{{ID: "a1", Role: agent.RoleAssistant,
		Parts: []agent.Part{{Kind: agent.PartText, Text: "Use a state machine for the parser"}}}}
	p.Open(agent.Session{ID: "s1", Title: "Parser"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 700)
	tt.Frame()
	if !tt.HasText("Drawer beside the chat") {
		t.Fatalf("the drawer is missing: %q", tt.Texts())
	}
	if tt.HasText("Reply") {
		t.Fatal("the popover shows without a selection")
	}
	selectText(t, tt, "Use a state machine for the parser")
	if !tt.HasText("Start a side chat") || !tt.HasText("Reply") {
		t.Fatalf("no popover over the selection: %q", tt.Texts())
	}
	if err := tt.Click("Start a side chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(got) != 1 || got[0].Text == "" || got[0].Conversation.Session != "s1" {
		t.Fatalf("selection %+v", got)
	}
	if tt.HasText("Start a side chat") {
		t.Fatal("the popover stayed after its action")
	}
	selectText(t, tt, "Use a state machine for the parser")
	p.draft = "Earlier words"
	if err := tt.Click("Reply"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if want := "Earlier words\n\n> " + got[0].Text + "\n\n"; p.draft != want {
		t.Fatalf("draft %q, want %q", p.draft, want)
	}
}

func TestSelectionPopoverWaitsForTheRelease(t *testing.T) {
	h, p, f, _ := start(t)
	f.Stored["s1"] = []agent.Message{{ID: "u1", Role: agent.RoleUser, Text: "Could the parser be simpler?"}, {ID: "a1", Role: agent.RoleAssistant,
		Parts: []agent.Part{{Kind: agent.PartText, Text: "Use a state machine for the parser"}}}}
	p.Open(agent.Session{ID: "s1", Title: "Parser"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 700)
	tt.Frame()
	r, ok := tt.Find("Use a state machine for the parser")
	if !ok {
		t.Fatalf("no answer in %q", tt.Texts())
	}
	y := r.Y + r.H/2
	// Each move draws a frame, as a window does, with no frame between.
	tt.Press(r.X+1, y)
	// Within the last letter, the selection stays as the pointer goes on.
	end := r.X + r.W
	for _, x := range []float32{end - 1, end - 0.8, end - 0.6} {
		tt.Move(x, y)
		if tt.HasText("Reply") {
			t.Fatalf("the popover shows while the drag goes on, at x %v", x)
		}
	}
	tt.Release(end-0.6, y)
	if !tt.HasText("Reply") {
		t.Fatalf("no popover after the release: %q", tt.Texts())
	}
}

func TestQuoteMarksEveryLine(t *testing.T) {
	if got := quote("one\n\ntwo \n"); got != "> one\n>\n> two" {
		t.Fatalf("quote %q", got)
	}
}
