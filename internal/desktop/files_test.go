package desktop

import (
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/workspace"

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
	a.scope().sessions = []agent.Session{{ID: "ses_r", Title: "Remote turn", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_r")
	a.conversation.selected = "Remote turn"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Files")
	if codingModule(a).Files == nil || len(codingModule(a).Files.Entries["."]) != 2 || codingModule(a).Files.Location != "/srv/app" {
		t.Fatalf("the tool did not read the folder: %+v", codingModule(a).Files)
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
	if len(codingModule(a).Files.Entries["src"]) != 2 || !tt.HasText("main.go") || !tt.HasText("deep") {
		t.Fatalf("the folder did not unfold: %+v / %v", codingModule(a).Files.Entries["src"], tt.Texts())
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
	if codingModule(a).Files.Path != "README.md" || !strings.Contains(codingModule(a).Files.Content, "Chats run here.") {
		t.Fatalf("the file did not read: %+v", codingModule(a).Files)
	}
	if err := tt.Click("File preview"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != codingModule(a).Files.Content {
		t.Fatal("the preview could not be copied")
	}
	tt.Type("an accidental edit")
	if codingModule(a).Files.Viewer.Buffer.String() != codingModule(a).Files.Content {
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
	a.scope().sessions = append(a.scope().sessions, agent.Session{ID: "ses_other", Title: "Other", Directory: "/srv/app/src"})
	a.adoptSession(a.scope().sessions[len(a.scope().sessions)-1])
	if codingModule(a).Files.Location != "/srv/app/src" || codingModule(a).Files.Path != "" {
		t.Fatalf("the tool kept reading %+v", codingModule(a).Files)
	}
}

// TestFilesDialTheLocalServer keeps the tool working on the app's own
// server: the built-in row holds neither an address nor a password, so a
// read takes the spawned address the last status named and this run's
// password, and nothing ephemeral lands in the saved row.
func TestFilesDialTheLocalServer(t *testing.T) {
	mux := http.NewServeMux()
	asked := &[]string{}
	mux.HandleFunc("GET /api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "opencode" || pass != "run-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		asked = appendString(asked, "list "+r.URL.Query().Get("location[directory]"))
		w.Write([]byte(`{"location":{"directory":"/home/work"},"data":[{"path":"plan.md","type":"file"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	a := newTestApp(t)
	a.setHealth(localServerID, serverHealth{status: healthReady, addr: srv.URL})
	a.runtime.localPassword = "run-key"
	a.scope().sessions = []agent.Session{{ID: "ses_l", Title: "Local turn", Directory: "/home/work"}}
	a.conversation.target.selectSession("ses_l")
	a.conversation.selected = "Local turn"

	a.openTool("Files")
	if codingModule(a).Files == nil || len(codingModule(a).Files.Entries["."]) != 1 {
		t.Fatalf("the tool did not read the local folder: %+v", codingModule(a).Files)
	}
	if got := (*asked)[len(*asked)-1]; got != "list /home/work" {
		t.Fatalf("the tool asked %q", got)
	}
	if a.connections.servers[0].url != "" || a.connections.servers[0].password != "" {
		t.Fatalf("the saved row took ephemeral state: %+v", a.connections.servers[0])
	}
}

// TestFilesOnAComingLocalServerNameTheWait keeps the pane honest while the
// app's server is still coming up: a dial it cannot make yet names the
// wait, not an address nobody typed.
func TestFilesOnAComingLocalServerNameTheWait(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []agent.Session{{ID: "ses_l", Title: "Local turn", Directory: "/home/work"}}
	a.conversation.target.selectSession("ses_l")
	a.conversation.selected = "Local turn"
	tt := ui.NewTester(a.view, 1400, 920)

	a.openTool("Files")
	tt.Frame()
	note := codingModule(a).Files.Note
	if !strings.Contains(note, "not up yet") || strings.Contains(note, "give the server an address") {
		t.Fatalf("the pane said %q", note)
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
	a.scope().sessions = []agent.Session{{ID: "ses_r", Title: "Remote turn", Directory: "/srv/app"}}
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
	if codingModule(a).Files.Query != "notes" || codingModule(a).Files.Finding {
		t.Fatalf("the search reads %+v", codingModule(a).Files)
	}
	if len(codingModule(a).Files.Results) != 2 {
		t.Fatalf("the results read %+v", codingModule(a).Files.Results)
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
	if codingModule(a).Files.Path != "src/deep/notes.txt" || !strings.Contains(codingModule(a).Files.Content, "a long line") {
		t.Fatalf("the match did not open: %+v", codingModule(a).Files)
	}
	if !tt.HasText("notes.txt") {
		t.Fatalf("the results left with the read: %v", tt.Texts())
	}

	// Emptying the field puts the tree back; a query with no matches says so.
	codingModule(a).Files.Query = ""
	tt.Frame()
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	if codingModule(a).Files.Query != "" || !tt.HasText("Workspace  ·") {
		t.Fatalf("the tree did not come back: %+v / %v", codingModule(a).Files, tt.Texts())
	}
	codingModule(a).Files.Query = "zzz"
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
	if codingModule(a).Files.Note == "" || !strings.Contains(codingModule(a).Files.Note, "no folder on the server") {
		t.Fatalf("a folderless remote reads %+v", codingModule(a).Files)
	}
	if !tt.HasText("Choose project") || tt.HasText("Workspace  ·") {
		t.Fatalf("the pane drew a tree it never read: %v", tt.Texts())
	}

	// A folder on the server, with a file past the viewer's cap.
	a.scope().sessions = []agent.Session{{ID: "ses_big", Title: "Big", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_big")
	a.conversation.selected = "Big"
	a.openFiles()
	if len(codingModule(a).Files.Entries["."]) != 2 {
		t.Fatalf("the tree did not land: %+v", codingModule(a).Files)
	}
	a.filesRead("src/deep/notes.txt")
	if codingModule(a).Files.Clipped {
		t.Fatal("a small file read as clipped")
	}
	if !strings.Contains(codingModule(a).Files.Content, "a long line for the viewer") {
		t.Fatalf("the file did not read: %+v", codingModule(a).Files)
	}

	// The viewer cuts what runs past its window, and says so.
	st := codingModule(a).Files
	st.Location, st.Entries["."] = "/srv/big", []workspace.FileEntry{{Path: "huge.txt", Name: "huge.txt"}}
	big := strings.Repeat("x\n", filesShown/2+8)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/fs/read/{file...}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(big))
	})
	bigSrv := httptest.NewServer(mux)
	defer bigSrv.Close()
	a.connections.servers[1].url = bigSrv.URL
	a.filesRead("huge.txt")
	if !st.Clipped || len(st.Content) != filesShown {
		t.Fatalf("the big file read %d bytes, clipped %v", len(st.Content), st.Clipped)
	}
	tt.Frame()
	if !tt.HasText("the first 256 KB") {
		t.Fatalf("the cut is not said: %v", tt.Texts())
	}

	// A file the server refuses keeps the tree and explains itself.
	st.Location = "/srv/app"
	a.connections.servers[1].url = srv.URL
	a.filesList(".")
	a.filesRead("README.md")
	if st.FileErr != "" {
		t.Fatalf("a readable file read as refused: %+v", st)
	}
	a.filesRead("nowhere.txt")
	if !strings.Contains(st.FileErr, "did not read") {
		t.Fatalf("a refused file reads %+v", st)
	}
	tt.Frame()
	if !tt.HasText("src") {
		t.Fatalf("a refused file took the tree with it: %v", tt.Texts())
	}
}

// TestFilesPaneActions copies the open file's path and hands it to the
// desktop editor and file manager.
func TestFilesPaneActions(t *testing.T) {
	a := newTestApp(t)
	a.runtime.background = func(func() func()) {}
	dir := t.TempDir()
	a.openTool("Files")
	codingModule(a).Files = &filesState{
		Location: dir,
		Open:     map[string]bool{},
		Entries:  map[string][]workspace.FileEntry{".": {{Path: "main.go", Name: "main.go"}}},
		Loading:  map[string]bool{},
		Path:     "main.go",
		Content:  "package main",
	}
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Copy file path"); err != nil {
		t.Fatal(err)
	}
	if copied != filepath.Join(dir, "main.go") {
		t.Fatalf("copied %q", copied)
	}
	if err := tt.Click("Open in editor"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Reveal in folder"); err != nil {
		t.Fatal(err)
	}
	urls := tt.OpenedURLs()
	if len(urls) != 2 {
		t.Fatalf("opened %d URLs: %v", len(urls), urls)
	}
	if !strings.HasSuffix(urls[0], "/main.go") || !strings.Contains(urls[1], dir) || strings.HasSuffix(urls[1], "main.go") {
		t.Fatalf("opened %v", urls)
	}

	// The preview's own menu copies what the viewer holds.
	if err := tt.RightClick("Preview actions: main.go"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tt.Menu(), "Copy contents") {
		t.Fatalf("the preview menu reads %v", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Copy contents"); err != nil {
		t.Fatal(err)
	}
	if copied != "package main" {
		t.Fatalf("copied %q", copied)
	}
}

// TestCopyFilesContentsTrimsAndWaits copies what a clipped viewer holds
// and refuses a file that has not arrived.
func TestCopyFilesContentsTrimsAndWaits(t *testing.T) {
	a := newTestApp(t)
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	codingModule(a).Files = &filesState{Path: "big.txt", Content: strings.Repeat("x", filesShown), Clipped: true}
	a.copyFilesContents()
	if len(copied) != filesShown {
		t.Fatalf("copied %d bytes", len(copied))
	}
	if len(a.layout.toasts) != 1 || !strings.Contains(a.layout.toasts[0].Title, "256 KB") {
		t.Fatalf("the cut is not said: %+v", a.layout.toasts)
	}
	copied = ""
	codingModule(a).Files.Content = "Reading…"
	a.copyFilesContents()
	if copied != "" {
		t.Fatalf("a reading file copied %q", copied)
	}
	codingModule(a).Files.Content = ""
	a.copyFilesContents()
	if copied != "" {
		t.Fatalf("an empty file copied %q", copied)
	}
}

// TestFilesViewerClosesAndShowsInTree unfolds a read file's folder to put
// the tree in view, and the cross closes the viewer on its own.
func TestFilesViewerClosesAndShowsInTree(t *testing.T) {
	a := newTestApp(t)
	a.openTool("Files")
	codingModule(a).Files = &filesState{
		Location: t.TempDir(),
		Open:     map[string]bool{},
		Entries: map[string][]workspace.FileEntry{
			".":   {{Path: "src", Name: "src", Directory: true}},
			"src": {{Path: "src/app.go", Name: "app.go"}},
		},
		Loading: map[string]bool{},
		Path:    "src/app.go",
		Content: "package main",
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Show in tree"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	st := codingModule(a).Files
	if st.Path != "" || st.Content != "" || st.Viewer != nil || !st.Open["src"] {
		t.Fatalf("Show in tree left %+v", st)
	}
	if !tt.HasText("app.go") {
		t.Fatalf("the tree does not show the file: %v", tt.Texts())
	}
	st.Path, st.Content = "src/app.go", "package main"
	tt.Frame()
	if err := tt.Click("Close file preview"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.Path != "" || st.Content != "" || st.FileErr != "" {
		t.Fatalf("the cross left %+v", st)
	}
}

// TestFilesShowInTreeClearsAQuery brings the tree back from a search.
func TestFilesShowInTreeClearsAQuery(t *testing.T) {
	a := newTestApp(t)
	codingModule(a).Files = &filesState{
		Open: map[string]bool{},
		Entries: map[string][]workspace.FileEntry{
			"src": {{Path: "src/app.go", Name: "app.go"}},
		},
		Path:    "src/app.go",
		Query:   "app",
		Finding: true,
		Results: []workspace.FileEntry{{Path: "src/app.go", Name: "app.go"}},
	}
	a.filesShowInTree()
	st := codingModule(a).Files
	if st.Query != "" || st.Finding || st.Results != nil {
		t.Fatalf("the search stayed over the tree: %+v", st)
	}
	if !st.Open["src"] || st.Path != "" {
		t.Fatalf("the tree did not unfold: %+v", st)
	}
}

// TestFilesSearchKeyboard walks the matches with the arrows, opens one
// with Enter, and makes a changed query search again rather than reopen
// the old choice.
func TestFilesSearchKeyboard(t *testing.T) {
	srv, _ := filesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []agent.Session{{ID: "ses_r", Title: "Remote turn", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_r")
	a.conversation.selected = "Remote turn"
	tt := ui.NewTester(a.view, 1400, 920)
	a.openTool("Files")
	tt.Frame()
	if err := tt.Click("Search files"); err != nil {
		t.Fatal(err)
	}
	tt.Type("notes")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	st := codingModule(a).Files
	if len(st.Results) != 2 || st.ResultsQuery != "notes" {
		t.Fatalf("the search reads %+v", st)
	}
	if st.Path != "" {
		t.Fatalf("the first Enter opened %q instead of searching", st.Path)
	}
	tt.Key(0, ui.KeyUp)
	if st.SearchSelected != 1 {
		t.Fatalf("Up from nothing chose %d", st.SearchSelected)
	}
	tt.Key(0, ui.KeyUp)
	if st.SearchSelected != 0 {
		t.Fatalf("a second Up chose %d", st.SearchSelected)
	}
	tt.Key(0, ui.KeyDown)
	if st.SearchSelected != 1 {
		t.Fatalf("Down chose %d", st.SearchSelected)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if st.Path != "src/deep/notes.txt" || !strings.Contains(st.Content, "a long line") {
		t.Fatalf("Enter opened %q with %q", st.Path, st.Content)
	}
	tt.Type("z")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if st.ResultsQuery != "notesz" {
		t.Fatalf("the changed query did not search: %+v", st)
	}
	if st.Path != "src/deep/notes.txt" {
		t.Fatalf("the changed query reopened %q", st.Path)
	}
}

// TestFilesWrapLongLines wraps and unwraps the viewer from its path row,
// and keeps the choice for the next file.
func TestFilesWrapLongLines(t *testing.T) {
	srv, _ := filesServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []agent.Session{{ID: "ses_r", Title: "Remote turn", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_r")
	a.conversation.selected = "Remote turn"
	a.openTool("Files")
	a.filesRead("src/deep/notes.txt")
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	st := codingModule(a).Files
	if st.Viewer == nil {
		t.Fatal("the read file built no viewer")
	}
	if codingModule(a).FilesWrap || st.Viewer.SoftWrap {
		t.Fatal("long lines started wrapped")
	}
	if err := tt.Click("Wrap long lines"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !codingModule(a).FilesWrap || !st.Viewer.SoftWrap {
		t.Fatalf("wrapping left %v/%v", codingModule(a).FilesWrap, st.Viewer.SoftWrap)
	}
	if err := tt.Click("Unwrap long lines"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if codingModule(a).FilesWrap || st.Viewer.SoftWrap {
		t.Fatalf("unwrapping left %v/%v", codingModule(a).FilesWrap, st.Viewer.SoftWrap)
	}
}
