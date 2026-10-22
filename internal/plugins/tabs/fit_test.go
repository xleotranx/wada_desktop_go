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

// many is n tabs in each of the folders, in order.
func many(n int, folders ...string) []tabs.Tab {
	var out []tabs.Tab
	for _, f := range folders {
		for i := range n {
			out = append(out, tabs.Tab{ID: fmt.Sprintf("%s#%d", f, i), Kind: tabs.Conversation, Folder: f, Title: fmt.Sprintf("%s %d", f, i)})
		}
	}
	return out
}

// drawn sums up a plan: each group as "folder:shown/all", collapsed ones
// marked with a star, then the hidden tabs' count.
func drawn(s stripPlan) []string {
	var out []string
	for _, g := range s.groups {
		mark := ""
		if g.collapsed {
			mark = "*"
		}
		out = append(out, fmt.Sprintf("%s%s:%d/%d", g.folder, mark, len(g.shown), len(g.tabs)))
	}
	return append(out, fmt.Sprintf("hidden %d", len(s.hidden)))
}

func TestFitKeepsEverythingWhileThereIsRoom(t *testing.T) {
	list := many(3, "/w/a", "/w/b")
	for _, width := range []float32{0, 2000} {
		if got := drawn(fit(list, "/w/a#0", nil, width)); !slices.Equal(got, []string{"/w/a:3/3", "/w/b:3/3", "hidden 0"}) {
			t.Fatalf("width %v: %q", width, got)
		}
	}
	// The user's own collapse holds at any width.
	if got := drawn(fit(list, "/w/a#0", map[string]bool{"/w/b": true}, 0)); !slices.Equal(got, []string{"/w/a:3/3", "/w/b*:0/3", "hidden 0"}) {
		t.Fatalf("collapsed by the user: %q", got)
	}
}

func TestFitCollapsesTheFarthestGroupsFirst(t *testing.T) {
	list := many(4, "/w/a", "/w/b", "/w/c", "/w/d")
	// Room for the active group's four tabs, one more group and three labels.
	plan := fit(list, "/w/a#1", nil, 4*tabMinWidth+4*tabMinWidth+3*groupGap+3*60)
	if got := drawn(plan); !slices.Equal(got, []string{"/w/a:4/4", "/w/b:4/4", "/w/c*:0/4", "/w/d*:0/4", "hidden 0"}) {
		t.Fatalf("plan %q (need %v)", got, plan.need())
	}
}

func TestFitMovesTabsIntoTheOverflowButNeverTheActiveOne(t *testing.T) {
	list := many(12, "")
	plan := fit(list, "#11", nil, 4*tabMinWidth+overflowWidth)
	if got := drawn(plan); !slices.Equal(got, []string{":4/12", "hidden 8"}) {
		t.Fatalf("plan %q", got)
	}
	if !slices.ContainsFunc(plan.groups[0].shown, func(t tabs.Tab) bool { return t.ID == "#11" }) {
		t.Fatal("the active tab went into the overflow")
	}
	if plan.need() > 4*tabMinWidth+overflowWidth {
		t.Fatalf("the plan needs %v", plan.need())
	}
}

func TestANarrowStripStillReachesEveryTab(t *testing.T) {
	ws := &plugintest.Workspace{}
	for i, folder := range []string{"/w/app", "/w/web", "/w/docs"} {
		ws.FolderSet = append(ws.FolderSet, workspace.Folder{Path: folder})
		for j := range 4 {
			ws.Convs = append(ws.Convs, workspace.Conversation{ID: agentID(i*4 + j), Title: fmt.Sprintf("Chat %d", i*4+j), Folder: folder})
		}
	}
	f := start(t, nil, ws)
	for i := range 12 {
		f.open(agentID(i))
		if i < 11 {
			f.chat.NewChat("")
		}
	}
	tt := ui.NewTester(f.p.strip, 700, 50)
	tt.Frame()
	tt.Frame() // fitted to the width the first frame measured
	if tt.HasText("Chat 0") || !tt.HasText("Chat 11") || !tt.HasText("app · 4") {
		t.Fatalf("strip %q", tt.Texts())
	}
	if err := tt.Click("Project app"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Menu(); len(got) < 4 || got[0] != "Chat 0" {
		t.Fatalf("the label's menu %q", got)
	}
	if err := tt.ChooseMenuItem("Chat 2"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	f.h.Flush()
	if a, _ := f.p.Active(); a.Title != "Chat 2" {
		t.Fatalf("active %q", a.Title)
	}
}

func agentID(i int) agent.SessionID { return agent.SessionID(fmt.Sprintf("c%d", i)) }
