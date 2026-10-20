package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestWorkspaceChipNamesFolderAndBranch shows the chat's folder and cached
// branch in the title bar, and opens Files from it.
func TestWorkspaceChipNamesFolderAndBranch(t *testing.T) {
	a := newTestApp(t)
	dir := filepath.Join(t.TempDir(), "wada")
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Alpha", Directory: dir}}
	a.conversation.target.selectSession("ses_a")
	a.conversation.selected = "Alpha"
	a.workspace.vcs = map[string]opencode.VCSInfo{
		vcsKey(a.connections.activeServer, dir): {Provider: "git", Branch: "improve-ui", Base: "main"},
	}
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	label := "Workspace wada on improve-ui"
	if _, ok := tt.Find(label); !ok {
		t.Fatalf("the workspace chip is missing: %v", tt.Texts())
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.layout.browserTool != "Files" || a.layout.browserCollapsed {
		t.Fatalf("the chip opened %q collapsed=%v", a.layout.browserTool, a.layout.browserCollapsed)
	}
}

// TestEnsureVcsReadsOnce caches a ready server's branch per folder.
func TestEnsureVcsReadsOnce(t *testing.T) {
	hits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/vcs", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"data":{"provider":"git","branch":{"current":"improve-ui","default":"main"}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.connections.serverHealth["srv-box"] = serverHealth{status: healthReady}
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Alpha", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_a")
	a.ensureVcs()
	if hits != 1 {
		t.Fatalf("the branch was asked for %d times", hits)
	}
	info := a.workspace.vcs[vcsKey("srv-box", "/srv/app")]
	if info.Provider != "git" || info.Branch != "improve-ui" {
		t.Fatalf("the branch cache reads %+v", info)
	}
	a.ensureVcs()
	if hits != 1 {
		t.Fatalf("the cached branch was read again: %d", hits)
	}
}

// TestEnsureVcsWaitsForHealth keeps a server that has not answered from
// being dialed for a decoration.
func TestEnsureVcsWaitsForHealth(t *testing.T) {
	hits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/vcs", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"data":{"provider":"git","branch":{"current":"main","default":"main"}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", name: "Build box", url: srv.URL})
	a.connections.activeServer = "srv-box"
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Alpha", Directory: "/srv/app"}}
	a.conversation.target.selectSession("ses_a")
	a.ensureVcs()
	if hits != 0 || len(a.workspace.vcs) != 0 {
		t.Fatalf("an unready server was read: %d hits, %+v", hits, a.workspace.vcs)
	}
}
