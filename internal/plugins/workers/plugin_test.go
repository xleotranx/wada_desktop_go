package workers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/agent"
	chatcontract "wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"
)

type fakeOrchService struct {
	*plugintest.Agent
	harnesses []agent.Harness
	workers   []agent.Worker
	worktrees []agent.Worktree
	prs       map[string]*agent.PRStatus
	removedWT string
}

func (f *fakeOrchService) NewPlugin() kernel.Plugin { return &fakeOrchPlugin{f: f} }

type fakeOrchPlugin struct {
	f *fakeOrchService
}

func (p *fakeOrchPlugin) Manifest() kernel.Manifest {
	base := p.f.Manifest()
	base.Provides = append(base.Provides, agent.HarnessesKey, agent.WorkersKey, agent.WorktreesKey)
	return base
}

func (p *fakeOrchPlugin) Setup(c *kernel.Context) error {
	if err := p.f.Setup(c); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Harnesses](c, agent.HarnessesKey, p.f); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Workers](c, agent.WorkersKey, p.f); err != nil {
		return err
	}
	return kernel.Provide[agent.Worktrees](c, agent.WorktreesKey, p.f)
}

func (f *fakeOrchService) Harnesses(context.Context) ([]agent.Harness, error) {
	return f.harnesses, nil
}

func (f *fakeOrchService) Workers(_ context.Context, parent agent.SessionID) ([]agent.Worker, error) {
	var out []agent.Worker
	for _, w := range f.workers {
		if parent == "" || w.ParentSession == parent {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeOrchService) ListWorktrees(context.Context, string) ([]agent.Worktree, error) {
	return f.worktrees, nil
}

func (f *fakeOrchService) CreateWorktree(_ context.Context, dir, name, _ string) (agent.Worktree, error) {
	return agent.Worktree{RepoRoot: dir, Path: "/tmp/wt/" + name, Branch: "wada/" + name}, nil
}

func (f *fakeOrchService) RemoveWorktree(_ context.Context, _, path string, _ bool) error {
	f.removedWT = path
	return nil
}

func (f *fakeOrchService) PRStatus(_ context.Context, dir string) (*agent.PRStatus, error) {
	if f.prs == nil {
		return nil, nil
	}
	return f.prs[dir], nil
}

func waitCond(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}

func startTestPlugin(t *testing.T) (*kerneltest.Harness, *Plugin, *fakeOrchService, *plugintest.Chat) {
	t.Helper()
	fa := &fakeOrchService{
		Agent: plugintest.NewAgent(),
		harnesses: []agent.Harness{
			{ID: "pig", Name: "PiG (Chief Agent)", Kind: agent.HarnessEmbedded, Available: true},
			{ID: "claude", Name: "Claude Code", Kind: agent.HarnessClaude, Binary: "claude", Available: true},
			{ID: "codex", Name: "Codex", Kind: agent.HarnessCodex, Binary: "codex", Available: false},
		},
	}
	fa.State = agent.Status{State: agent.Ready, Attempt: 1}
	chatFake := &plugintest.Chat{}
	host := &kittest.Host{Data: t.TempDir()}
	var p *Plugin
	h := kerneltest.New(t, host.New, fa.NewPlugin, chatFake.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	waitCond(t, h, "harnesses loaded", func() bool { return len(p.harnesses) == 3 })
	return h, p, fa, chatFake
}

func TestHarnessPickerStampsNewChatProvider(t *testing.T) {
	h, p, _, _ := startTestPlugin(t)

	tt := ui.NewTester(p.harnessPickerView, 400, 120)
	tt.Frame()
	if !tt.HasText("PiG (Chief Agent)") {
		t.Fatalf("expected harness picker label, got %q", tt.Texts())
	}

	// Unavailable harness rejects on outgoing transform.
	p.selectedProvider = "codex"
	if _, _, err := p.outgoing(chatcontract.Draft{Directory: "/repo", Text: "hi"}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("expected unavailable error, got %v", err)
	}

	// Available harness stamps Provider on new drafts, leaves stored sessions untouched.
	p.selectedProvider = "claude"
	out, _, err := p.outgoing(chatcontract.Draft{Directory: "/repo", Text: "hi"})
	if err != nil || out.Provider != "claude" {
		t.Fatalf("outgoing new = %+v, err=%v", out, err)
	}
	stored, _, err := p.outgoing(chatcontract.Draft{Session: "s1", Directory: "/repo", Text: "hi"})
	if err != nil || stored.Provider != "" {
		t.Fatalf("outgoing stored = %+v, err=%v", stored, err)
	}

	// Picker hides once a stored conversation is active.
	kernel.Emit(h, chatcontract.Changed, chatcontract.Conversation{Session: "s1", Directory: "/repo"})
	tt.Frame()
	if tt.HasText("PiG (Chief Agent)") {
		t.Fatalf("picker stayed visible on stored session: %q", tt.Texts())
	}
}

func TestWorkPanelDisplaysLiveWorkersAndWorktrees(t *testing.T) {
	h, p, fa, _ := startTestPlugin(t)
	fa.worktrees = []agent.Worktree{
		{RepoRoot: "/repo", Path: "/repo", Branch: "main", IsMain: true},
		{RepoRoot: "/repo", Path: "/tmp/wt/auth", Branch: "wada/auth-1234", Dirty: true},
	}
	fa.prs = map[string]*agent.PRStatus{
		"/tmp/wt/auth": {Number: 42, State: "OPEN", Checks: "pass", URL: "https://github.com/xleotranx/wada_desktop_go/pull/42"},
	}

	kernel.Emit(h, chatcontract.Changed, chatcontract.Conversation{Session: "chief1", Directory: "/repo"})
	kernel.Emit(h, agent.WorkerUpdated, agent.Worker{
		ID:            "wrk_1",
		ParentSession: "chief1",
		Provider:      "claude",
		Name:          "Auth worker",
		Directory:     "/tmp/wt/auth",
		Branch:        "wada/auth-1234",
		WorktreePath:  "/tmp/wt/auth",
		Status:        agent.WorkerRunning,
	})
	waitCond(t, h, "worktrees loaded", func() bool { return len(p.worktrees) == 2 && p.worktrees[1].PR != nil })

	if !p.workShown() {
		t.Fatal("expected work panel to be shown")
	}

	tt := ui.NewTester(p.workPanelView, 320, 480)
	tt.Frame()
	for _, want := range []string{"Auth worker", "Running", "wada/auth-1234", "modified", "#42 · pass"} {
		if !tt.HasText(want) {
			t.Fatalf("work panel missing %q in %q", want, tt.Texts())
		}
	}
}

func TestWorkerDrawerInspectsTranscriptAndDiffAndStops(t *testing.T) {
	h, p, fa, _ := startTestPlugin(t)
	fa.Stored["wrk_1"] = []agent.Message{
		{ID: "m1", Role: agent.RoleUser, Text: "Implement auth"},
		{ID: "m2", Role: agent.RoleAssistant, Parts: []agent.Part{
			{Kind: agent.PartTool, Tool: &agent.Tool{Name: "Edit", Command: "auth.go", Status: "completed"}},
			{Kind: agent.PartText, Text: "Added auth handler."},
		}},
	}
	fa.Changed["wrk_1"] = []agent.FileChange{
		{Path: "auth.go", Status: agent.FileAdded, Added: 2, Patch: "@@ -0,0 +1,2 @@\n+package auth\n+func Auth() {}"},
	}

	kernel.Emit(h, chatcontract.Changed, chatcontract.Conversation{Session: "chief1", Directory: "/repo"})
	kernel.Emit(h, agent.WorkerUpdated, agent.Worker{
		ID:            "wrk_1",
		ParentSession: "chief1",
		Provider:      "claude",
		Name:          "Auth worker",
		Branch:        "wada/auth",
		Status:        agent.WorkerRunning,
	})

	p.openDrawer("wrk_1")
	waitCond(t, h, "drawer loaded", func() bool { return !p.drawerLoading && len(p.drawerMessages) == 2 })

	tt := ui.NewTester(p.drawerView, 400, 600)
	tt.Frame()
	for _, want := range []string{"Auth worker", "Implement auth", "auth.go", "Added auth handler."} {
		if !tt.HasText(want) {
			t.Fatalf("drawer transcript missing %q in %q", want, tt.Texts())
		}
	}

	p.drawerTab = tabDiff
	p.drawerOpenDiff["auth.go"] = true
	tt.Frame()
	if !tt.HasText("func Auth() {}") {
		t.Fatalf("drawer diff missing added line: %q", tt.Texts())
	}

	tt.Click("Stop")
	waitCond(t, h, "abort sent", func() bool { return fa.Called("abort wrk_1") == 1 })
}
