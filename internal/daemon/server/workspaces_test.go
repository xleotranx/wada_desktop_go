package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/daemon/pig"
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

// fakeTerminals records the terminals it is asked to start and kill.
type fakeTerminals struct {
	*pig.Runtime
	mu   sync.Mutex
	list []protocol.TerminalInfo
	made []protocol.TerminalCreateRequest
}

func (f *fakeTerminals) ListTerminals(context.Context) ([]protocol.TerminalInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.list), nil
}

func (f *fakeTerminals) CreateTerminal(_ context.Context, req protocol.TerminalCreateRequest) (protocol.TerminalInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.made = append(f.made, req)
	info := protocol.TerminalInfo{ID: fmt.Sprintf("term_%d", len(f.made)), Name: req.Name, Directory: req.Directory,
		Running: true, Command: req.Command, Role: req.Role, CreatedAt: int64(len(f.made))}
	f.list = append(f.list, info)
	return info, nil
}

func (*fakeTerminals) AttachTerminal(context.Context, string, protocol.TerminalAttachRequest) (protocol.TerminalInfo, []byte, <-chan []byte, func(), error) {
	return protocol.TerminalInfo{}, nil, nil, nil, errors.New("not attached in this test")
}
func (*fakeTerminals) TerminalInput(context.Context, string, []byte) error    { return nil }
func (*fakeTerminals) TerminalResize(context.Context, string, int, int) error { return nil }
func (*fakeTerminals) CaptureTerminal(context.Context, string, int) (protocol.TerminalCaptureResponse, error) {
	return protocol.TerminalCaptureResponse{}, nil
}

func (f *fakeTerminals) KillTerminal(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i].Running = false
		}
	}
	return nil
}

func TestSetupAndServicesRunOnceTrusted(t *testing.T) {
	dataDir := t.TempDir()
	terms := &fakeTerminals{}
	startTestServer(t, dataDir, func(cfg *Config) {
		terms.Runtime = cfg.Backend.(*pig.Runtime)
		cfg.Backend = terms
	})
	c, err := client.DialLocal(t.Context(), dataDir, client.Options{ClientType: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wada.json"),
		[]byte(`{"setup": ["npm ci", "npm run build"], "services": {"web": "npm run dev"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := c.AddProject(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	id := added.Workspace.ID

	cfg, err := c.WorkspaceConfig(t.Context(), id)
	if err != nil || !cfg.Found || cfg.Trusted || len(cfg.Services) != 1 || cfg.Services[0].Port != 41000 {
		t.Fatalf("config %+v %v", cfg, err)
	}
	if _, err := c.RunSetup(t.Context(), id); !isCode(err, protocol.ErrCodeConflict) {
		t.Fatalf("untrusted setup = %v, want conflict", err)
	}
	if err := c.TrustWorkspaceConfig(t.Context(), id, cfg.Hash); err != nil {
		t.Fatal(err)
	}

	setup, err := c.RunSetup(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := c.RunSetup(t.Context(), id); again.ID != setup.ID {
		t.Fatalf("a running setup started again as %+v", again)
	}
	web, err := c.StartService(t.Context(), id, "web")
	if err != nil {
		t.Fatal(err)
	}
	terms.mu.Lock()
	made := slices.Clone(terms.made)
	terms.mu.Unlock()
	if len(made) != 2 || made[0].Command != "npm ci && npm run build" || made[0].Role != "setup" ||
		made[1].Command != "npm run dev" || made[1].Role != "service:web" || !slices.Contains(made[1].Env, "PORT=41000") ||
		made[1].Directory != dir {
		t.Fatalf("terminals made %+v", made)
	}

	cfg, _ = c.WorkspaceConfig(t.Context(), id)
	if cfg.SetupTerminal == nil || cfg.SetupTerminal.ID != setup.ID || cfg.Services[0].Terminal == nil ||
		cfg.Services[0].Terminal.ID != web.ID || !cfg.Services[0].Terminal.Running || cfg.Services[0].Terminal.Workspace != id {
		t.Fatalf("config after starting %+v", cfg)
	}
	if err := c.StopService(t.Context(), id, "web"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = c.WorkspaceConfig(t.Context(), id); cfg.Services[0].Terminal.Running {
		t.Fatal("the stopped service still runs")
	}
	if _, err := c.StartService(t.Context(), id, "nope"); !isCode(err, protocol.ErrCodeNotFound) {
		t.Fatalf("unknown service = %v", err)
	}
}
