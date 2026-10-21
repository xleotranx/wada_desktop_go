package desktop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPolicyRefreshesEqualPathAcrossConnections(t *testing.T) {
	for _, fromLocal := range []bool{true, false} {
		t.Run(fmt.Sprintf("fromLocal=%t", fromLocal), func(t *testing.T) {
			a, m := regressionApp(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[]`) })
			remote := a.connections.activeServer
			a.conversation.draftProject = "/srv/app"
			if fromLocal {
				a.connections.activeServer = localServerID
			} else {
				a.connections.servers = append(a.connections.servers, serverConn{id: "previous", url: "http://127.0.0.1:1"})
				a.connections.activeServer = "previous"
			}
			codingModule(a).Policy.Dir, codingModule(a).Policy.Level = "/srv/app", "ask"
			codingModule(a).Policy.Route, codingModule(a).Policy.Remote = a.connections.route, !fromLocal
			previous := codingModule(a).Policy.Version
			a.connections.activeServer = remote
			a.connections.route++
			a.refreshWorkspacePolicy()
			if len(m.work) != 1 {
				t.Fatalf("equal path connection switch submitted %d policy reads, want 1", len(m.work))
			}
			if codingModule(a).Policy.Version == previous {
				t.Fatal("connection switch retained old policy identity")
			}
			target := <-codingModule(a).Policy.Dirs
			if target.Dir != "" {
				t.Fatalf("remote target left local watcher active in %q", target.Dir)
			}
			a.acceptPolicy(policyResult{Dir: "/srv/app", Level: "ask", Revision: previous})
			m.finish(t, 0)
			m.publish(t, 0)
			if codingModule(a).Policy.Level != "full" {
				t.Fatalf("new server policy = %q, want full", codingModule(a).Policy.Level)
			}
			a.acceptPolicy(policyResult{Dir: "/srv/app", Level: "ask", Revision: previous})
			if codingModule(a).Policy.Level != "full" {
				t.Fatal("stale local/previous server publication replaced remote policy")
			}
		})
	}
}

func TestPolicyReturningToLocalRejectsEarlierVisit(t *testing.T) {
	a := newTestApp(t)
	a.watchWorkspacePolicy("/repo/a")
	previous := codingModule(a).Policy.Version
	a.watchWorkspacePolicy("/repo/b")
	a.watchWorkspacePolicy("/repo/a")
	a.acceptPolicy(policyResult{Dir: "/repo/a", Level: "full", Revision: codingModule(a).Policy.Version})
	a.acceptPolicy(policyResult{Dir: "/repo/a", Level: "ask", Revision: previous})
	if codingModule(a).Policy.Level != "full" {
		t.Fatal("earlier visit to the same local directory replaced latest policy")
	}
}

func TestRemotePolicyWritesKeepSubmissionOrderAndLatestPublication(t *testing.T) {
	var mu sync.Mutex
	state := map[string]any{}
	var writes []string
	a, m := regressionApp(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/config":
			json.NewEncoder(w).Encode([]map[string]any{{"type": "document", "path": "/srv/app/opencode.json", "info": state}})
		case "/api/experimental/fs/write":
			state = map[string]any{}
			if err := json.NewDecoder(r.Body).Decode(&state); err != nil {
				t.Error(err)
			}
			writes = append(writes, shellGate(state))
		default:
			http.NotFound(w, r)
		}
	})
	a.conversation.draftProject = "/srv/app"
	codingModule(a).Policy.Dir, codingModule(a).Policy.Level = "/srv/app", "full"
	a.setAsk(true)
	a.setAsk(false)
	// Execute the second worker first. It must wait for its predecessor;
	// completing workers and publishing their results are separate operations.
	secondStarted := make(chan struct{})
	secondDone := make(chan func(), 1)
	go func() { close(secondStarted); secondDone <- m.work[1]() }()
	<-secondStarted
	select {
	case <-secondDone:
		t.Fatal("second write finished before its predecessor ran")
	case <-time.After(50 * time.Millisecond):
	}
	m.finish(t, 0)
	select {
	case m.done[1] = <-secondDone:
	case <-time.After(3 * time.Second):
		t.Fatal("second write did not resume after predecessor finished")
	}
	m.work[1] = nil
	m.publish(t, 1)
	m.publish(t, 0)
	if codingModule(a).Policy.Level != "full" {
		t.Fatalf("older completion replaced latest policy with %q", codingModule(a).Policy.Level)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 2 || writes[0] != "ask" || writes[1] != "full" {
		t.Fatalf("server policy write order = %v, want [ask full]", writes)
	}
}

func TestDismissedAttachmentRejectsPostedQuote(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote=%t", remote), func(t *testing.T) {
			a, m := regressionApp(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "quoted contents") })
			a.conversation.draftProject = "/srv/app"
			file := "readme.txt"
			if !remote {
				a.connections.activeServer = localServerID
				file = filepath.Join(t.TempDir(), "readme.txt")
				if err := os.WriteFile(file, []byte("quoted contents"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			a.conversation.draft = "Keep this draft"
			a.layout.attach = &attachState{open: true, draft: file}
			a.attachFile()
			m.finish(t, 0)
			a.layout.attach.open = false
			m.publish(t, 0)
			if a.conversation.draft != "Keep this draft" {
				t.Fatalf("dismissed dialog changed draft: %q", a.conversation.draft)
			}
		})
	}
}
