package main

import (
	"slices"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestPinnedChatDragReorders drags the first pinned chat over the last and
// expects it to take that slot, the order being what the sidebar saves.
func TestPinnedChatDragReorders(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Alpha chat"},
		{ID: "ses_b", Title: "Beta chat"},
		{ID: "ses_c", Title: "Gamma chat"},
	}
	a.scope().sessionsLoaded = true
	a.scope().setPins([]string{"ses_a", "ses_b", "ses_c"})
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	src, ok := tt.Find("Alpha chat")
	if !ok {
		t.Fatal("the first pinned chat is not drawn")
	}
	dst, ok := tt.Find("Gamma chat")
	if !ok {
		t.Fatal("the last pinned chat is not drawn")
	}
	tt.Press(src.X+src.W/2, src.Y+src.H/2)
	tt.Move(src.X+src.W/2+12, src.Y+src.H/2)
	tt.Move(dst.X+dst.W/2, dst.Y+dst.H/2)
	tt.Release(dst.X+dst.W/2, dst.Y+dst.H/2)
	tt.Frame()
	if !slices.Equal(a.scope().pinOrder, []string{"ses_b", "ses_c", "ses_a"}) {
		t.Fatalf("the dragged pins read %v", a.scope().pinOrder)
	}
}

// TestMovePinBackwards puts a pin back over its neighbour, and leaves a
// miss alone.
func TestMovePinBackwards(t *testing.T) {
	a := newTestApp(t)
	a.scope().setPins([]string{"ses_a", "ses_b", "ses_c"})
	a.movePinTo("ses_c", "ses_a")
	if !slices.Equal(a.scope().pinOrder, []string{"ses_c", "ses_a", "ses_b"}) {
		t.Fatalf("backwards drag reads %v", a.scope().pinOrder)
	}
	a.movePinTo("ses_c", "missing")
	a.movePinTo("missing", "ses_a")
	if !slices.Equal(a.scope().pinOrder, []string{"ses_c", "ses_a", "ses_b"}) {
		t.Fatalf("a miss moved the pins: %v", a.scope().pinOrder)
	}
}
