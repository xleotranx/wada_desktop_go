package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestWorkspaceToolsFollowDraftProject(t *testing.T) {
	for _, tool := range []string{"Files", "Changes"} {
		t.Run(tool, func(t *testing.T) {
			srv, _ := filesServer(t)
			if tool == "Changes" {
				// Changes asks the version-control endpoints instead.
				srv, _ = changesServer(t)
			}
			a := newTestApp(t)
			a.connections.servers = append(a.connections.servers, serverConn{id: "box", name: "Box", url: srv.URL})
			a.connections.activeServer = "box"
			a.scope().projects = []string{"/srv/app", "/srv/app/src"}
			a.startChatIn("/srv/app")
			a.openTool(tool)
			tt := ui.NewTester(a.view, 1400, 920)
			if err := tt.Click("Choose project"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("Use project src"); err != nil {
				t.Fatal(err)
			}
			assertDir := func(want string) {
				t.Helper()
				got := ""
				if tool == "Files" && codingModule(a).Files != nil {
					got = codingModule(a).Files.Location
				} else if tool == "Changes" && codingModule(a).Changes != nil {
					got = codingModule(a).Changes.Dir
				}
				if got != want {
					t.Fatalf("%s reads %q, want %q", tool, got, want)
				}
			}
			assertDir("/srv/app/src")
			a.reloadBrowserPage()
			assertDir("/srv/app/src")
			a.selectDraftProject("")
			tt.Frame()
			assertDir("")
			if !tt.HasText("no folder on the server") {
				t.Fatalf("folderless tool still shows old data: %v", tt.Texts())
			}
			a.startChatIn("/srv/app")
			assertDir("/srv/app")
			a.connections.servers = append(a.connections.servers, serverConn{id: "other", name: "Other", url: srv.URL})
			a.useServer("other")
			tt.Frame()
			assertDir("")
		})
	}
}
