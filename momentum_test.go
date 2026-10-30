package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
)

// Optional local visual verification; the shell and backend remain in memory.
func captureMomentum(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	// Tester time is explicit; let dialog fades finish before inspection.
	tt.Advance(time.Second)
	if dir := os.Getenv("WADA_TEST_CAPTURE"); dir != "" {
		file, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := png.Encode(file, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

// selectText drags across the words of text in the window, as a user
// selects them.
func (a *app) selectText(text string) {
	a.t.Helper()
	r, ok := a.tt.Find(text)
	if !ok {
		a.t.Fatalf("no %q in %q", text, a.tt.Texts())
	}
	y := r.Y + r.H/2
	a.tt.Press(r.X+1, y)
	a.tt.Frame()
	a.tt.Move(r.X+r.W/2, y)
	a.tt.Frame()
	a.tt.Move(r.X+r.W-1, y)
	a.tt.Frame()
	a.tt.Release(r.X+r.W-1, y)
	a.tt.Frame()
}

func TestASideChatFromSelectedTextReturnsAnAnswerToTheOriginalDraft(t *testing.T) {
	a := startApp(t)
	a.agent.Stored["s1"] = []agent.Message{
		{ID: "u1", Role: agent.RoleUser, Text: "Could the parser be simpler?"},
		{ID: "a1", Role: agent.RoleAssistant, Parts: []agent.Part{{Kind: agent.PartText, Text: "We can use a state machine."}}},
	}
	a.openConversation(agent.Session{ID: "s1", Title: "Fix the parser", Directory: "/work/app"})
	ctl, _ := kernel.Use(a.shell.Kernel, chat.ControllerKey)
	ctl.SetDraft("Keep my unfinished thought")
	a.settle()
	a.selectText("We can use a state machine.")
	captureMomentum(t, a.tt, "side-chat-selection")
	if err := a.tt.Click("Start a side chat"); err != nil {
		t.Fatalf("%v in %q", err, a.tt.Texts())
	}
	a.settle()
	if !a.tt.HasText("No messages yet") || !a.tt.HasText("We can use a state machine.") || !a.tt.Focused("Side chat message") {
		t.Fatalf("the drawer did not open with the quote: %q", a.tt.Texts())
	}
	captureMomentum(t, a.tt, "side-chat-empty")
	a.tt.Type("What about a parser combinator?")
	a.tt.Key(0, ui.KeyEnter)
	a.until("the side chat's first message", func() bool { return a.agent.Called("send child-1") == 1 })
	a.agent.Respond("child-1", "Keep the state machine for now.")
	a.until("the answer", func() bool { return a.tt.HasText("Add to main chat") })
	captureMomentum(t, a.tt, "side-chat")
	if send, ok := a.tt.Find("Side chat message"); !ok || send.Y+send.H > 800 {
		t.Fatalf("the side chat's composer is outside the window: %+v", send)
	}
	if ctl.Current().Session != "s1" || ctl.DraftText() != "Keep my unfinished thought" {
		t.Fatalf("the side chat changed the main chat: %+v, %q", ctl.Current(), ctl.DraftText())
	}
	ws, _ := kernel.Use(a.shell.Kernel, workspace.ServiceKey)
	if _, ok := ws.Lookup("child-1"); ok {
		t.Fatal("the side chat joined All chats")
	}
	if err := a.tt.Click("Add to main chat"); err != nil {
		t.Fatal(err)
	}
	a.settle()
	if !strings.HasPrefix(ctl.DraftText(), "Keep my unfinished thought\n\n") || !strings.Contains(ctl.DraftText(), "Keep the state machine for now.") {
		t.Fatalf("draft %q", ctl.DraftText())
	}
	if a.agent.Called("send s1") != 0 {
		t.Fatal("the answer was sent to the main chat without the user pressing Send")
	}
	if err := a.tt.Click("Close side chat"); err != nil {
		t.Fatal(err)
	}
	a.settle()
	if a.tt.HasText("Add to main chat") {
		t.Fatalf("the drawer did not close: %q", a.tt.Texts())
	}
	a.command("explore.show")
	if !a.tt.HasText("Keep the state machine for now.") {
		t.Fatalf("Show Side Chat did not bring it back: %q", a.tt.Texts())
	}
}
