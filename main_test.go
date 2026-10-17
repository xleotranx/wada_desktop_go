package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The view runs without a window in tests, which click and type as a user
// would.
func TestView(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)

	// The interface starts clean: an empty transcript waiting for the
	// boot, the search field over the store and no demo furniture.
	if a.selected != "" || len(a.blocks) != 0 {
		t.Fatalf("initial state: selected %q, %d blocks", a.selected, len(a.blocks))
	}
	if !tt.HasText("What should we work on?") || !tt.HasText("Recents") {
		t.Fatalf("bare interface missing: %v", tt.Texts())
	}
	if !tt.HasText("Search chats") {
		t.Fatalf("search field missing: %v", tt.Texts())
	}
	for _, gone := range []string{"Pinned", "Projects", "Homework",
		"Compare OpenCode dev bot options", "wada_pi_webapp", "Nightly digest job"} {
		if tt.HasText(gone) {
			t.Fatalf("demo furniture %q still shown: %v", gone, tt.Texts())
		}
	}
}

// TestSidebarSearchCentered keeps the search row on the card's midline:
// the card is a column, so centering it vertically is Justify's doing,
// not AlignItems'.
func TestSidebarSearchCentered(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)

	row, ok := tt.Find("New chat")
	if !ok {
		t.Fatalf("the New chat row is missing: %v", tt.Texts())
	}
	in, ok := tt.Find("Search chats")
	if !ok {
		t.Fatalf("the search field is missing: %v", tt.Texts())
	}
	// The search card follows the New chat row across the list's two 2px
	// gaps and the 6px spacer; the field centers on the card's midline.
	cardTop := row.Y + row.H + 2 + 6 + 2
	if got, want := in.Y+in.H/2, cardTop+15; got < want-1 || got > want+1 {
		t.Fatalf("the search field centers on %v, want the card midline %v", got, want)
	}
}

// TestSidebarSearch drives the search field: typing queues a store
// lookup, the results take the list over, choosing one opens the chat
// and a blank field hands back to the ordinary list.
func TestSidebarSearch(t *testing.T) {
	a := newApp()
	a.sessionsLoaded = true
	a.sessions = []agentSession{{id: "ses_a", title: "Grep wiring"}}
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Search chats"); err != nil {
		t.Fatalf("focusing the search field: %v", err)
	}
	tt.Type("wire")
	if a.searchText != "wire" {
		t.Fatalf("search text %q after typing", a.searchText)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "search" || job.text != "wire" {
			t.Fatalf("search job %+v", job)
		}
	default:
		t.Fatal("typing did not queue a search job")
	}
	if !a.searchLoading {
		t.Fatal("the lookup does not show as running")
	}

	// The results take the list over while the query stands.
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Results") || !tt.HasText("Searching chats") {
		t.Fatalf("search state not shown: %v", tt.Texts())
	}
	a.searchLoading = false
	a.searchResults = []agentSession{{id: "ses_b", title: "Wiring the wings"}}
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Wiring the wings") || tt.HasText("Grep wiring") {
		t.Fatalf("results not shown in place of the list: %v", tt.Texts())
	}
	if err := tt.Click("Wiring the wings"); err != nil {
		t.Fatal(err)
	}
	if a.selected != "Wiring the wings" || a.selectedSession != "ses_b" {
		t.Fatalf("result not opened: %q %q", a.selected, a.selectedSession)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "open" || job.sid != "ses_b" {
			t.Fatalf("open job %+v", job)
		}
	default:
		t.Fatal("opening a result did not queue a job")
	}

	// Clearing the field hands the list back at once.
	a.searchText = ""
	a.searchResults = nil
	a.reviseSearch()
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Grep wiring") || tt.HasText("Results") {
		t.Fatalf("clearing did not restore the list: %v", tt.Texts())
	}
	select {
	case <-a.jobs:
		t.Fatal("a blank field queued a lookup")
	default:
	}
}

// TestHeaderWiring drives the header shapes: the bell counts the
// approvals waiting and takes the user to one, and the search chip aims
// the field the way the product menu does.
func TestHeaderWiring(t *testing.T) {
	a := newApp()
	a.selectedSession = "ses_seed"
	a.selected = "Wired turn"
	a.selectedSession = "ses_z"
	a.blocks = []block{{kind: "text", text: "half a thought"}}
	tt := ui.NewTester(a.view, 1400, 920)

	// The search chip aims the field: the ask stands until a frame gives
	// the field the keyboard, then it is gone, one ask at a time.
	a.focusSearch = true
	tt = ui.NewTester(a.view, 1400, 920)
	if a.focusSearch {
		t.Fatal("the field did not take the focus")
	}

	// With an approval waiting, the bell names it and opens its chat.
	a.sessionsLoaded = true
	a.sessions = []agentSession{{id: "ses_z", title: "Wired turn"}}
	a.pending["per_5"] = &agentPerm{id: "per_5", session: "ses_z", action: "shell"}
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("1") || !tt.HasText("Notifications, 1 approvals waiting") {
		t.Fatalf("bell badge missing: %v", tt.Texts())
	}
	if err := tt.Click("Notifications, 1 approvals waiting"); err != nil {
		t.Fatal(err)
	}
	if a.selectedSession != "ses_z" || a.selected != "Wired turn" {
		t.Fatalf("bell click landed on %q %q", a.selectedSession, a.selected)
	}
	select {
	case job := <-a.jobs:
		if job.kind != "open" || job.sid != "ses_z" {
			t.Fatalf("bell job %+v", job)
		}
	default:
		t.Fatal("the bell did not queue the open")
	}

	// The quiet bell, nothing pending, shows no badge.
	a.pending = map[string]*agentPerm{}
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Notifications") || tt.HasText("approvals waiting") {
		t.Fatalf("badge stuck: %v", tt.Texts())
	}
}

// TestAttachDialog drives the composer's attach flow: the + opens the
// dialog, typing a path quotes the file and the drawer closes.
func TestAttachDialog(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "notes.md"), []byte("# hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	a.sessionsLoaded = true
	a.sessions = []agentSession{{id: "ses_z", title: "Turn", dir: tmp}}
	a.selectedSession = "ses_z"
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Attach"); err != nil {
		t.Fatalf("the + did not open the dialog: %v", err)
	}
	if !a.attachOpen {
		t.Fatal("the dialog did not open")
	}
	tt = ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Quote a file") {
		t.Fatalf("dialog missing: %v", tt.Texts())
	}
	a.attachDraft = "notes.md"
	tt = ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Quote"); err != nil {
		t.Fatal(err)
	}
	if a.attachOpen {
		t.Fatal("the dialog stayed open")
	}
	mark := "(file: " + filepath.Join(tmp, "notes.md") + ")"
	if !strings.Contains(a.draft, mark) || !strings.Contains(a.draft, "# hello") {
		t.Fatalf("draft %q", a.draft)
	}
	// The cancelled drawer leaves the draft to itself.
	a.attachOpen = true
	a.attachDraft = ""
	tt = ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if a.attachOpen || strings.Count(a.draft, mark) != 1 {
		t.Fatalf("cancel fail: open %v draft %q", a.attachOpen, a.draft)
	}
}

func TestActivityExpands(t *testing.T) {
	a := newApp()
	a.selectedSession = "ses_seed"
	a.selected = "Wired turn"
	a.blocks = []block{
		{kind: "activity", text: "Ran command", icon: icTerminal, key: "activity:call_1",
			detail: commandDetail(
				"$ echo hi",
				"hi")},
	}
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Ran command"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("$ echo hi") || !tt.HasText("hi") {
		t.Fatalf("expanded commands missing: %v", tt.Texts())
	}
	if err := tt.Click("Ran command"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("$ echo hi") {
		t.Fatal("expanded commands did not close")
	}
}

func TestComposerSends(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.view, 1400, 920)

	// The composer talks even with nothing chosen: the send queues the
	// turn for a chat the runner opens.
	if err := tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Ship the boundary check")
	if a.draft != "Ship the boundary check" {
		t.Fatalf("draft %q after typing", a.draft)
	}
	tt.Key(0, ui.KeyEnter)
	if a.draft != "" {
		t.Fatalf("draft %q after Enter", a.draft)
	}
	if len(a.blocks) == 0 || a.blocks[len(a.blocks)-1].text != "Ship the boundary check" {
		t.Fatalf("last block %v", a.blocks[len(a.blocks)-1])
	}
	if a.blocks[len(a.blocks)-1].kind != "user" {
		t.Fatal("the sent block is not the user's")
	}

	// The round button turns into the send button once anything is typed.
	if err := tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	tt.Type("One more")
	if !tt.HasText("Send message") {
		t.Fatalf("send button not shown; texts %q", tt.Texts())
	}
	if a.draft != "One more" {
		t.Fatalf("draft %q", a.draft)
	}
	if err := tt.Click("Send message"); err != nil {
		t.Fatal(err)
	}
	if a.draft != "" {
		t.Fatalf("draft %q after the send button", a.draft)
	}
	if n := len(a.blocks); n == 0 || a.blocks[n-1].text != "One more" {
		t.Fatalf("last block %v", a.blocks[n-1])
	}
	if tt.HasText("Send message") {
		t.Fatal("send button stayed after sending")
	}
}
