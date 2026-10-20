package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestChatTabsOpenSwitchAndClose keeps several conversations one press
// apart: each opened chat joins the strip, its title opens it, and its
// cross closes it, handing the view to the neighbouring tab or a draft.
func TestChatTabsOpenSwitchAndClose(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}
	a.openConversation(a.scope().sessions[0])
	a.openConversation(a.scope().sessions[1])
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()

	if got := len(a.scope().tabs); got != 2 {
		t.Fatalf("opened %d tabs, want 2", got)
	}
	if !tt.HasText("Alpha") || !tt.HasText("Beta") {
		t.Fatalf("the strip is missing a title: %v", tt.Texts())
	}
	if err := tt.Click("Open Alpha"); err != nil {
		t.Fatal(err)
	}
	if got := string(a.conversation.target.session); got != "ses_alpha" {
		t.Fatalf("the Alpha tab opened %q", got)
	}

	if err := tt.Click("Close Alpha"); err != nil {
		t.Fatal(err)
	}
	if got := string(a.conversation.target.session); got != "ses_beta" {
		t.Fatalf("closing the open tab did not fall back to Beta: %q", got)
	}
	if got := len(a.scope().tabs); got != 1 {
		t.Fatalf("closing left %d tabs", got)
	}

	// The last tab's strip folds with it; closing the tab itself still
	// hands the view to a fresh draft.
	a.closeTab("ses_beta")
	if !a.conversation.target.isDraft() || len(a.scope().tabs) != 0 {
		t.Fatalf("closing the last tab left target=%q tabs=%d",
			a.conversation.target.session, len(a.scope().tabs))
	}
}

// TestChatTabsCycle moves through the strip with the keyboard, wrapping
// from the last tab to the first and back the other way.
func TestChatTabsCycle(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.adoptSession(a.scope().sessions[1])
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Ctrl, ui.KeyTab)
	tt.Frame()
	if got := string(a.conversation.target.session); got != "ses_alpha" {
		t.Fatalf("Ctrl+Tab landed on %q", got)
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	tt.Frame()
	if got := string(a.conversation.target.session); got != "ses_beta" {
		t.Fatalf("Ctrl+Shift+Tab landed on %q", got)
	}
}

// TestChatTabsKeepTheNewest bounds the strip: opening more chats than fit
// drops the oldest tab instead of growing the row without limit.
func TestChatTabsKeepTheNewest(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	for i := range chatTabLimit + 3 {
		s := opencode.Session{ID: fmt.Sprintf("ses_%02d", i), Title: fmt.Sprintf("Chat %02d", i)}
		a.scope().sessions = append(a.scope().sessions, s)
		a.adoptSession(s)
	}
	tabs := a.scope().tabs
	if len(tabs) != chatTabLimit {
		t.Fatalf("the strip grew to %d tabs", len(tabs))
	}
	if tabs[0].id != "ses_03" || tabs[len(tabs)-1].id != "ses_10" {
		t.Fatalf("the strip kept %q … %q", tabs[0].id, tabs[len(tabs)-1].id)
	}
}

// TestChatTabsStartADraft keeps the strip while a draft is open, so the
// plus does not lose the conversations it sits beside.
func TestChatTabsStartADraft(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.adoptSession(a.scope().sessions[1])
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("New chat tab"); err != nil {
		t.Fatal(err)
	}
	if !a.conversation.target.isDraft() {
		t.Fatal("the plus did not start a draft")
	}
	if len(a.scope().tabs) != 2 || !tt.HasText("Alpha") || !tt.HasText("Beta") {
		t.Fatalf("the draft dropped the strip: tabs=%d", len(a.scope().tabs))
	}
}

// TestChatTabsSurviveRestart writes the strip and its active tab through
// the metadata writer and restores them into a second app.
func TestChatTabsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newTestApp(t)
	db, err := a.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	a.preferences.metadata = startMetadataWriter(db, func(uint64, error) {})
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.adoptSession(a.scope().sessions[1])
	a.closeMetadata()

	b := newTestApp(t)
	db, err = b.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	scope := b.scopeFor(localServerID)
	if len(scope.tabs) != 2 || scope.tabs[0].id != "ses_alpha" || scope.tabs[1].title != "Beta" {
		t.Fatalf("restored tabs: %+v", scope.tabs)
	}
	if scope.activeTab != "ses_beta" {
		t.Fatalf("restored active tab %q", scope.activeTab)
	}
}

// TestBootRestoresTheLastTab opens the conversation that was on show
// before the restart instead of the newest one.
func TestBootRestoresTheLastTab(t *testing.T) {
	a := newTestApp(t)
	a.scope().tabs = []chatTab{
		{id: "ses_alpha", title: "Alpha"},
		{id: "ses_beta", title: "Beta"},
	}
	a.scope().activeTab = "ses_beta"
	a.settleBoot([]opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}, "", nil)
	if got := string(a.conversation.target.session); got != "ses_beta" {
		t.Fatalf("boot opened %q, want the last tab", got)
	}
}

// TestReopenClosedTab brings back the last tab a cross removed with
// Cmd+Shift+T, and keeps a deleted conversation out of that history.
func TestReopenClosedTab(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{
		{ID: "ses_alpha", Title: "Alpha"},
		{ID: "ses_beta", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.adoptSession(a.scope().sessions[1])
	a.closeTab("ses_alpha")
	if len(a.scope().closedTabs) != 1 {
		t.Fatalf("closed history: %+v", a.scope().closedTabs)
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyT)
	tt.Frame()
	if got := string(a.conversation.target.session); got != "ses_alpha" {
		t.Fatalf("Cmd+Shift+T reopened %q", got)
	}
	if len(a.scope().tabs) != 2 || len(a.scope().closedTabs) != 0 {
		t.Fatalf("reopened strip: tabs=%d closed=%d", len(a.scope().tabs), len(a.scope().closedTabs))
	}

	a.dropSession("ses_alpha")
	if len(a.scope().closedTabs) != 0 {
		t.Fatalf("a deleted conversation entered closed history: %+v", a.scope().closedTabs)
	}
}

// TestTabContextMenuClosesTabs closes the other tabs, then all of them,
// from one tab's context menu, keeping the closed ones for reopening.
func TestTabContextMenuClosesTabs(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Alpha"},
		{ID: "ses_b", Title: "Beta"},
		{ID: "ses_c", Title: "Gamma"},
	}
	a.scope().sessionsLoaded = true
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Alpha"
	a.scope().tabs = []chatTab{
		{id: "ses_a", title: "Alpha"},
		{id: "ses_b", title: "Beta"},
		{id: "ses_c", title: "Gamma"},
	}
	a.scope().activeTab = "ses_a"
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.RightClick("Open Alpha"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tt.Menu(), "Close other tabs") || !slices.Contains(tt.Menu(), "Close all tabs") {
		t.Fatalf("the tab menu reads %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Close other tabs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.scope().tabs) != 1 || a.scope().tabs[0].id != "ses_a" {
		t.Fatalf("the strip kept %+v", a.scope().tabs)
	}
	if len(a.scope().closedTabs) != 2 ||
		a.scope().closedTabs[0].id != "ses_b" || a.scope().closedTabs[1].id != "ses_c" {
		t.Fatalf("the reopen history is %+v", a.scope().closedTabs)
	}
	if a.conversation.target.session != "ses_a" {
		t.Fatalf("the kept tab is not on show: %q", a.conversation.target.session)
	}

	// The strip hides with one tab; seed a second one to reach the menu
	// again, then close the whole strip.
	a.scope().tabs = []chatTab{{id: "ses_a", title: "Alpha"}, {id: "ses_b", title: "Beta"}}
	a.scope().activeTab = "ses_a"
	tt.Frame()
	if err := tt.RightClick("Open Alpha"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Close all tabs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.scope().tabs) != 0 || !a.conversation.target.isDraft() {
		t.Fatalf("closing all left tabs %+v, view %q", a.scope().tabs, a.conversation.target.session)
	}
}

// TestCloseTabCommands runs the palette's close-tab and close-other-tabs
// actions against the open conversation.
func TestCloseTabCommands(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Alpha"},
		{ID: "ses_b", Title: "Beta"},
	}
	a.scope().sessionsLoaded = true
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Alpha"
	a.scope().tabs = []chatTab{{id: "ses_a", title: "Alpha"}, {id: "ses_b", title: "Beta"}}
	a.scope().activeTab = "ses_a"
	a.runCommand("action:close-other-tabs")
	if len(a.scope().tabs) != 1 || a.scope().tabs[0].id != "ses_a" {
		t.Fatalf("close-other-tabs left %+v", a.scope().tabs)
	}
	a.runCommand("action:close-tab")
	if len(a.scope().tabs) != 0 || !a.conversation.target.isDraft() {
		t.Fatalf("close-tab left %+v, view %q", a.scope().tabs, a.conversation.target.session)
	}
}

// TestTabDragReorders drags the first tab over the last one and expects it
// to take that slot, with the conversation on show unchanged.
func TestTabDragReorders(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Alpha"},
		{ID: "ses_b", Title: "Beta"},
		{ID: "ses_c", Title: "Gamma"},
	}
	a.scope().sessionsLoaded = true
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Alpha"
	a.scope().tabs = []chatTab{
		{id: "ses_a", title: "Alpha"},
		{id: "ses_b", title: "Beta"},
		{id: "ses_c", title: "Gamma"},
	}
	a.scope().activeTab = "ses_a"
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	src, ok := tt.Find("Open Alpha")
	if !ok {
		t.Fatal("the first tab is not drawn")
	}
	dst, ok := tt.Find("Open Gamma")
	if !ok {
		t.Fatal("the last tab is not drawn")
	}
	tt.Press(src.X+src.W/2, src.Y+src.H/2)
	tt.Move(src.X+src.W/2+12, src.Y+src.H/2)
	tt.Move(dst.X+dst.W/2, dst.Y+dst.H/2)
	tt.Release(dst.X+dst.W/2, dst.Y+dst.H/2)
	tt.Frame()
	ids := []string{}
	for _, tab := range a.scope().tabs {
		ids = append(ids, tab.id)
	}
	if !slices.Equal(ids, []string{"ses_b", "ses_c", "ses_a"}) {
		t.Fatalf("the dragged strip reads %v", ids)
	}
	if a.conversation.target.session != "ses_a" {
		t.Fatalf("the drag moved the view to %q", a.conversation.target.session)
	}
}

// TestMoveTabBackwards puts a tab back over its neighbour, and leaves a
// miss alone.
func TestMoveTabBackwards(t *testing.T) {
	a := newTestApp(t)
	a.scope().tabs = []chatTab{
		{id: "ses_a", title: "Alpha"},
		{id: "ses_b", title: "Beta"},
		{id: "ses_c", title: "Gamma"},
	}
	a.moveTabTo("ses_c", "ses_a")
	ids := []string{}
	for _, tab := range a.scope().tabs {
		ids = append(ids, tab.id)
	}
	if !slices.Equal(ids, []string{"ses_c", "ses_a", "ses_b"}) {
		t.Fatalf("backwards drag reads %v", ids)
	}
	a.moveTabTo("ses_c", "missing")
	a.moveTabTo("missing", "ses_a")
	if len(a.scope().tabs) != 3 || a.scope().tabs[0].id != "ses_c" {
		t.Fatalf("a miss moved the strip: %+v", a.scope().tabs)
	}
}
