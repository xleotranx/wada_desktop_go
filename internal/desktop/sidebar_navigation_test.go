package desktop

import (
	"fmt"
	"testing"

	"wada-desktop-go/internal/agent"

	"github.com/egoist/mygo/ui"
)

func TestChatSwitchKeepsSidebarPositionDuringSave(t *testing.T) {
	a := newTestApp(t)
	for i := range 40 {
		id := fmt.Sprintf("chat-%02d", i)
		a.scope().sessions = append(a.scope().sessions, agent.Session{ID: id, Title: fmt.Sprintf("Chat %02d", i)})
		a.scope().pinned[id] = true
		a.scope().pinOrder = append(a.scope().pinOrder, id)
	}
	a.adoptSession(a.scope().sessions[0])
	tt := ui.NewTester(a.view, 1400, 700)
	unscrolled, _ := tt.Find("Chat 11")
	tt.Scroll(180, 350, 0, 300)
	before, ok := tt.Find("Chat 11")
	if !ok || before.Y < 100 || before.Y+before.H > 700 || before.Y >= unscrolled.Y {
		t.Fatalf("scroll fixture did not expose the target row: %+v", before)
	}
	newChat, _ := tt.Find("New chat")

	// Hold persistence pending, then publish its completion independently of
	// navigation, just as a slow disk can across several native frames.
	a.preferences.metadata = &metadataWriter{queue: make(chan metadataWrite, 1)}
	t.Cleanup(func() { a.preferences.metadata = nil })
	if err := tt.Click("Chat 12"); err != nil {
		t.Fatal(err)
	}
	if a.conversation.target.session != "chat-12" || len(a.preferences.metadata.queue) == 0 {
		t.Fatal("chat navigation did not queue persistence")
	}
	check := func(stage string) {
		t.Helper()
		tt.Frame()
		if got, _ := tt.Find("New chat"); got != newChat {
			t.Errorf("%s moved the sidebar controls: %+v -> %+v", stage, newChat, got)
		}
		if got, _ := tt.Find("Chat 11"); got != before {
			t.Errorf("%s moved the scrolled chat: %+v -> %+v", stage, before, got)
		}
		if tt.HasText("Saving…") {
			t.Errorf("%s showed routine background persistence in navigation", stage)
		}
	}
	check("pending save")
	a.acceptMetadataWrite(a.preferences.metadataVersion, nil)
	check("completed save")
}
