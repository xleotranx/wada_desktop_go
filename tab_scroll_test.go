package main

import (
	"testing"
	"time"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestChatTabStripScrolls keeps every tab reachable in a narrow window:
// the strip overflows into a scroll, its arrows page it, and revealing a
// tab brings it back.
func TestChatTabStripScrolls(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	for i := 0; i < 6; i++ {
		id := "ses_" + string(rune('a'+i))
		title := "A long conversation title " + string(rune('A'+i))
		a.scope().sessions = append(a.scope().sessions, opencode.Session{ID: id, Title: title})
		a.scope().tabs = append(a.scope().tabs, chatTab{id: id, title: title})
	}
	a.conversation.target.selectSession("ses_f")
	a.conversation.selected = "A long conversation title F"
	a.scope().activeTab = "ses_f"
	tt := ui.NewTester(a.view, 1120, 700)
	tt.Frame()
	tt.Frame()
	if a.layout.tabScroll.MaxX <= 0 {
		t.Fatalf("the strip does not overflow: %+v", a.layout.tabScroll)
	}
	if _, ok := tt.Find("Scroll tabs right"); !ok {
		t.Fatalf("the strip has no scroll control: %v", tt.Texts())
	}
	if err := tt.Click("Scroll tabs right"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	after := a.layout.tabScroll.X
	if after <= 0 {
		t.Fatalf("the strip did not scroll: %+v", a.layout.tabScroll)
	}
	a.revealTab("ses_a")
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if a.layout.tabScroll.X >= after {
		t.Fatalf("revealing the first tab left the strip at %v (was %v)", a.layout.tabScroll.X, after)
	}
}
