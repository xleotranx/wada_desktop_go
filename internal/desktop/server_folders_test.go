package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// browseServer is a fake server whose file system the browse walks: one
// directory per key, its children spelled relative to the listing's
// location the way opencode spells them.
func browseServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	listed := &[]string{}
	files := map[string][]string{
		"/srv/work": {`{"path":"proj/","type":"directory"}`,
			`{"path":"other/","type":"directory"}`,
			`{"path":"notes.md","type":"file"}`},
		"/srv/work/proj": {`{"path":"proj/inner/","type":"directory"}`,
			`{"path":"proj/go.mod","type":"file"}`},
		"/srv/work/other":      {`{"path":"other/thing/","type":"directory"}`},
		"/srv/work/proj/inner": {},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/location", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"directory":"/srv/work","project":{"id":"p1","directory":"/srv/work","canonical":"/srv/work"}}`))
	})
	mux.HandleFunc("GET /api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		location, rel := q.Get("location[directory]"), q.Get("path")
		*listed = append(*listed, location+" | "+rel)
		list, ok := files[path.Join(location, rel)]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"_tag":"InvalidRequestError","message":"the folder is not there"}`))
			return
		}
		w.Write([]byte(`{"location":{"directory":"` + location + `"},"data":[` + strings.Join(list, ",") + `]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, listed
}

// TestListDir reads one directory the way the browse asks for it: the
// location rides the query, a relative path walks from it, and folders
// come back as folders.
func TestListDir(t *testing.T) {
	srv, listed := browseServer(t)
	ag := &opencode.Client{Base: srv.URL + "/api", HTTP: srv.Client()}
	ctx := context.Background()

	entries, err := ag.ListDir(ctx, "/srv/work", ".")
	if err != nil || len(entries) != 3 {
		t.Fatalf("list %+v %v", entries, err)
	}
	if entries[0].Path != "proj" || entries[0].Name != "proj" || !entries[0].Directory {
		t.Fatalf("the folder row reads %+v", entries[0])
	}
	if entries[2].Path != "notes.md" || entries[2].Directory {
		t.Fatalf("the file row reads %+v", entries[2])
	}
	// A walk below the location spells its own path; the location stays.
	entries, err = ag.ListDir(ctx, "/srv/work", "proj")
	if err != nil || len(entries) != 2 || entries[0].Path != "proj/inner" || !entries[0].Directory {
		t.Fatalf("nested list %+v %v", entries, err)
	}
	if got := (*listed)[len(*listed)-1]; got != "/srv/work | proj" {
		t.Fatalf("the nested ask reads %q", got)
	}
	if _, err := ag.ListDir(ctx, "/srv/work", "nowhere"); err == nil ||
		!strings.Contains(err.Error(), "not there") {
		t.Fatalf("a missing folder reads %v", err)
	}

	dir, err := ag.Location(ctx, "")
	if err != nil || dir != "/srv/work" {
		t.Fatalf("location %q %v", dir, err)
	}
}

// TestBrowseTheServersFolders walks the dialog a remote draft's picker
// opens: the browse starts on the server's own folder, descends, steps
// back up, jumps to a typed path, and settles the draft's project — the
// location it names never moving, so looking around files no project row.
func TestBrowseTheServersFolders(t *testing.T) {
	srv, listed := browseServer(t)
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.layout.settingsOpen = false
	tt := ui.NewTester(a.view, 1400, 920)

	a.browseServerFolders(true)
	if a.layout.browse == nil || !a.layout.browse.open {
		t.Fatal("the browse did not open")
	}
	if a.layout.browse.location != "/srv/work" || a.layout.browse.path != "/srv/work" || len(a.layout.browse.entries) != 3 {
		t.Fatalf("the browse opened on %+v", a.layout.browse)
	}
	tt.Frame()
	if !tt.HasText("Browse the server") || !tt.HasText("proj") || !tt.HasText("notes.md") {
		t.Fatalf("the dialog is not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Open proj"); err != nil {
		t.Fatal(err)
	}
	if a.layout.browse.path != "/srv/work/proj" || len(a.layout.browse.entries) != 2 {
		t.Fatalf("the folder did not open: %+v", a.layout.browse)
	}
	if err := tt.Click("Up one folder"); err != nil {
		t.Fatal(err)
	}
	if a.layout.browse.path != "/srv/work" || len(a.layout.browse.entries) != 3 {
		t.Fatalf("stepping up landed on %+v", a.layout.browse)
	}
	a.layout.browse.path = "/srv/work/other"
	tt.Frame()
	if err := tt.Click("Go to the folder"); err != nil {
		t.Fatal(err)
	}
	if a.layout.browse.path != "/srv/work/other" || a.layout.browse.location != "/srv/work" {
		t.Fatalf("the typed path landed on %+v", a.layout.browse)
	}
	tt.Frame()
	if err := tt.Click("Work here"); err != nil {
		t.Fatal(err)
	}
	if a.layout.browse != nil || a.conversation.draftProject != "/srv/work/other" {
		t.Fatalf("the draft landed in %q (browse %+v)", a.conversation.draftProject, a.layout.browse)
	}
	for _, ask := range *listed {
		if !strings.HasPrefix(ask, "/srv/work | ") {
			t.Fatalf("the browse named another location: %q", ask)
		}
	}

	// The sidebar's Add folder opens where the user already is — the
	// draft's folder — and its pick starts a chat there.
	a.browseServerFolders(false)
	if a.layout.browse.path != "/srv/work/other" {
		t.Fatalf("the sidebar browse opened on %+v", a.layout.browse)
	}
	version := a.conversation.target.revision
	tt.Frame()
	if err := tt.Click("Start a chat here"); err != nil {
		t.Fatal(err)
	}
	if a.layout.browse != nil || a.conversation.draftProject != "/srv/work/other" || a.conversation.target.revision != version+1 {
		t.Fatalf("the sidebar's pick left %q / version %d", a.conversation.draftProject, a.conversation.target.revision)
	}

	// A folder the server does not have says so and refuses the pick.
	a.browseServerFolders(false)
	a.layout.browse.path = "/srv/nowhere"
	tt.Frame()
	if err := tt.Click("Go to the folder"); err != nil {
		t.Fatal(err)
	}
	if a.layout.browse.note == "" || a.layout.browse.entries != nil {
		t.Fatalf("a missing folder reads %+v", a.layout.browse)
	}
	tt.Frame()
	if err := tt.Click("Start a chat here"); err == nil && a.layout.browse == nil {
		t.Fatal("a folder that did not open was picked anyway")
	}
}

// TestBrowseOnTheLocalServerUsesThePanel keeps the system's own folder
// chooser for the server the app runs here: the server's disk is this
// machine's, so the panel hands the folder over and the draft settles on
// it without any listing.
func TestBrowseOnTheLocalServerUsesThePanel(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	picked := false
	a.runtime.pickFolder = func(done func(path string)) {
		picked = true
		done(dir)
	}
	a.browseServerFolders(true)
	if !picked || a.layout.browse != nil {
		t.Fatalf("the local browse read %v / %+v", picked, a.layout.browse)
	}
	if a.layout.selectAddedProject {
		t.Fatal("the panel's pick left the draft flag set")
	}
	if a.conversation.draftProject != canonDir(dir) {
		t.Fatalf("the draft landed in %q, want %q", a.conversation.draftProject, canonDir(dir))
	}
}
