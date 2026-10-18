package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egoist/mygo/ui"
)

// manualBackground keeps both worker execution and UI publication under the
// test's control. A completed worker can wait while newer actions run.
type manualBackground struct {
	work []func() func()
	done []func()
}

func (m *manualBackground) submit(work func() func()) {
	m.work = append(m.work, work)
	m.done = append(m.done, nil)
}

func (m *manualBackground) finish(t *testing.T, index int) {
	t.Helper()
	if index >= len(m.work) || m.work[index] == nil {
		t.Fatalf("background task %d is unavailable (%d submitted)", index, len(m.work))
	}
	m.done[index] = m.work[index]()
	m.work[index] = nil
}

func (m *manualBackground) publish(t *testing.T, index int) {
	t.Helper()
	if index >= len(m.done) || m.done[index] == nil {
		t.Fatalf("background task %d has no completed result", index)
	}
	done := m.done[index]
	m.done[index] = nil
	done()
}

func regressionApp(t *testing.T, handler http.HandlerFunc) (*app, *manualBackground) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := newTestApp(t)
	a.servers = append(a.servers, serverConn{id: "srv-regression", name: "Regression server", url: srv.URL})
	a.activeServer = "srv-regression"
	m := &manualBackground{}
	a.background = m.submit
	return a, m
}

func TestBrowseLocationRejectsClosedDialogResult(t *testing.T) {
	a, m := regressionApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/location" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"directory":"/srv/old-dialog"}`)
	})
	a.browseServerFolders(true)
	m.finish(t, 0)
	a.browse = nil
	a.browseServerFolders(true)
	current := a.browse
	m.publish(t, 0)
	if a.browse != current || current.location != "" || !current.loading {
		t.Fatalf("closed dialog's location changed its replacement: %+v", a.browse)
	}
}

func TestBrowseListingRejectsClosedDialogResult(t *testing.T) {
	a, m := regressionApp(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"path":"old-dialog.txt","type":"file"}]}`)
	})
	a.draftProject = "/srv/app"
	a.browseServerFolders(true)
	m.finish(t, 0)
	a.browse = nil
	a.browseServerFolders(true)
	current := a.browse
	m.publish(t, 0)
	if a.browse != current || len(current.entries) != 0 || !current.loading {
		t.Fatalf("closed dialog's listing changed its replacement: %+v", a.browse)
	}
}

func TestBrowseListingRejectsRepeatedPathResult(t *testing.T) {
	entry := "old.txt"
	a, m := regressionApp(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"data":[{"path":%q,"type":"file"}]}`, entry)
	})
	a.draftProject = "/srv/app"
	a.browseServerFolders(true)
	m.finish(t, 0)
	a.browse.path = "/srv/app/other"
	a.browseGo()
	a.browse.path = "/srv/app"
	a.browseGo()
	entry = "current.txt"
	m.finish(t, 2)
	m.publish(t, 2)
	m.publish(t, 0)
	if got := a.browse.entries; len(got) != 1 || got[0].Name != "current.txt" {
		t.Fatalf("first visit replaced the latest visit to the same folder: %+v", got)
	}
}

func TestFilesReadRejectsRepeatedPathResult(t *testing.T) {
	content := "old contents"
	a, m := regressionApp(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, content)
	})
	a.files = &filesState{location: "/srv/app"}
	a.filesRead("a.txt")
	m.finish(t, 0)
	a.filesRead("b.txt")
	a.filesRead("a.txt")
	content = "current contents"
	m.finish(t, 2)
	m.publish(t, 2)
	m.publish(t, 0)
	if a.files.content != "current contents" {
		t.Fatalf("first visit replaced the latest file contents: %q", a.files.content)
	}
}

func TestFilesFindRejectsRepeatedQueryResult(t *testing.T) {
	entry := "old.txt"
	a, m := regressionApp(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"data":[{"path":%q,"type":"file"}]}`, entry)
	})
	a.files = &filesState{location: "/srv/app", query: "a"}
	a.filesFind()
	m.finish(t, 0)
	a.files.query = "b"
	a.filesFind()
	a.files.query = "a"
	a.filesFind()
	entry = "current.txt"
	m.finish(t, 2)
	m.publish(t, 2)
	m.publish(t, 0)
	if got := a.files.results; len(got) != 1 || got[0].Name != "current.txt" {
		t.Fatalf("first search replaced the latest search for the same query: %+v", got)
	}
}

func TestChangesRejectsRepeatedModeResult(t *testing.T) {
	file := "old.go"
	a, m := regressionApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/vcs" {
			fmt.Fprint(w, `{"data":{"provider":"git","branch":{"current":"feature","default":"main"}}}`)
			return
		}
		fmt.Fprintf(w, `{"data":[{"file":%q,"status":"modified","patch":"old","additions":1}]}`, file)
	})
	a.draftProject = "/srv/app"
	a.openChanges(changesWorking)
	m.finish(t, 0)
	a.openChanges(changesBranch)
	a.openChanges(changesWorking)
	file = "current.go"
	m.finish(t, 2)
	m.publish(t, 2)
	m.publish(t, 0)
	if got := a.changes.files; len(got) != 1 || got[0].File != "current.go" {
		t.Fatalf("first read replaced the latest changes in the same mode: %+v", got)
	}
}

func TestReplacingRequestCancelsWorkerBeforePublication(t *testing.T) {
	a := newTestApp(t)
	m := &manualBackground{}
	a.background = m.submit
	var request requestState
	var firstContext context.Context
	value := 0
	runRequest(a, &request, func(ctx context.Context) (int, error) {
		firstContext = ctx
		return 1, nil
	}, func(result int, _ error) { value = result })
	m.finish(t, 0)
	runRequest(a, &request, func(context.Context) (int, error) {
		return 2, nil
	}, func(result int, _ error) { value = result })
	if firstContext.Err() != context.Canceled {
		t.Fatal("replacing a request did not cancel its worker")
	}
	m.finish(t, 1)
	m.publish(t, 1)
	m.publish(t, 0)
	if value != 2 {
		t.Fatalf("replaced worker published %d, want 2", value)
	}
}

func TestShutdownCancelsWorkAndRejectsPostedResult(t *testing.T) {
	a := newTestApp(t)
	m := &manualBackground{}
	a.background = m.submit
	var workerContext context.Context
	applied := false
	inBackground(a, func(ctx context.Context) (int, error) {
		workerContext = ctx
		return 1, nil
	}, func(int, error) { applied = true })
	m.finish(t, 0)
	a.closeWork()
	if workerContext.Err() != context.Canceled {
		t.Fatal("shutdown left the worker running")
	}
	m.publish(t, 0)
	if applied {
		t.Fatal("shutdown accepted a result posted before closing")
	}
}

func TestReconnectCancelsActiveWorkButKeepsRegistryWork(t *testing.T) {
	a := newTestApp(t)
	m := &manualBackground{}
	a.background = m.submit
	var activeContext, registryContext context.Context
	activeApplied, registryApplied := false, false
	inBackground(a, func(ctx context.Context) (int, error) {
		activeContext = ctx
		return 1, nil
	}, func(int, error) { activeApplied = true })
	var probe requestState
	runOwned(a, &probe, a.lifetime, func(ctx context.Context) (int, error) {
		registryContext = ctx
		return 1, nil
	}, func(int, error) { registryApplied = true })
	m.finish(t, 0)
	m.finish(t, 1)
	a.reconnect()
	if activeContext.Err() != context.Canceled {
		t.Fatal("reconnect left the old connection's work running")
	}
	if registryContext.Err() != nil {
		t.Fatal("reconnect canceled registry work for an independent server")
	}
	m.publish(t, 0)
	m.publish(t, 1)
	if activeApplied || !registryApplied {
		t.Fatalf("after reconnect: active applied=%v, registry applied=%v", activeApplied, registryApplied)
	}
}

func TestWorkspaceToolRebuildDoesNotSubmitReads(t *testing.T) {
	for _, tool := range []string{"Files", "Changes"} {
		for _, state := range []string{"unopened", "loading", "folderless"} {
			t.Run(tool+"/"+state, func(t *testing.T) {
				a := newTestApp(t)
				m := &manualBackground{}
				a.background = m.submit
				a.servers = append(a.servers, serverConn{id: "box", name: "Box", url: "http://127.0.0.1:1"})
				a.activeServer = "box"
				if state != "folderless" {
					a.draftProject = "/srv/app"
				}
				if state == "unopened" {
					a.browserCollapsed, a.browserTool = false, tool
				} else {
					a.openTool(tool)
				}
				before := len(m.work)
				tt := ui.NewTester(a.view, 1400, 920)
				tt.Frame()
				tt.Frame()
				if len(m.work) != before {
					t.Fatalf("rebuilding %s submitted %d reads", tool, len(m.work)-before)
				}
			})
		}
	}
}
