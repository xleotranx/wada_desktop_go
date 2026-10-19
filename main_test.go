package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// The view runs without a window in tests, which click and type as a user
// would.
func TestView(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)

	// The interface starts clean, with Codex navigation and workspace
	// tools around an empty transcript waiting for the boot.
	if a.conversation.selected != "" || a.conversation.transcript.Len() != 0 {
		t.Fatalf("initial state: selected %q, %d blocks", a.conversation.selected, a.conversation.transcript.Len())
	}
	if !tt.HasText("What should we work on?") || !tt.HasText("Codex") ||
		!tt.HasText("Pinned") || !tt.HasText("Projects") || tt.HasText("Recents") ||
		!tt.HasText("Tools") {
		t.Fatalf("bare interface missing: %v", tt.Texts())
	}
	// The tools and browser pane waits for the rail's tools button: a
	// fresh window is conversation only, and the button brings it back.
	if tt.HasText("New page") {
		t.Fatalf("the right pane started open: %v", tt.Texts())
	}
	if err := tt.Click("Tools"); err != nil {
		t.Fatalf("opening the right pane: %v", err)
	}
	if !tt.HasText("New page") {
		t.Fatalf("the right pane did not open: %v", tt.Texts())
	}
	for _, gone := range []string{"Homework",
		"Compare OpenCode dev bot options", "Nightly digest job"} {
		if tt.HasText(gone) {
			t.Fatalf("demo furniture %q still shown: %v", gone, tt.Texts())
		}
	}
}

// TestChromeWash keeps the chrome riding the window's material readable:
// the title bar and rail lay a translucent wash over it, warm and near
// white in the light appearance, so no wallpaper shows through the chrome
// bare.
func TestChromeWash(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.SetVibrancy(true)

	// The pixels are premultiplied: the light wash is warm white at its own
	// alpha, down the title bar and along the rail beside it.
	for _, at := range [][2]int{{900, 20}, {47, 400}} {
		c := tt.Image().RGBAAt(at[0], at[1])
		if c.A == 0 || c.A == 255 {
			t.Fatalf("the chrome at %v is not a wash: %v", at, c)
		}
		near := func(v, a uint8) bool { return int(v) >= int(a)*9/10 && v <= a }
		if !near(c.R, c.A) || !near(c.G, c.A) || !near(c.B, c.A) {
			t.Fatalf("the chrome at %v is not the light wash: %v", at, c)
		}
	}

	// The dark appearance washes the other way: a translucent near-black.
	tt.SetDark(true)
	if c := tt.Image().RGBAAt(900, 20); c.A == 0 || c.A == 255 || c.R > 48 || c.G > 48 || c.B > 48 {
		t.Fatalf("the dark chrome is not a dark wash: %v", c)
	}
}

// TestTopBarChrome keeps the title strip shaped like the reference: the
// conversation's title stands over it, clear of the sidebar, and the
// tools toggle at its end brings the right pane in and out.
func TestTopBarChrome(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{{ID: "ses_z", Title: "Plan project folder metadata storage"}}
	a.adoptSession(a.scope().sessions[0])
	tt := ui.NewTester(a.view, 1400, 920)

	title, ok := tt.Find("Plan project folder metadata storage")
	if !ok {
		t.Fatalf("the strip has no title: %v", tt.Texts())
	}
	// It starts where the conversation does, past the rail and sidebar,
	// and stands inside the strip.
	if past := navRailWidth + a.layout.sidebarW; title.X < past || title.X > past+24 {
		t.Fatalf("the title starts at %v, want just past the sidebar at %v", title.X, past)
	}
	if title.Y >= 48 {
		t.Fatalf("the title stands outside the strip at %v", title.Y)
	}

	// The toggle at the end brings the right pane in and out.
	if err := tt.Click("Show tools"); err != nil {
		t.Fatalf("the tools toggle: %v", err)
	}
	if a.layout.browserCollapsed || !tt.HasText("New page") {
		t.Fatal("the tools toggle did not bring the right pane in")
	}
	if err := tt.Click("Hide tools"); err != nil {
		t.Fatalf("the tools toggle: %v", err)
	}
	if !a.layout.browserCollapsed || tt.HasText("New page") {
		t.Fatal("the tools toggle did not fold the right pane away")
	}

	// Folded away, the sidebar hands the title back to the controls.
	wide := title.X
	if err := tt.Click("Hide sidebar"); err != nil {
		t.Fatalf("the sidebar toggle: %v", err)
	}
	if folded, ok := tt.Find("Plan project folder metadata storage"); !ok || folded.X >= wide {
		t.Fatalf("the title did not follow the sidebar in: %+v", folded)
	}
}

// TestStreamFollowsFeed keeps a streaming answer in view: at the foot the
// transcript pins to the growing answer, and a reader who scrolled up
// during it stays where they put the view.
func TestStreamFollowsFeed(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_z")
	a.conversation.selected = "Turn"
	a.conversation.transcript.Start("ses_z")
	for i := range 80 {
		a.conversation.transcript.Append(chat.Block{Kind: "text", Text: fmt.Sprintf("line %d", i)})
	}

	// A conversation opens at its foot, then follows newly appended rows.
	tt := ui.NewTester(a.view, 1400, 920)
	if !a.layout.feed.AtEnd() {
		t.Fatal("the transcript did not open at its end")
	}
	for i := range 40 {
		a.conversation.transcript.Append(chat.Block{Kind: "stream", Text: fmt.Sprintf("more %d", i)})
	}
	tt.Frame()                  // the grown content starts the glide
	tt.Advance(2 * time.Second) // which lands at the new foot
	if !a.layout.feed.AtEnd() || !tt.HasText("more 39") {
		t.Fatal("the feed did not follow the stream")
	}

	// A reader who scrolled up keeps their place while the answer grows.
	tt.Scroll(1000, 300, 0, -10000)
	first, _ := a.layout.feed.Visible()
	if a.layout.feed.AtEnd() || first != 0 {
		t.Fatalf("scrolling up did not leave the foot: first %d", first)
	}
	for i := range 40 {
		a.conversation.transcript.Append(chat.Block{Kind: "stream", Text: fmt.Sprintf("tail %d", i)})
	}
	tt.Frame()
	tt.Advance(2 * time.Second)
	if now, _ := a.layout.feed.Visible(); now != first || a.layout.feed.AtEnd() {
		t.Fatalf("the feed dragged the reader from row %d to %d", first, now)
	}
	if tt.HasText("tail 39") {
		t.Fatal("a row outside the viewport was built")
	}
}

// TestStreamPinsInLineSteps keeps the transcript still while the answer
// fills its current line: without a glide, the feed moves only when a
// line wraps, not on every word.
func TestStreamPinsInLineSteps(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_z")
	a.conversation.selected = "Turn"
	a.conversation.transcript.Start("ses_z")
	for i := range 80 {
		a.conversation.transcript.Append(chat.Block{Kind: chat.KindText, Text: fmt.Sprintf("line %d", i)})
	}
	a.conversation.transcript.Append(chat.Block{Kind: chat.KindStream, Text: "start", Key: "stream:m:0", Message: "m"})
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Advance(time.Second) // the words pace in

	before, ok := tt.Find("line 79")
	if !ok {
		t.Fatalf("the row above the answer is not in view: %v", tt.Texts())
	}
	// Words that stay on the answer's first line leave the feed alone.
	setRowText(a, a.conversation.transcript.Len()-1, "start with a few more words")
	for range 20 {
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
		if now, _ := tt.Find("line 79"); now.Y != before.Y {
			t.Fatalf("the feed moved while the line filled: %0.1f to %0.1f", before.Y, now.Y)
		}
	}
	// A wrapped line pins the bottom at once, lifting the rows above by
	// that line rather than gliding over the next half second.
	setRowText(a, a.conversation.transcript.Len()-1, strings.Repeat("word ", 40))
	tt.Frame()
	tt.Advance(time.Second)
	if after, _ := tt.Find("line 79"); after.Y >= before.Y {
		t.Fatalf("the wrapped line did not move the feed: %0.1f to %0.1f", before.Y, after.Y)
	}
}

// TestStreamMarkers keeps half-arrived markers out of the view: a
// trailing line that could still turn into something else waits for what
// follows it, half-written syntax reads as what it will become, and the
// words pace in rather than landing in bursts.
func TestStreamMarkers(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_z")
	a.conversation.selected = "Turn"
	tt := ui.NewTester(a.view, 1400, 920)
	show := func(text string) *ui.Tester {
		seedTranscript(a, []chat.Block{{Kind: "stream", Text: text}})
		tt.Frame()
		tt.Advance(time.Second) // the words pace in
		return tt
	}
	// drew reports a text drawn whole: a piece of its own, not a dash or
	// dot of some other label.
	drew := func(tt *ui.Tester, s string) bool {
		_, ok := tt.Find(s)
		return ok
	}

	if tt := show("intro\n\n#"); drew(tt, "#") {
		t.Fatalf("a bare heading marker drew: %v", tt.Texts())
	}
	if tt := show("intro\n\n-"); drew(tt, "-") {
		t.Fatalf("a bare bullet drew: %v", tt.Texts())
	}
	if tt := show("intro\n\n``"); drew(tt, "``") {
		t.Fatalf("a bare fence drew: %v", tt.Texts())
	}

	// Decided, they draw as what they are.
	if tt := show("intro\n\n# Title"); !tt.HasText("Title") || drew(tt, "# Title") {
		t.Fatalf("the heading did not draw: %v", tt.Texts())
	}
	if tt := show("intro\n\n- item"); !drew(tt, "•") || !tt.HasText("item") || drew(tt, "- item") {
		t.Fatalf("the bullet did not draw: %v", tt.Texts())
	}
	if tt := show("intro\n\n1. item"); !drew(tt, "1.") || !tt.HasText("item") {
		t.Fatalf("the numbered item did not draw: %v", tt.Texts())
	}
	if tt := show("intro\n\n```go\ncode"); !tt.HasText("Code, go") {
		t.Fatalf("the fence did not draw: %v", tt.Texts())
	}

	// Half-written syntax reads as what it will become; the markers stay
	// unshown.
	if tt := show("run `go test"); !tt.HasText("run go test") || tt.HasText("`go test") {
		t.Fatalf("the open code run: %v", tt.Texts())
	}
	if tt := show("this is **the fix"); !tt.HasText("this is the fix") || tt.HasText("**") {
		t.Fatalf("the open bold run: %v", tt.Texts())
	}
	if tt := show("see [the docs](https://ex"); !tt.HasText("the docs") || tt.HasText("https://ex") {
		t.Fatalf("the open link: %v", tt.Texts())
	}
}

// TestTranscriptLoader keeps the working spinner at the transcript's
// foot while the open chat's turn runs with nothing being said: before
// the first words, between them while a tool works, never while an
// approval waits on the user, and never for another chat's turn.
func TestTranscriptLoader(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_z")
	a.conversation.selected = "Turn"
	a.conversation.transcript.Start("ses_z")
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Working…") {
		t.Fatalf("the loader is missing: %v", tt.Texts())
	}

	// Words landing take the loader's place, and an empty stream row --
	// the next paragraph starting -- brings it back.
	seedTranscript(a, []chat.Block{{Kind: "stream", Text: "half an answer"}})
	tt.Frame()
	tt.Advance(time.Second) // the words pace in
	if tt.HasText("Working…") || !tt.HasText("half an answer") {
		t.Fatalf("wordy stream: %v", tt.Texts())
	}
	a.conversation.transcript.Append(chat.Block{Kind: "stream", Key: "stream:msg:1"})
	tt.Frame()
	if !tt.HasText("Working…") {
		t.Fatalf("a silent stream row drops the loader: %v", tt.Texts())
	}

	// An approval waiting leaves the decisions to the user, not the agent.
	seedTranscript(a, nil)
	a.conversation.transcript.Ask(&chat.Permission{ID: "per_1", Session: "ses_z"})
	tt.Frame()
	if tt.HasText("Working…") {
		t.Fatalf("the loader runs while waiting on the user: %v", tt.Texts())
	}

	// Another chat's turn does not show here.
	a.conversation.transcript.ClearPermissions()
	a.conversation.target.selectSession("ses_other")
	tt.Frame()
	if tt.HasText("Working…") {
		t.Fatalf("another chat's turn showed its loader: %v", tt.Texts())
	}
}

// TestSidebarSearchCentered keeps the search row on the card's midline:
// the card is a column, so centering it vertically is Justify's doing,
// not AlignItems'.
func TestSidebarSearchCentered(t *testing.T) {
	a := newTestApp(t)
	a.search.searchOpen = true
	tt := ui.NewTester(a.view, 1400, 920)

	row, ok := tt.Find("New chat")
	if !ok {
		t.Fatalf("the New chat row is missing: %v", tt.Texts())
	}
	in, ok := tt.Find("Search chats")
	if !ok {
		t.Fatalf("the search field is missing: %v", tt.Texts())
	}
	// The search card follows the New chat row across the list's 2px
	// gap; the field centers on the card's midline.
	cardTop := row.Y + row.H + 2
	if got, want := in.Y+in.H/2, cardTop+15; got < want-1 || got > want+1 {
		t.Fatalf("the search field centers on %v, want the card midline %v", got, want)
	}
}

// TestSidebarSearch drives the search field: typing queues a store
// lookup, the results take the list over, choosing one opens the chat
// and a blank field hands back to projects and pinned chats.
func TestSidebarSearch(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Grep wiring"}}
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Search"); err != nil {
		t.Fatalf("focusing the search field: %v", err)
	}
	tt.Type("wire")
	if a.search.searchText != "wire" {
		t.Fatalf("search text %q after typing", a.search.searchText)
	}
	select {
	case job := <-a.search.searches:
		if job.text != "wire" {
			t.Fatalf("search job %+v", job)
		}
	default:
		t.Fatal("typing did not queue a search job")
	}
	if !a.search.searchLoading {
		t.Fatal("the lookup does not show as running")
	}

	// The results take the list over while the query stands.
	tt.Frame()
	if !tt.HasText("Results") || !tt.HasText("Searching chats") {
		t.Fatalf("search state not shown: %v", tt.Texts())
	}
	a.search.searchLoading = false
	a.search.searchResults = []opencode.Session{{ID: "ses_b", Title: "Wiring the wings"}}
	tt.Frame()
	if !tt.HasText("Wiring the wings") || tt.HasText("Grep wiring") {
		t.Fatalf("results not shown in place of the list: %v", tt.Texts())
	}
	if err := tt.Click("Wiring the wings"); err != nil {
		t.Fatal(err)
	}
	if a.conversation.selected != "Wiring the wings" || a.conversation.target.session != "ses_b" {
		t.Fatalf("result not opened: %q %q", a.conversation.selected, a.conversation.target.session)
	}
	select {
	case job := <-commandsOf(a):
		if job.kind != "open" || job.sid != "ses_b" {
			t.Fatalf("open job %+v", job)
		}
	default:
		t.Fatal("opening a result did not queue a job")
	}

	// Clearing the field hides results without showing recent chats.
	a.search.searchText = ""
	a.search.searchResults = nil
	a.reviseSearch()
	tt.Frame()
	if tt.HasText("Grep wiring") || tt.HasText("Results") || !tt.HasText("Projects") {
		t.Fatalf("clearing did not restore projects: %v", tt.Texts())
	}
	select {
	case <-commandsOf(a):
		t.Fatal("a blank field queued a lookup")
	default:
	}
}

// TestHeaderWiring drives the header shapes: the bell counts the
// approvals waiting and takes the user to one, and the search chip aims
// the field the way the product menu does.
func TestHeaderWiring(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_seed")
	a.conversation.selected = "Wired turn"
	a.conversation.target.selectSession("ses_z")
	seedTranscript(a, []chat.Block{{Kind: "text", Text: "half a thought"}})
	tt := ui.NewTester(a.view, 1400, 920)

	// The search chip aims the field: the ask stands until a frame gives
	// the field the keyboard, then it is gone, one ask at a time.
	a.search.searchOpen = true
	a.search.focusSearch = true
	tt.Frame()
	if a.search.focusSearch {
		t.Fatal("the field did not take the focus")
	}

	// With an approval waiting, the bell names it and opens its chat.
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{{ID: "ses_z", Title: "Wired turn"}}
	a.conversation.transcript.Ask(&chat.Permission{ID: "per_5", Session: "ses_z", Action: "shell"})
	tt.Frame()
	if !tt.HasText("1") || !tt.HasText("Notifications, 1 approvals waiting") {
		t.Fatalf("bell badge missing: %v", tt.Texts())
	}
	if err := tt.Click("Notifications, 1 approvals waiting"); err != nil {
		t.Fatal(err)
	}
	if a.conversation.target.session != "ses_z" || a.conversation.selected != "Wired turn" {
		t.Fatalf("bell click landed on %q %q", a.conversation.target.session, a.conversation.selected)
	}
	select {
	case job := <-commandsOf(a):
		if job.kind != "open" || job.sid != "ses_z" {
			t.Fatalf("bell job %+v", job)
		}
	default:
		t.Fatal("the bell did not queue the open")
	}

	// The quiet bell, nothing pending, shows no badge.
	a.conversation.transcript.ClearPermissions()
	tt.Frame()
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
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.scope().sessions = []opencode.Session{{ID: "ses_z", Title: "Turn", Directory: tmp}}
	a.conversation.target.selectSession("ses_z")
	tt := ui.NewTester(a.view, 1400, 920)

	if err := tt.Click("Attach"); err != nil {
		t.Fatalf("the + did not open the dialog: %v", err)
	}
	if a.layout.attach == nil {
		t.Fatal("the dialog did not open")
	}
	tt.Frame()
	if !tt.HasText("Quote a file") {
		t.Fatalf("dialog missing: %v", tt.Texts())
	}
	a.layout.attach.draft = "notes.md"
	tt.Frame()
	if err := tt.Click("Quote"); err != nil {
		t.Fatal(err)
	}
	if a.layout.attach != nil {
		t.Fatal("the dialog stayed open")
	}
	mark := "(file: " + filepath.Join(tmp, "notes.md") + ")"
	if !strings.Contains(a.conversation.draft, mark) || !strings.Contains(a.conversation.draft, "# hello") {
		t.Fatalf("draft %q", a.conversation.draft)
	}
	// The cancelled drawer leaves the draft to itself.
	a.layout.attach = &attachState{open: true}
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if a.layout.attach != nil || strings.Count(a.conversation.draft, mark) != 1 {
		t.Fatalf("cancel fail: open %v draft %q", a.layout.attach != nil, a.conversation.draft)
	}
}

func TestActivityExpands(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_seed")
	a.conversation.selected = "Wired turn"
	seedTranscript(a, []chat.Block{
		{Kind: "activity", Text: "Ran command", Icon: "shell", Key: "activity:call_1",
			Detail: []string{
				"$ echo hi",
				"hi"}},
	})
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
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)

	// The composer talks even with nothing chosen: the send queues the
	// turn for a chat the runner opens.
	if err := tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Ship the boundary check")
	if a.conversation.draft != "Ship the boundary check" {
		t.Fatalf("draft %q after typing", a.conversation.draft)
	}
	tt.Key(0, ui.KeyEnter)
	if a.conversation.draft != "" {
		t.Fatalf("draft %q after Enter", a.conversation.draft)
	}
	if a.conversation.transcript.Len() == 0 || a.conversation.transcript.Row(a.conversation.transcript.Len()-1).Text != "Ship the boundary check" {
		t.Fatalf("last block %v", a.conversation.transcript.Row(a.conversation.transcript.Len()-1))
	}
	if a.conversation.transcript.Row(a.conversation.transcript.Len()-1).Kind != "user" {
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
	if a.conversation.draft != "One more" {
		t.Fatalf("draft %q", a.conversation.draft)
	}
	if err := tt.Click("Send message"); err != nil {
		t.Fatal(err)
	}
	if a.conversation.draft != "" {
		t.Fatalf("draft %q after the send button", a.conversation.draft)
	}
	if n := a.conversation.transcript.Len(); n == 0 || a.conversation.transcript.Row(n-1).Text != "One more" {
		t.Fatalf("last block %v", a.conversation.transcript.Row(n-1))
	}
	// After sending, the button is the idle send button again: it keeps
	// the send label but another click sends nothing.
	before := len(commandsOf(a))
	if err := tt.Click("Send message"); err != nil {
		t.Fatal(err)
	}
	if len(commandsOf(a)) != before {
		t.Fatal("the idle send button submitted a turn")
	}
}
