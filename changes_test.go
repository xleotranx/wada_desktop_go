package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
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
			"--- a/src/app.go\n+++ b/src/app.go\n@@ -1,3 +1,4 @@\n package main\n+// a new line\n \n"
		if q.Get("mode") == "branch" {
			w.Write([]byte(`{"data":[{"file":"src/app.go","patch":` + jsonString(patch) +
				`,"additions":1,"deletions":0,"status":"modified"},` +
				`{"file":"docs/old.md","patch":"@@ -1 +1 @@\n-a\n+b\n","additions":1,"deletions":1,"status":"modified"}]}`))
			return
		}
		w.Write([]byte(`{"data":[{"file":"src/app.go","patch":` + jsonString(patch) +
			`,"additions":1,"deletions":0,"status":"modified"},` +
			`{"file":"src/new.go","patch":"@@ -0,0 +1 @@\n+package main\n","additions":1,"deletions":0,"status":"added"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, asked
}

// jsonString quotes a patch for a fake answer.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TestAgentWorkspaceChanges reads a workspace's version control and its
// diff: the folder and the mode ride the query, and the patch comes back
// with its counts.
func TestAgentWorkspaceChanges(t *testing.T) {
	srv, asked := changesServer(t)
	ag := &agent{base: srv.URL + "/api", http: srv.Client()}
	ctx := context.Background()

	info, err := ag.workspaceVcs(ctx, "/srv/app")
	if err != nil || info.provider != "git" || info.branch != "feature/remote" || info.base != "main" {
		t.Fatalf("vcs %+v %v", info, err)
	}
	files, err := ag.workspaceChanges(ctx, "/srv/app", "working")
	if err != nil || len(files) != 2 {
		t.Fatalf("changes %+v %v", files, err)
	}
	if files[0].file != "src/app.go" || files[0].additions != 1 || files[0].status != "modified" ||
		!strings.Contains(files[0].patch, "+// a new line") {
		t.Fatalf("the first change reads %+v", files[0])
	}
	if files[1].status != "added" {
		t.Fatalf("the added file reads %+v", files[1])
	}
	if got := (*asked)[1]; got != "diff /srv/app working" {
		t.Fatalf("the diff asked %q", got)
	}
	if _, err := ag.workspaceChanges(ctx, "/srv/app", "branch"); err != nil {
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
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.activeServer = "srv-box"
	a.scope().sessions = []agentSession{{id: "ses_r", title: "Remote turn", dir: "/srv/app"}}
	a.selectedSession, a.selected = "ses_r", "Remote turn"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Changes")
	if a.changes == nil || a.changes.loading {
		t.Fatalf("the tool did not read: %+v", a.changes)
	}
	if a.changes.dir != "/srv/app" || a.changes.mode != "working" || len(a.changes.files) != 2 {
		t.Fatalf("the read landed on %+v", a.changes)
	}
	tt.Frame()
	if !tt.HasText("Review the files changed in this workspace.") ||
		!tt.HasText("git · feature/remote") || !tt.HasText("2 files") ||
		!tt.HasText("src/app.go") || !tt.HasText("src/new.go") || !tt.HasText("+1") {
		t.Fatalf("the pane is not drawn: %v", tt.Texts())
	}
	// The file on show is the first one's, and its patch draws.
	if !tt.HasText("package main") || !tt.HasText("+// a new line") {
		t.Fatalf("the patch is not drawn: %v", tt.Texts())
	}
	// Asking for the other file shows its patch instead.
	if err := tt.Click("Show src/new.go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.changes.selected != 1 || !tt.HasText("+package main") {
		t.Fatalf("the second file's patch is not drawn: %v", tt.Texts())
	}

	// The branch mode reads again, and its own files land.
	if err := tt.Click("Branch changes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.changes.mode != "branch" || len(a.changes.files) != 2 || a.changes.selected != 0 {
		t.Fatalf("the branch read landed on %+v", a.changes)
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
	a.scope().sessions = append(a.scope().sessions, agentSession{id: "ses_other", title: "Other", dir: "/srv/other"})
	a.adoptSession(a.scope().sessions[len(a.scope().sessions)-1])
	if a.changes.dir != "/srv/other" {
		t.Fatalf("the tool kept comparing %q", a.changes.dir)
	}
}

// TestChangesPaneKeepsHonestEdges keeps the tool's quiet paths: a remote
// chat with no folder compares nothing rather than naming this machine's,
// and a folder without changes says so.
func TestChangesPaneKeepsHonestEdges(t *testing.T) {
	srv, _ := changesServer(t)
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.activeServer = "srv-box"
	a.selectedSession, a.selected = "", ""
	tt := ui.NewTester(a.view, 1400, 920)

	// A remote draft with no folder yet.
	a.openTool("Changes")
	tt.Frame()
	if a.changes.note == "" || !strings.Contains(a.changes.note, "no folder on the server") {
		t.Fatalf("a folderless remote reads %+v", a.changes)
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
	a.servers[1].url = clean.URL
	a.scope().sessions = []agentSession{{id: "ses_clean", title: "Clean turn", dir: "/srv/clean"}}
	a.selectedSession, a.selected = "ses_clean", "Clean turn"
	a.openChanges(changesWorking)
	if a.changes.note != "" || len(a.changes.files) != 0 {
		t.Fatalf("a clean read landed on %+v", a.changes)
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
	a.servers[1].url = bad.URL
	a.openChanges(changesWorking)
	if !strings.Contains(a.changes.note, "not a repository") {
		t.Fatalf("a refused read landed on %+v", a.changes)
	}
}

// TestPatchTint keeps the patch's reading: adding and removing lines carry
// the palette's diff slots, and the headers do not.
func TestPatchTint(t *testing.T) {
	p := palette{diffAdd: ui.Hex("#00ff00"), diffDel: ui.Hex("#ff0000"),
		diffAddBg: ui.Hex("#00ff0018"), diffDelBg: ui.Hex("#ff000018"),
		diffHunkBg: ui.Hex("#0000ff10"), bg: ui.Hex("#ffffff"),
		link: ui.Hex("#0000ff"), muted: ui.Hex("#999999"), faint: ui.Hex("#cccccc")}
	if got := patchTint(p, "+added"); got != p.diffAdd {
		t.Fatalf("an added line reads %v", got)
	}
	if got := patchTint(p, "-removed"); got != p.diffDel {
		t.Fatalf("a removed line reads %v", got)
	}
	if got := patchTint(p, "+++ b/src/app.go"); got != p.muted {
		t.Fatalf("the file header reads %v", got)
	}
	if got := patchTint(p, "@@ -1,3 +1,4 @@"); got != p.link {
		t.Fatalf("a hunk header reads %v", got)
	}
	if got := patchTint(p, " package main"); got != p.muted {
		t.Fatalf("a context line reads %v", got)
	}
	if got := patchTint(p, "diff --git a/x b/x"); got != p.faint {
		t.Fatalf("the diff header reads %v", got)
	}
	if statusLetter("added") != "A" || statusLetter("deleted") != "D" || statusLetter("modified") != "M" {
		t.Fatal("the status letters drifted")
	}
	// Each line rides its own strip: the washes for what moved, the pane's
	// own color for what did not.
	if got := patchBg(p, "+added"); got == p.bg || got == p.diffDelBg {
		t.Fatalf("an added line rides %v", got)
	}
	if got := patchBg(p, "-removed"); got == p.bg || got == p.diffAddBg {
		t.Fatalf("a removed line rides %v", got)
	}
	if got := patchBg(p, "@@ -1 +1 @@"); got != p.diffHunkBg {
		t.Fatalf("a hunk rides %v", got)
	}
	for _, plain := range []string{"+++ b/x", "--- a/x", "diff --git a/x b/x", " context"} {
		if got := patchBg(p, plain); got != p.bg {
			t.Fatalf("%q rides %v, want the pane's own color", plain, got)
		}
	}
}
