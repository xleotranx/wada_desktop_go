package tabs

import (
	"fmt"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

func many(n int) []tabs.Tab {
	out := make([]tabs.Tab, n)
	for i := range out {
		out[i] = tabs.Tab{ID: fmt.Sprintf("#%d", i), Kind: tabs.Conversation, Title: fmt.Sprintf("Chat %d", i)}
	}
	return out
}

func TestFitKeepsEveryTabWhileThereIsRoom(t *testing.T) {
	for _, width := range []float32{0, 12 * tabMinWidth} {
		if shown, hidden := fit(many(12), "#0", width); len(shown) != 12 || hidden != nil {
			t.Fatalf("width %v: %d shown, %d hidden", width, len(shown), len(hidden))
		}
	}
}

func TestFitMovesTabsIntoTheOverflowButNeverTheActiveOne(t *testing.T) {
	width := float32(4*tabMinWidth + overflowWidth)
	shown, hidden := fit(many(12), "#11", width)
	if len(shown) != 4 || len(hidden) != 8 || shown[3].ID != "#11" || hidden[0].ID != "#3" {
		t.Fatalf("shown %v hidden %v", shown, hidden)
	}
	if !slices.IsSortedFunc(append(slices.Clone(shown[:3]), hidden...), func(a, b tabs.Tab) int { return len(a.ID) - len(b.ID) }) {
		t.Fatal("the order changed")
	}
}

func agentID(i int) agent.SessionID { return agent.SessionID(fmt.Sprintf("c%d", i)) }

func TestANarrowStripStillReachesEveryTab(t *testing.T) {
	ws := &plugintest.Workspace{FolderSet: []workspace.Folder{{Path: "/w/app"}}}
	for i := range 12 {
		ws.Convs = append(ws.Convs, workspace.Conversation{ID: agentID(i), Title: fmt.Sprintf("Chat %d", i), Folder: "/w/app"})
	}
	f := start(t, nil, ws)
	for i := range 12 {
		f.open(agentID(i))
		if i < 11 {
			f.chat.NewChat("/w/app")
		}
	}
	tt := ui.NewTester(f.p.strip, 700, 50)
	tt.Frame()
	tt.Frame() // fitted to the width the first frame measured
	if tt.HasText("Chat 11") == false || tt.HasText("Chat 10") {
		t.Fatalf("strip %q", tt.Texts())
	}
	if err := tt.Click("More tabs"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Chat 10"); err != nil {
		t.Fatalf("%v; menu %q", err, tt.Menu())
	}
	tt.Frame()
	f.h.Flush()
	if a, _ := f.p.Active(); a.Title != "Chat 10" {
		t.Fatalf("active %q", a.Title)
	}
}
