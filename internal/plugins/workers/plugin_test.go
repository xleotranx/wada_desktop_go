package workers

import (
	"context"
	"errors"
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
}

func (f *fakeOrchService) NewPlugin() kernel.Plugin { return &fakeOrchPlugin{f: f} }

type fakeOrchPlugin struct {
	f *fakeOrchService
}

func (p *fakeOrchPlugin) Manifest() kernel.Manifest {
	base := p.f.Manifest()
	base.Provides = append(base.Provides, agent.HarnessesKey, agent.WorkersKey)
	return base
}

func (p *fakeOrchPlugin) Setup(c *kernel.Context) error {
	if err := p.f.Setup(c); err != nil {
		return err
	}
	if err := kernel.Provide[agent.Harnesses](c, agent.HarnessesKey, p.f); err != nil {
		return err
	}
	return kernel.Provide[agent.Workers](c, agent.WorkersKey, p.f)
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

func TestWorkPanelDisplaysLiveWorkers(t *testing.T) {
	h, p, _, _ := startTestPlugin(t)

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
	waitCond(t, h, "the worker is listed", func() bool { return len(p.workers) == 1 })

	if !p.workShown() {
		t.Fatal("expected work panel to be shown")
	}

	tt := ui.NewTester(p.workPanelView, 320, 480)
	tt.Frame()
	for _, want := range []string{"Auth worker", "Running", "wada/auth-1234"} {
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

func TestHarnessChatsTakeTextOnly(t *testing.T) {
	h, p, _, _ := startTestPlugin(t)

	// A new chat on Claude Code refuses files and skills.
	if _, _, err := p.textOnly(chatcontract.Draft{Provider: "claude", Text: "hi", Files: []agent.File{{Name: "a.png"}}}); err == nil || !strings.Contains(err.Error(), "Claude Code takes text only") {
		t.Fatalf("new harness chat with a file = %v", err)
	}
	if _, _, err := p.textOnly(chatcontract.Draft{Provider: "claude", Text: "hi"}); err != nil {
		t.Fatalf("plain text refused: %v", err)
	}

	// A stored one is recognized by its conversation.
	kernel.Emit(h, chatcontract.Changed, chatcontract.Conversation{Session: "w1", Directory: "/repo", Provider: "claude"})
	if _, _, err := p.textOnly(chatcontract.Draft{Session: "w1", Text: "hi", Skills: []agent.SkillRef{{ID: "review"}}}); err == nil {
		t.Fatal("stored harness chat with a skill was accepted")
	}

	// PiG conversations take everything.
	kernel.Emit(h, chatcontract.Changed, chatcontract.Conversation{Session: "s1", Directory: "/repo", Provider: "pig"})
	if _, _, err := p.textOnly(chatcontract.Draft{Session: "s1", Text: "hi", Files: []agent.File{{Name: "a.png"}}}); err != nil {
		t.Fatalf("PiG draft refused: %v", err)
	}
}

func TestStoppingWorkersIsIndependentAndReported(t *testing.T) {
	h, p, fa, _ := startTestPlugin(t)
	kernel.Emit(h, chatcontract.Changed, chatcontract.Conversation{Session: "chief1", Directory: "/repo"})

	p.stopWorker("wrk_1")
	p.stopWorker("wrk_2")
	waitCond(t, h, "both aborts sent", func() bool {
		return fa.Called("abort wrk_1") == 1 && fa.Called("abort wrk_2") == 1
	})

	fa.Fail["Abort"] = errors.New("worker is not responding")
	p.stopWorker("wrk_3")
	host := p.host.(*kittest.Host)
	waitCond(t, h, "stop failure reported", func() bool {
		return len(host.Toasts) > 0 && host.Toasts[len(host.Toasts)-1] == "Could not stop the agent"
	})
}
