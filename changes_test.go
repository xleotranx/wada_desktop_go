package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// changesServer is a fake server with one workspace's changes: the working
// tree holds a modified file and an added one, the branch holds the same
// pair plus an older file, and the requests it answered are noted.
func changesServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	asked := &[]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/vcs", func(w http.ResponseWriter, r *http.Request) {
		*asked = append(*asked, "vcs "+r.URL.Query().Get("location[directory]"))
		w.Write([]byte(`{"location":{"directory":"/srv/app"},"data":{"provider":"git",` +
			`"branch":{"current":"feature/remote","default":"main"}}}`))
	})
	mux.HandleFunc("GET /api/vcs/diff", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*asked = append(*asked, "diff "+q.Get("location[directory]")+" "+q.Get("mode"))
		patch := "diff --git a/src/app.go b/src/app.go\nindex 1111111..2222222 100644\n" +
			"--- a/src/app.go\n+++ b/src/app.go\n@@ -1,2 +1,3 @@\n package main\n+// a new line\n \n"
		patchJSON, _ := json.Marshal(patch)
		if q.Get("mode") == "branch" {
			w.Write([]byte(`{"data":[{"file":"src/app.go","patch":` + string(patchJSON) +
				`,"additions":1,"deletions":0,"status":"modified"},` +
				`{"file":"docs/old.md","patch":"@@ -1 +1 @@\n-a\n+b\n","additions":1,"deletions":1,"status":"modified"}]}`))
			return
		}
		w.Write([]byte(`{"data":[{"file":"src/app.go","patch":` + string(patchJSON) +
			`,"additions":1,"deletions":0,"status":"modified"},` +
			`{"file":"src/new.go","patch":"@@ -0,0 +1 @@\n+package main\n","additions":1,"deletions":0,"status":"added"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, asked
}

// TestAgentWorkspaceChanges reads a workspace's version control and its
// diff: the folder and the mode ride the query, and the patch comes back
// with its counts.
func TestAgentWorkspaceChanges(t *testing.T) {
	srv, asked := changesServer(t)
	ag := &opencode.Client{Base: srv.URL + "/api", HTTP: srv.Client()}
	ctx := context.Background()

	info, err := ag.WorkspaceVcs(ctx, "/srv/app")
	if err != nil || info.Provider != "git" || info.Branch != "feature/remote" || info.Base != "main" {
		t.Fatalf("vcs %+v %v", info, err)
	}
	files, err := ag.WorkspaceChanges(ctx, "/srv/app", "working")
	if err != nil || len(files) != 2 {
		t.Fatalf("changes %+v %v", files, err)
	}
	if files[0].File != "src/app.go" || files[0].Additions != 1 || files[0].Status != "modified" ||
		!strings.Contains(files[0].Patch, "+// a new line") {
		t.Fatalf("the first change reads %+v", files[0])
	}
	if files[1].Status != "added" {
		t.Fatalf("the added file reads %+v", files[1])
	}
	if got := (*asked)[1]; got != "diff /srv/app working" {
		t.Fatalf("the diff asked %q", got)
	}
	if _, err := ag.WorkspaceChanges(ctx, "/srv/app", "branch"); err != nil {
		t.Fatal(err)
	}
	if got := (*asked)[len(*asked)-1]; got != "diff /srv/app branch" {
		t.Fatalf("the branch diff asked %q", got)
	}
}

// TestChangesPane walks the tool: opening it reads the chat's folder, the
// files land with their counts, a row shows its own patch, the branch pill
// reads the other mode, and a clean workspace says so.
func TestChangesPane(t *testing.T) {
	srv, asked := changesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []opencode.Session{{ID: "ses_r", Title: "Remote turn", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_r")
	a.conversation.selected = "Remote turn"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Changes")
	if a.workspace.changes == nil || a.workspace.changes.loading {
		t.Fatalf("the tool did not read: %+v", a.workspace.changes)
	}
	if a.workspace.changes.dir != "/srv/app" || a.workspace.changes.mode != "working" || len(a.workspace.changes.files) != 2 {
		t.Fatalf("the read landed on %+v", a.workspace.changes)
	}
	tt.Frame()
	if !tt.HasText("Review the files changed in this workspace.") ||
		!tt.HasText("git · feature/remote") || !tt.HasText("2 files") ||
		!tt.HasText("src/app.go") || !tt.HasText("src/new.go") || !tt.HasText("+1") {
		t.Fatalf("the pane is not drawn: %v", tt.Texts())
	}
	// The file on show is the first one's, and its patch draws.
	if a.workspace.changes.diff.New == nil || !strings.Contains(a.workspace.changes.diff.New.String(), "// a new line") {
		t.Fatalf("the patch is not drawn: %v", tt.Texts())
	}
	// Asking for the other file shows its patch instead.
	if err := tt.Click("Show src/new.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.workspace.changes.selected != 1 || a.workspace.changes.diff.New == nil || a.workspace.changes.diff.New.String() != "package main" {
		t.Fatalf("the second file's patch is not drawn: %v", tt.Texts())
	}

	// The branch mode reads again, and its own files land.
	if err := tt.Click("Branch changes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.workspace.changes.mode != "branch" || len(a.workspace.changes.files) != 2 || a.workspace.changes.selected != 0 {
		t.Fatalf("the branch read landed on %+v", a.workspace.changes)
	}
	if !tt.HasText("git · feature/remote against main") || !tt.HasText("docs/old.md") {
		t.Fatalf("the branch pane is not drawn: %v", tt.Texts())
	}
	if got := (*asked)[len(*asked)-1]; got != "diff /srv/app branch" {
		t.Fatalf("the branch read asked %q", got)
	}

	// Reloading reads the mode on show again.
	before := len(*asked)
	a.reloadBrowserPage()
	if len(*asked) <= before {
		t.Fatal("a reload did not read again")
	}
	if got := (*asked)[len(*asked)-1]; got != "diff /srv/app branch" {
		t.Fatalf("the reload asked %q", got)
	}

	// Another chat's folder is another diff.
	a.scope().sessions = append(a.scope().sessions, opencode.Session{ID: "ses_other", Title: "Other", Directory: "/srv/other"})
	a.adoptSession(a.scope().sessions[len(a.scope().sessions)-1])
	if a.workspace.changes.dir != "/srv/other" {
		t.Fatalf("the tool kept comparing %q", a.workspace.changes.dir)
	}
}

// TestChangesPaneKeepsHonestEdges keeps the tool's quiet paths: a remote
// chat with no folder compares nothing rather than naming this machine's,
// and a folder without changes says so.
func TestChangesPaneKeepsHonestEdges(t *testing.T) {
	srv, _ := changesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.conversation.target.selectSession("")
	a.conversation.selected = ""
	tt := ui.NewTester(a.view, 1400, 920)

	// A remote draft with no folder yet.
	a.openTool("Changes")
	tt.Frame()
	if a.workspace.changes.note == "" || !strings.Contains(a.workspace.changes.note, "no folder on the server") {
		t.Fatalf("a folderless remote reads %+v", a.workspace.changes)
	}
	if tt.HasText("src/app.go") {
		t.Fatalf("the pane drew a diff it never read: %v", tt.Texts())
	}

	// A clean workspace answers with no files rather than an error.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/vcs", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"provider":"git","branch":{"current":"main","default":"main"}}}`))
	})
	mux.HandleFunc("GET /api/vcs/diff", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	})
	clean := httptest.NewServer(mux)
	defer clean.Close()
	a.connections.servers[1].url = clean.URL
	a.scope().sessions = []opencode.Session{{ID: "ses_clean", Title: "Clean turn", Directory: "/srv/clean"}}
	a.conversation.target.selectSession("ses_clean")
	a.conversation.selected = "Clean turn"
	a.openChanges(changesWorking)
	if a.workspace.changes.note != "" || len(a.workspace.changes.files) != 0 {
		t.Fatalf("a clean read landed on %+v", a.workspace.changes)
	}
	tt.Frame()
	if !tt.HasText("Nothing changed here.") {
		t.Fatalf("the empty pane reads: %v", tt.Texts())
	}

	// A server that will not answer says why instead of drawing a diff.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"_tag":"InvalidRequestError","message":"not a repository"}`))
	}))
	defer bad.Close()
	a.connections.servers[1].url = bad.URL
	a.openChanges(changesWorking)
	if !strings.Contains(a.workspace.changes.note, "not a repository") {
		t.Fatalf("a refused read landed on %+v", a.workspace.changes)
	}
}

func TestChangesPatchIsSelectableAndReadOnly(t *testing.T) {
	a := newTestApp(t)
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	line := strings.Repeat("x", 600)
	a.workspace.changes = &changesState{mode: changesWorking, files: []opencode.Change{
		{File: "long.txt", Patch: "@@ -1 +1 @@\n-old\n+" + line + "\n"},
		{File: "other.txt", Patch: "@@ -1 +1 @@\n-before\n+after\n"},
	}}
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("New text"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != line {
		t.Fatal("the full patch line could not be copied")
	}
	tt.Type("an accidental edit")
	if a.workspace.changes.diff.New.String() != line {
		t.Fatalf("the patch was clipped or edited: %v", tt.Texts())
	}
	if err := tt.Click("Show other.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.workspace.changes.diff.New.String() != "after" {
		t.Fatalf("the selected patch did not change: %v", tt.Texts())
	}
	tt.SetDark(true)
	tt.Frame()
}

func TestChangesRawPatch(t *testing.T) {
	for _, patch := range []string{
		"diff --git a/x b/x\nBinary files a/x and b/x differ\n",
		"diff --git a/x b/x\nold mode 100644\nnew mode 100755\n",
		"@@ broken header\n+keep this visible\n",
	} {
		t.Run(patch, func(t *testing.T) {
			a := newTestApp(t)
			a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
			a.workspace.changes = &changesState{mode: changesWorking, files: []opencode.Change{{File: "x", Patch: patch}}}
			tt := ui.NewTester(a.view, 1400, 920)
			if err := tt.Click("Raw patch"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Key(ui.Cmd, ui.KeyC)
			if tt.Clipboard() != patch {
				t.Fatal("the raw patch could not be copied")
			}
			tt.Type("an accidental edit")
			if a.workspace.changes.patch != patch {
				t.Fatalf("the raw patch was lost or edited: %v", tt.Texts())
			}
		})
	}
}

func TestChangeStatusLetters(t *testing.T) {
	if statusLetter("added") != "A" || statusLetter("deleted") != "D" || statusLetter("modified") != "M" {
		t.Fatal("the status letters drifted")
	}
}

// TestChangesDiffStyle switches the file on show between one column and
// two, keeps that reading while another file is picked, and saves the
// choice with the rest of the settings.
func TestChangesDiffStyle(t *testing.T) {
	a := newTestApp(t)
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	a.workspace.changes = &changesState{mode: changesWorking, files: []opencode.Change{
		{File: "app.go", Patch: "@@ -1,2 +1,3 @@\n package main\n+// added\n \n", Additions: 1},
		{File: "other.txt", Patch: "@@ -1 +1 @@\n-before\n+after\n", Additions: 1, Deletions: 1},
	}}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if got := a.workspace.changes.diff.Layout; got != editor.DiffUnified {
		t.Fatalf("the diff started in layout %v", got)
	}
	if !tt.HasText("app.go") || !tt.HasText("+1") {
		t.Fatalf("the selected file's header is missing: %v", tt.Texts())
	}
	if err := tt.Click("Split diff"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.preferences.diffSplit || a.workspace.changes.diff.Layout != editor.DiffSplit {
		t.Fatalf("split choice %v, layout %v", a.preferences.diffSplit, a.workspace.changes.diff.Layout)
	}
	if snap := a.stateSnapshot(); !snap.diffSplit {
		t.Fatal("the split reading did not reach the settings snapshot")
	}
	// Another file keeps the reading the user chose.
	if err := tt.Click("Show other.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.workspace.changes.diff.Layout != editor.DiffSplit ||
		a.workspace.changes.diff.New.String() != "after" {
		t.Fatalf("the second file lost the split reading: %v", tt.Texts())
	}
	if err := tt.Click("Unified diff"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.preferences.diffSplit || a.workspace.changes.diff.Layout != editor.DiffUnified {
		t.Fatalf("back to unified: %v, %v", a.preferences.diffSplit, a.workspace.changes.diff.Layout)
	}
}

// TestChangesPreviewsTheSelectedFile hands the file under the patch to the
// Files tool, keeping the Changes tab in the strip for the way back.
func TestChangesPreviewsTheSelectedFile(t *testing.T) {
	a := newTestApp(t)
	a.runtime.background = func(func() func()) {}
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	a.layout.paneTabs = []paneTab{{kind: "Changes"}}
	a.workspace.changes = &changesState{mode: changesWorking, files: []opencode.Change{
		{File: "cmd/wada/main.go", Patch: "@@ -1 +1 @@\n-a\n+b\n"},
	}}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Preview the selected file"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.browserTool != "Files" || a.workspace.files == nil ||
		a.workspace.files.path != "cmd/wada/main.go" {
		t.Fatalf("the preview landed in %q at %+v", a.layout.browserTool, a.workspace.files)
	}
	kinds := []string{}
	for _, tab := range a.layout.paneTabs {
		kinds = append(kinds, tab.kind)
	}
	if !slices.Contains(kinds, "Changes") || !slices.Contains(kinds, "Files") {
		t.Fatalf("the strip lost a tab: %v", kinds)
	}
}

// TestChangesReviewNotes opens a note from the diff's gutter, writes it,
// and adds it to the message; the next note appends after it.
func TestChangesReviewNotes(t *testing.T) {
	a := newTestApp(t)
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	a.workspace.changes = &changesState{mode: changesWorking, files: []opencode.Change{
		{File: "app.go", Patch: "@@ -1,2 +1,3 @@\n package main\n+// added\n \n", Additions: 1},
	}}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	st := a.workspace.changes
	if st.diff == nil || st.diff.OnGutterButton == nil {
		t.Fatal("the diff does not offer notes")
	}
	st.diff.OnGutterButton(editor.DiffLineRange{Side: editor.DiffNew, Start: 1, End: 1})
	tt.Frame()
	if !tt.HasText("app.go:2") || !tt.HasText("Review note") {
		t.Fatalf("the note editor is missing: %v", tt.Texts())
	}
	tt.Type("return early when the list is empty")
	tt.Frame()
	if len(st.review) != 1 || st.review[0].draft != "return early when the list is empty" {
		t.Fatalf("the note reads %+v", st.review)
	}
	if err := tt.Click("Add to message"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.conversation.draft; got != "In app.go at line 2: return early when the list is empty\n" {
		t.Fatalf("the note reached the composer as %q", got)
	}
	if len(st.review) != 0 {
		t.Fatalf("the note stayed: %+v", st.review)
	}

	// A second note adds a sentence of its own after a blank line.
	st.diff.OnGutterButton(editor.DiffLineRange{Side: editor.DiffNew, Start: 2, End: 2})
	tt.Frame()
	tt.Type("this line has no context")
	tt.Frame()
	if err := tt.Click("Add to message"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	want := "In app.go at line 2: return early when the list is empty\n\n" +
		"In app.go at line 3: this line has no context\n"
	if a.conversation.draft != want {
		t.Fatalf("the message reads %q, want %q", a.conversation.draft, want)
	}
}

// TestReviewNotesStayPerFile keeps a note on one file while another patch
// is on show, and lets it be discarded.
func TestReviewNotesStayPerFile(t *testing.T) {
	a := newTestApp(t)
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	a.workspace.changes = &changesState{mode: changesWorking, files: []opencode.Change{
		{File: "app.go", Patch: "@@ -1,2 +1,3 @@\n package main\n+// added\n \n"},
		{File: "other.txt", Patch: "@@ -1 +1 @@\n-before\n+after\n"},
	}}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	st := a.workspace.changes
	st.diff.OnGutterButton(editor.DiffLineRange{Side: editor.DiffNew, Start: 0, End: 0})
	tt.Frame()
	tt.Type("first note")
	tt.Frame()
	if err := tt.Click("Show other.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if notes := a.reviewNotesFor("app.go"); len(notes) != 1 || notes[0].draft != "first note" {
		t.Fatalf("the note left its file: %+v", notes)
	}
	if tt.HasText("Ask for a change") {
		t.Fatal("the note showed under another file")
	}
	if err := tt.Click("Show app.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	notes := a.reviewNotesFor("app.go")
	if len(notes) != 1 || notes[0].draft != "first note" || !tt.HasText("app.go:1") {
		t.Fatalf("the note did not come back with its file: %+v %v", notes, tt.Texts())
	}
	if err := tt.Click("Discard the note"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.reviewNotesFor("app.go")) != 0 {
		t.Fatal("the discarded note stayed")
	}
}

// TestReviewTrayAddsAllNotes collects notes from more than one file into
// one message, in file and line order, and clears the tray.
func TestReviewTrayAddsAllNotes(t *testing.T) {
	a := newTestApp(t)
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	a.workspace.changes = &changesState{mode: changesWorking, files: []opencode.Change{
		{File: "app.go", Patch: "@@ -1,2 +1,3 @@\n package main\n+// added\n \n"},
	}}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	st := a.workspace.changes
	a.beginReviewNote("app.go", editor.DiffLineRange{Side: editor.DiffNew, Start: 1, End: 1})
	a.beginReviewNote("other.txt", editor.DiffLineRange{Side: editor.DiffNew, Start: 4, End: 4})
	a.beginReviewNote("zzz.txt", editor.DiffLineRange{Side: editor.DiffNew, Start: 0, End: 0})
	st.review[0].draft = "return early"
	st.review[1].draft = "fix the name"
	tt.Frame()
	if !tt.HasText("3 review notes") || !tt.HasText("Add all to message") {
		t.Fatalf("the tray is missing: %v", tt.Texts())
	}
	if err := tt.Click("Add all to message"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	want := "In app.go at line 2: return early\n\nIn other.txt at line 5: fix the name\n"
	if a.conversation.draft != want {
		t.Fatalf("the tray wrote %q, want %q", a.conversation.draft, want)
	}
	if len(st.review) != 0 || tt.HasText("review notes") {
		t.Fatalf("the tray outlived its notes: %+v", st.review)
	}
}

// TestReviewNoteEnterAdds writes a note and presses Enter in its field to
// take it to the message.
func TestReviewNoteEnterAdds(t *testing.T) {
	a := newTestApp(t)
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	a.workspace.changes = &changesState{mode: changesWorking, files: []opencode.Change{
		{File: "app.go", Patch: "@@ -1,2 +1,3 @@\n package main\n+// added\n \n"},
	}}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	st := a.workspace.changes
	st.diff.OnGutterButton(editor.DiffLineRange{Side: editor.DiffNew, Start: 1, End: 1})
	tt.Frame()
	tt.Type("quick note")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.conversation.draft != "In app.go at line 2: quick note\n" {
		t.Fatalf("Enter left the draft as %q", a.conversation.draft)
	}
	if len(st.review) != 0 {
		t.Fatalf("the note stayed: %+v", st.review)
	}
}

// TestChangesFileActions copies a changed file's path and hands it to this
// machine's editor and file manager, then previews it in Files.
func TestChangesFileActions(t *testing.T) {
	a := newTestApp(t)
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	dir := t.TempDir()
	a.workspace.changes = &changesState{mode: changesWorking, dir: dir, files: []opencode.Change{
		{File: "src/app.go", Patch: "@@ -1 +1 @@\n-a\n+b\n"},
	}}
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	openActions := func() {
		t.Helper()
		if err := tt.Click("File actions for src/app.go"); err != nil {
			t.Fatal(err)
		}
	}
	openActions()
	if err := tt.ChooseMenuItem("Copy path"); err != nil {
		t.Fatal(err)
	}
	if copied != filepath.Join(dir, "src/app.go") {
		t.Fatalf("copied %q", copied)
	}
	openActions()
	if err := tt.ChooseMenuItem("Open in editor"); err != nil {
		t.Fatal(err)
	}
	openActions()
	if err := tt.ChooseMenuItem("Reveal in folder"); err != nil {
		t.Fatal(err)
	}
	urls := tt.OpenedURLs()
	if len(urls) != 2 {
		t.Fatalf("opened %d URLs: %v", len(urls), urls)
	}
	if !strings.HasSuffix(urls[0], "/src/app.go") || !strings.Contains(urls[1], dir) ||
		strings.HasSuffix(urls[1], "app.go") {
		t.Fatalf("opened %v", urls)
	}
	openActions()
	if err := tt.ChooseMenuItem("Preview in Files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.browserTool != "Files" || a.workspace.files == nil ||
		a.workspace.files.path != "src/app.go" {
		t.Fatalf("preview landed in %q at %+v", a.layout.browserTool, a.workspace.files)
	}
}

// TestChangesFileActionsKeepRemoteFilesOnTheServer leaves the editor and
// file manager out for a server's folder, and copies the server's path.
func TestChangesFileActionsKeepRemoteFilesOnTheServer(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: "http://10.0.0.12:4096"})
	a.connections.activeServer = "srv-box"
	a.layout.browserCollapsed, a.layout.browserTool = false, "Changes"
	a.workspace.changes = &changesState{mode: changesWorking, dir: "/srv/app", files: []opencode.Change{
		{File: "src/app.go", Patch: "@@ -1 +1 @@\n-a\n+b\n"},
	}}
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("File actions for src/app.go"); err != nil {
		t.Fatal(err)
	}
	items := tt.Menu()
	if slices.Contains(items, "Open in editor") || slices.Contains(items, "Reveal in folder") {
		t.Fatalf("a remote file offered local actions: %v", items)
	}
	if !slices.Contains(items, "Preview in Files") || !slices.Contains(items, "Copy path") {
		t.Fatalf("the menu lacks its server-safe actions: %v", items)
	}
	if err := tt.ChooseMenuItem("Copy path"); err != nil {
		t.Fatal(err)
	}
	if copied != "src/app.go" {
		t.Fatalf("copied %q", copied)
	}
}
