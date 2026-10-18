package main

import (
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

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
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.activeServer = "srv-box"
	a.scope().sessions = []agentSession{{id: "ses_r", title: "Remote turn", dir: "/srv/app"}}
	a.selectedSession, a.selected = "ses_r", "Remote turn"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Files")
	if a.files == nil || len(a.files.entries["."]) != 2 || a.files.location != "/srv/app" {
		t.Fatalf("the tool did not read the folder: %+v", a.files)
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
	if len(a.files.entries["src"]) != 2 || !tt.HasText("main.go") || !tt.HasText("deep") {
		t.Fatalf("the folder did not unfold: %+v / %v", a.files.entries["src"], tt.Texts())
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
	if a.files.path != "README.md" || !strings.Contains(a.files.content, "Chats run here.") {
		t.Fatalf("the file did not read: %+v", a.files)
	}
	if !tt.HasText("# Remote box") || !tt.HasText("Chats run here.") {
		t.Fatalf("the viewer is not drawn: %v", tt.Texts())
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
	a.scope().sessions = append(a.scope().sessions, agentSession{id: "ses_other", title: "Other", dir: "/srv/app/src"})
	a.adoptSession(a.scope().sessions[len(a.scope().sessions)-1])
	if a.files.location != "/srv/app/src" || a.files.path != "" {
		t.Fatalf("the tool kept reading %+v", a.files)
	}
}

// TestFilesPaneKeepsHonestEdges keeps the tool's quiet paths: a remote chat
// with no folder reads no tree, a big file is cut with a note, and a folder
// the server refuses says so.
func TestFilesPaneKeepsHonestEdges(t *testing.T) {
	srv, _ := filesServer(t)
	a := newApp()
	a.servers = append(a.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.activeServer = "srv-box"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Files")
	tt.Frame()
	if a.files.note == "" || !strings.Contains(a.files.note, "no folder on the server") {
		t.Fatalf("a folderless remote reads %+v", a.files)
	}
	if !tt.HasText("Choose project") || tt.HasText("Workspace  ·") {
		t.Fatalf("the pane drew a tree it never read: %v", tt.Texts())
	}

	// A folder on the server, with a file past the viewer's cap.
	a.scope().sessions = []agentSession{{id: "ses_big", title: "Big", dir: "/srv/app"}}
	a.selectedSession, a.selected = "ses_big", "Big"
	a.openFiles()
	if len(a.files.entries["."]) != 2 {
		t.Fatalf("the tree did not land: %+v", a.files)
	}
	a.filesRead("src/deep/notes.txt")
	if a.files.clipped {
		t.Fatal("a small file read as clipped")
	}
	if !strings.Contains(a.files.content, "a long line for the viewer") {
		t.Fatalf("the file did not read: %+v", a.files)
	}

	// The viewer cuts what runs past its window, and says so.
	st := a.files
	st.location, st.entries["."] = "/srv/big", []fsEntry{{rel: "huge.txt", name: "huge.txt"}}
	big := strings.Repeat("x\n", filesShown/2+8)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fs/read/{file...}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(big))
	})
	bigSrv := httptest.NewServer(mux)
	defer bigSrv.Close()
	a.servers[1].url = bigSrv.URL
	a.filesRead("huge.txt")
	if !st.clipped || len(st.content) != filesShown {
		t.Fatalf("the big file read %d bytes, clipped %v", len(st.content), st.clipped)
	}
	tt.Frame()
	if !tt.HasText("the first 256 KB") {
		t.Fatalf("the cut is not said: %v", tt.Texts())
	}
	// One line the width of the window is cut to what the viewer draws.
	if got := clipLine(strings.Repeat("x", filesLineCut+5)); len([]rune(got)) != filesLineCut+1 ||
		!strings.HasSuffix(got, "…") {
		t.Fatalf("a line past the cut reads %q", got)
	}
	if got := clipLine("short enough"); got != "short enough" {
		t.Fatalf("a short line reads %q", got)
	}

	// A file the server refuses keeps the tree and explains itself.
	st.location = "/srv/app"
	a.servers[1].url = srv.URL
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
