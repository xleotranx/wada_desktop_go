package main

import (
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// filesServer is a fake server with one small workspace: a folder that
// nests, a file to read, and one file too big for the viewer.
func filesServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	asked := &[]string{}
	listings := map[string]string{
		"/srv/app": `{"path":"src/","type":"directory"},{"path":"README.md","type":"file"}`,
		"/srv/app/src": `{"path":"src/main.go","type":"file"},` +
			`{"path":"src/deep/","type":"directory"}`,
		"/srv/app/src/deep": `{"path":"src/deep/notes.txt","type":"file"}`,
	}
	files := map[string]string{
		"README.md":          "# Remote box\n\nChats run here.\n",
		"src/main.go":        "package main\n\nfunc main() {}\n",
		"src/deep/notes.txt": strings.Repeat("a long line for the viewer\n", 4),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		location, rel := q.Get("location[directory]"), q.Get("path")
		asked = appendString(asked, "list "+location+" "+rel)
		body, ok := listings[path.Join(location, rel)]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"_tag":"FileNotFoundError","message":"Directory not found: ."}`))
			return
		}
		w.Write([]byte(`{"location":{"directory":"` + location + `"},"data":[` + body + `]}`))
	})
	mux.HandleFunc("GET /api/fs/find", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		asked = appendString(asked, "find "+q.Get("location[directory]")+" "+q.Get("query")+
			" "+q.Get("type")+" "+q.Get("limit"))
		if q.Get("query") == "zzz" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		w.Write([]byte(`{"location":{"directory":"/srv/app"},"data":[` +
			`{"path":"src/main.go","type":"file"},{"path":"src/deep/notes.txt","type":"file"}]}`))
	})
	mux.HandleFunc("GET /api/fs/read/{file...}", func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.PathValue("file"), "/")
		location := r.URL.Query().Get("location[directory]")
		asked = appendString(asked, "read "+location+" "+rel)
		body, ok := files[rel]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"_tag":"FileNotFoundError","message":"File not found"}`))
			return
		}
		w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, asked
}

// appendString adds one ask to a list two goroutines may share.
func appendString(list *[]string, item string) *[]string {
	*list = append(*list, item)
	return list
}

// TestFilesPane walks the tool: opening it reads the chat's folder, the
// tree unfolds a folder at a time, a file's text lands beside it, and the
// toolbar reads the whole thing again.
func TestFilesPane(t *testing.T) {
	srv, asked := filesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []opencode.Session{{ID: "ses_r", Title: "Remote turn", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_r")
	a.conversation.selected = "Remote turn"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Files")
	if a.workspace.files == nil || len(a.workspace.files.entries["."]) != 2 || a.workspace.files.location != "/srv/app" {
		t.Fatalf("the tool did not read the folder: %+v", a.workspace.files)
	}
	tt.Frame()
	if !tt.HasText("Browse the files in the current project.") || !tt.HasText("Workspace  ·  app") ||
		!tt.HasText("src") || !tt.HasText("README.md") || !tt.HasText("Pick a file to read it.") {
		t.Fatalf("the pane is not drawn: %v", tt.Texts())
	}

	// A folder unfolds into its own children, one call for it alone.
	if err := tt.Click("Unfold src"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.workspace.files.entries["src"]) != 2 || !tt.HasText("main.go") || !tt.HasText("deep") {
		t.Fatalf("the folder did not unfold: %+v / %v", a.workspace.files.entries["src"], tt.Texts())
	}
	if got := (*asked)[len(*asked)-1]; got != "list /srv/app src" {
		t.Fatalf("the unfold asked %q", got)
	}
	if err := tt.Click("Unfold deep"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("notes.txt") {
		t.Fatalf("the nested folder did not unfold: %v", tt.Texts())
	}
	// Folding takes it back out of the tree without another read.
	if err := tt.Click("Unfold src"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("main.go") {
		t.Fatalf("the folder stayed unfolded: %v", tt.Texts())
	}

	// A file's text lands in the viewer.
	if err := tt.Click("Read README.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.workspace.files.path != "README.md" || !strings.Contains(a.workspace.files.content, "Chats run here.") {
		t.Fatalf("the file did not read: %+v", a.workspace.files)
	}
	if err := tt.Click("File preview"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != a.workspace.files.content {
		t.Fatal("the preview could not be copied")
	}
	tt.Type("an accidental edit")
	if a.workspace.files.viewer.Buffer.String() != a.workspace.files.content {
		t.Fatal("the read-only preview changed")
	}

	// Reloading reads the tree and the file on show again.
	before := len(*asked)
	a.reloadBrowserPage()
	if len(*asked) <= before {
		t.Fatal("a reload did not read again")
	}
	if got := (*asked)[len(*asked)-1]; got != "read /srv/app README.md" {
		t.Fatalf("the reload asked %q", got)
	}

	// Another chat's folder is another tree.
	a.scope().sessions = append(a.scope().sessions, opencode.Session{ID: "ses_other", Title: "Other", Directory: "/srv/app/src"})
	a.adoptSession(a.scope().sessions[len(a.scope().sessions)-1])
	if a.workspace.files.location != "/srv/app/src" || a.workspace.files.path != "" {
		t.Fatalf("the tool kept reading %+v", a.workspace.files)
	}
}

// TestFilesSearch runs the pane's search: Enter asks the server for the
// files whose paths match, the matches stand in the tree's place, a match
// opens into the viewer, and an emptied field brings the tree back.
func TestFilesSearch(t *testing.T) {
	srv, asked := filesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []opencode.Session{{ID: "ses_r", Title: "Remote turn", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_r")
	a.conversation.selected = "Remote turn"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Files")
	tt.Frame()
	if !tt.HasText("Search files") {
		t.Fatalf("the field is not drawn: %v", tt.Texts())
	}
	// Typing and pressing Enter is the whole road: no job, just the read.
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Type("notes")
	tt.Key(0, ui.KeyEnter)
	if a.workspace.files.query != "notes" || a.workspace.files.finding {
		t.Fatalf("the search reads %+v", a.workspace.files)
	}
	if len(a.workspace.files.results) != 2 {
		t.Fatalf("the results read %+v", a.workspace.files.results)
	}
	if got := (*asked)[len(*asked)-1]; got != "find /srv/app notes file 50" {
		t.Fatalf("the search asked %q", got)
	}
	tt.Frame()
	if !tt.HasText("main.go") || !tt.HasText("notes.txt") || !tt.HasText("src/deep") {
		t.Fatalf("the results are not drawn: %v", tt.Texts())
	}
	if tt.HasText("Workspace  ·") {
		t.Fatalf("the tree stood beside the results: %v", tt.Texts())
	}

	// A match opens into the viewer under the results.
	if err := tt.Click("Read src/deep/notes.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.workspace.files.path != "src/deep/notes.txt" || !strings.Contains(a.workspace.files.content, "a long line") {
		t.Fatalf("the match did not open: %+v", a.workspace.files)
	}
	if !tt.HasText("notes.txt") {
		t.Fatalf("the results left with the read: %v", tt.Texts())
	}

	// Emptying the field puts the tree back; a query with no matches says so.
	a.workspace.files.query = ""
	tt.Frame()
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	if a.workspace.files.query != "" || !tt.HasText("Workspace  ·") {
		t.Fatalf("the tree did not come back: %+v / %v", a.workspace.files, tt.Texts())
	}
	a.workspace.files.query = "zzz"
	a.filesFind()
	tt.Frame()
	if !tt.HasText("No files match zzz.") {
		t.Fatalf("an empty result reads: %v", tt.Texts())
	}

	// Reloading re-runs what the pane shows: the search, not the tree.
	before := len(*asked)
	a.reloadBrowserPage()
	if got := (*asked)[len(*asked)-1]; got != "find /srv/app zzz file 50" {
		t.Fatalf("the reload asked %q (before %d)", got, before)
	}
}

// TestFilesPaneKeepsHonestEdges keeps the tool's quiet paths: a remote chat
// with no folder reads no tree, a big file is cut with a note, and a folder
// the server refuses says so.
func TestFilesPaneKeepsHonestEdges(t *testing.T) {
	srv, _ := filesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Files")
	tt.Frame()
	if a.workspace.files.note == "" || !strings.Contains(a.workspace.files.note, "no folder on the server") {
		t.Fatalf("a folderless remote reads %+v", a.workspace.files)
	}
	if !tt.HasText("Choose project") || tt.HasText("Workspace  ·") {
		t.Fatalf("the pane drew a tree it never read: %v", tt.Texts())
	}

	// A folder on the server, with a file past the viewer's cap.
	a.scope().sessions = []opencode.Session{{ID: "ses_big", Title: "Big", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_big")
	a.conversation.selected = "Big"
	a.openFiles()
	if len(a.workspace.files.entries["."]) != 2 {
		t.Fatalf("the tree did not land: %+v", a.workspace.files)
	}
	a.filesRead("src/deep/notes.txt")
	if a.workspace.files.clipped {
		t.Fatal("a small file read as clipped")
	}
	if !strings.Contains(a.workspace.files.content, "a long line for the viewer") {
		t.Fatalf("the file did not read: %+v", a.workspace.files)
	}

	// The viewer cuts what runs past its window, and says so.
	st := a.workspace.files
	st.location, st.entries["."] = "/srv/big", []opencode.FileEntry{{Path: "huge.txt", Name: "huge.txt"}}
	big := strings.Repeat("x\n", filesShown/2+8)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fs/read/{file...}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(big))
	})
	bigSrv := httptest.NewServer(mux)
	defer bigSrv.Close()
	a.connections.servers[1].url = bigSrv.URL
	a.filesRead("huge.txt")
	if !st.clipped || len(st.content) != filesShown {
		t.Fatalf("the big file read %d bytes, clipped %v", len(st.content), st.clipped)
	}
	tt.Frame()
	if !tt.HasText("the first 256 KB") {
		t.Fatalf("the cut is not said: %v", tt.Texts())
	}

	// A file the server refuses keeps the tree and explains itself.
	st.location = "/srv/app"
	a.connections.servers[1].url = srv.URL
	a.filesList(".")
	a.filesRead("README.md")
	if st.fileErr != "" {
		t.Fatalf("a readable file read as refused: %+v", st)
	}
	a.filesRead("nowhere.txt")
	if !strings.Contains(st.fileErr, "did not read") {
		t.Fatalf("a refused file reads %+v", st)
	}
	tt.Frame()
	if !tt.HasText("src") {
		t.Fatalf("a refused file took the tree with it: %v", tt.Texts())
	}
}
