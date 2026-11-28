package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/protocol"
)

func TestSessionsArePlacedInWorkspaces(t *testing.T) {
	dataDir := t.TempDir()
	_, _, _, _ = startTestServer(t, dataDir)
	c, err := client.DialLocal(t.Context(), dataDir, client.Options{ClientType: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, events, err := c.Subscribe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	model := protocol.ModelRef{Provider: "wada-test", ID: "test-model"}

	// A session in a new directory gets a project of its own.
	cliDir := t.TempDir()
	s1, err := c.Create(t.Context(), protocol.CreateSessionRequest{Directory: cliDir, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if s1.Workspace == "" {
		t.Fatal("created session has no workspace")
	}
	ev := nextWorkspaceEvent(t, events)
	if ev.Project == nil || ev.Project.Root != cliDir {
		t.Fatalf("first event = %+v, want the new project", ev)
	}

	// Creating by workspace uses its directory.
	appDir := filepath.Join(t.TempDir(), "app")
	if err := os.Mkdir(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	added, err := c.AddProject(t.Context(), appDir)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := c.Create(t.Context(), protocol.CreateSessionRequest{Workspace: added.Workspace.ID, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if s2.Directory != added.Workspace.Directory || s2.Workspace != added.Workspace.ID {
		t.Fatalf("session = %+v, want it in %+v", s2, added.Workspace)
	}
	if _, err := c.Create(t.Context(), protocol.CreateSessionRequest{Workspace: added.Workspace.ID, Directory: cliDir, Model: model}); !isCode(err, protocol.ErrCodeInvalid) {
		t.Fatalf("workspace and another directory = %v, want invalid", err)
	}

	// No directory is the inbox.
	s3, err := c.Create(t.Context(), protocol.CreateSessionRequest{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := c.Projects(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	inbox := ps[len(ps)-1]
	ws, err := c.Workspaces(t.Context(), protocol.WorkspacesListRequest{Project: inbox.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !inbox.Inbox || len(ws) != 1 || s3.Workspace != ws[0].ID {
		t.Fatalf("inbox %+v workspaces %+v, session %+v", inbox, ws, s3)
	}

	// Listing filters by workspace and places every session.
	page, err := c.Sessions(t.Context(), protocol.SessionQuery{Workspace: added.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].ID != s2.ID || page.Sessions[0].Workspace != added.Workspace.ID {
		t.Fatalf("workspace listing = %+v", page.Sessions)
	}

	// Archiving keeps the sessions placed in the archived workspace.
	if err := c.ArchiveProject(t.Context(), added.Project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sessions(t.Context(), protocol.SessionQuery{Workspace: added.Workspace.ID}); !isCode(err, protocol.ErrCodeNotFound) {
		t.Fatalf("listing an archived workspace = %v, want not found", err)
	}
	all, err := c.Sessions(t.Context(), protocol.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all.Sessions {
		if s.ID == s2.ID && s.Workspace != added.Workspace.ID {
			t.Fatalf("archived workspace's session = %+v", s)
		}
	}
	if got, err := c.Projects(t.Context(), false); err != nil || len(got) != 2 {
		t.Fatalf("active projects = %+v %v, want the CLI one and the inbox", got, err)
	}
}

func nextWorkspaceEvent(t *testing.T, events <-chan protocol.Event) protocol.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Kind == protocol.EventWorkspaceUpdated {
				return ev
			}
		case <-timeout:
			t.Fatal("no workspace_updated event")
		}
	}
}

func isCode(err error, code string) bool {
	var rpcErr *protocol.RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == code
}
