package pig

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"wada-desktop-go/internal/plugintest"

	pa "github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/agent"
)

func waitFor(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}
func call[T any](t *testing.T, h *kerneltest.Harness, fn func(context.Context) (T, error)) T {
	t.Helper()
	done := make(chan struct{})
	var value T
	var err error
	go func() { value, err = fn(context.Background()); close(done) }()
	waitFor(t, h, "call", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func startPig(t *testing.T, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *ai.FauxProviderHandle) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var p *Plugin
	host := &kittest.Host{Data: t.TempDir()}
	h := kerneltest.New(t, append([]kernel.Factory{host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} })}, extra...)...)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	e, err := p.current()
	if err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "wada-test", Model: "test-model"})
	if err = e.models.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	if err = e.services.SettingsManager().SetDefaultModelAndProvider("wada-test", "test-model"); err != nil {
		t.Fatal(err)
	}
	return h, p, faux
}
func createChat(t *testing.T, h *kerneltest.Harness, p *Plugin, dir string) agent.Session {
	t.Helper()
	return call(t, h, func(ctx context.Context) (agent.Session, error) {
		return p.Create(ctx, agent.CreateSession{Directory: dir, Title: "Example", Model: agent.ModelRef{Provider: "wada-test", ID: "test-model"}})
	})
}
func TestSessionLifecycleAndIndependentChildren(t *testing.T) {
	h, p, _ := startPig(t)
	dir := t.TempDir()
	s := createChat(t, h, p, dir)
	if s.ID == "" {
		t.Fatal("missing identity")
	}
	page := call(t, h, func(ctx context.Context) (agent.SessionPage, error) { return p.Sessions(ctx, agent.SessionQuery{}) })
	if len(page.Sessions) != 1 || page.Sessions[0].ID != s.ID {
		t.Fatalf("sessions %+v", page)
	}
	child := call(t, h, func(ctx context.Context) (agent.Session, error) {
		return p.Spawn(ctx, agent.Spawn{Parent: s.ID, Name: "Planner", Directory: dir, Policy: agent.ToolPolicy{Deny: []agent.ToolKind{agent.ToolEdit}}})
	})
	if child.ID == s.ID {
		t.Fatal("child shares parent identity")
	}
	page = call(t, h, func(ctx context.Context) (agent.SessionPage, error) { return p.Sessions(ctx, agent.SessionQuery{}) })
	if len(page.Sessions) != 1 {
		t.Fatalf("children leaked %+v", page)
	}
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Rename(ctx, s.ID, "Renamed") })
	if err := h.Disable("pig"); err != nil {
		t.Fatal(err)
	}
	if err := h.Enable("pig"); err != nil {
		t.Fatal(err)
	}
	service, _ := kernel.Use(h, agent.ServiceKey)
	p = service.(*Plugin)
	waitFor(t, h, "restart", func() bool { return p.Status().State == agent.Ready })
	page = call(t, h, func(ctx context.Context) (agent.SessionPage, error) {
		return p.Sessions(ctx, agent.SessionQuery{Search: "Renamed"})
	})
	if len(page.Sessions) != 1 || page.Sessions[0].ID != s.ID {
		t.Fatalf("restart lost identity %+v", page)
	}
	call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	e, _ := p.current()
	c, _ := e.get(s.ID)
	c.mu.Lock()
	actual := c.session.ID()
	c.mu.Unlock()
	if actual != string(s.ID) {
		t.Fatalf("SDK restored %s instead of %s", actual, s.ID)
	}
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Delete(ctx, s.ID) })
}
func TestStreamingAndStoredAttachments(t *testing.T) {
	h, p, faux := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxThinking("Consider"), ai.FauxText("Hello")}}})
	var events []agent.Event
	kernel.On(h, agent.Events, func(batch []agent.Event) { events = append(events, batch...) })
	disposition := call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Hi", Files: []agent.File{{Name: "note.txt", MIME: "text/plain", Data: []byte("attached")}}})
	})
	if disposition != agent.SendAccepted {
		t.Fatalf("admission %v", disposition)
	}
	waitFor(t, h, "turn", func() bool {
		for _, ev := range events {
			if ev.Kind == agent.TurnFailed {
				t.Fatalf("turn failed: %s", ev.Error)
			}
			if ev.Kind == agent.TurnSucceeded {
				return true
			}
		}
		return false
	})
	messages := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	if len(messages) != 2 || messages[0].Text != "Hi" || len(messages[0].Files) != 1 || messages[0].Files[0].Name != "note.txt" {
		t.Fatalf("messages %+v", messages)
	}
	var text strings.Builder
	for _, part := range messages[1].Parts {
		if part.Kind == agent.PartText {
			text.WriteString(part.Text)
		}
	}
	if text.String() != "Hello" {
		t.Fatalf("reply %q", text.String())
	}
}
func TestMentionsStayInsideProject(t *testing.T) {
	h, p, _ := startPig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0600)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "private.txt"), []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(dir, "escape"))
	for _, path := range []string{"../private.txt", "/etc/passwd", "escape/private.txt"} {
		if _, err := p.Read(t.Context(), dir, path); err == nil {
			t.Fatalf("read escaped via %s", path)
		}
	}
	got := call(t, h, func(ctx context.Context) ([]byte, error) { return p.Read(ctx, dir, "hello.txt") })
	if string(got) != "hello" {
		t.Fatal(string(got))
	}
}

func TestToolApprovalAndSessionChanges(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("pre-existing edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := createChat(t, h, p, dir)
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "note.txt", "content": "updated\n"}, nil)}, StopReason: "toolUse"}, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done")}}})
	var approval *agent.Approval
	settled := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.ApprovalRequested {
				approval = ev.Approval
			}
			if ev.Kind == agent.TurnSucceeded {
				settled = true
			}
			if ev.Kind == agent.TurnFailed {
				t.Errorf("turn: %s", ev.Error)
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Update it"})
	})
	waitFor(t, h, "approval", func() bool { return approval != nil })
	before, _ := os.ReadFile(path)
	if string(before) != "pre-existing edit\n" {
		t.Fatal("write ran before approval")
	}
	if err := p.Reply(t.Context(), agent.Reply{Session: "wrong", Approval: approval.ID, Decision: agent.Once}); err == nil {
		t.Fatal("accepted mismatched session")
	}
	if err := p.Reply(t.Context(), agent.Reply{Session: s.ID, Approval: approval.ID, Decision: agent.Always}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, h, "tool turn", func() bool { return settled })
	changes := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Changes(ctx, s.ID) })
	if len(changes) != 1 || !strings.Contains(changes[0].Patch, "-pre-existing edit") || !strings.Contains(changes[0].Patch, "+updated") {
		t.Fatalf("changes %+v", changes)
	}
}
func TestShellTracksNewFilesInEmptyWorkspace(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	s := createChat(t, h, p, dir)
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("bash", map[string]any{"command": "printf hello > created.txt"}, nil)}, StopReason: "toolUse"}, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done")}}})
	settled := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.TurnSucceeded {
				settled = true
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Create a file", Mode: modeFull})
	})
	waitFor(t, h, "shell turn", func() bool { return settled })
	changes := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Changes(ctx, s.ID) })
	if len(changes) != 1 || changes[0].Status != agent.FileAdded || changes[0].Path != "created.txt" {
		t.Fatalf("changes %+v", changes)
	}
}

func TestBackendContract(t *testing.T) {
	h, p, _ := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	e, _ := p.current()
	c, _ := e.get(s.ID)
	var count atomic.Int32
	c.session.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"input": {func(...any) (any, error) { count.Add(1); c.session.Agent().SetModel(nil); return nil, nil }}}}}, c.rec.Session.Directory))
	plugintest.CheckBackend(t, plugintest.BackendCase{Harness: h, PluginID: "pig", Session: s.ID, RejectSend: func() (string, func() int) { return "refuse", func() int { return int(count.Load()) } }})
}
func TestProviderFailureIsNotSuccessOrRetried(t *testing.T) {
	h, p, faux := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{StopReason: "error", ErrorMessage: "scripted provider failure"}})
	failed := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.TurnSucceeded {
				t.Error("provider failure reported success")
			}
			if ev.Kind == agent.TurnFailed {
				failed = true
				if ev.Error != "scripted provider failure" {
					t.Errorf("error %q", ev.Error)
				}
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "fail"})
	})
	waitFor(t, h, "provider failure", func() bool { return failed })
	if got := faux.State().CallCount(); got != 1 {
		t.Fatalf("provider retried %d times", got)
	}
}
func TestStopReleasesApprovalAndDoesNotWrite(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	s := createChat(t, h, p, dir)
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "blocked.txt", "content": "bad"}, nil)}, StopReason: "toolUse"}})
	var approval *agent.Approval
	stopped := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Approval != nil {
				approval = ev.Approval
			}
			if ev.Kind == agent.TurnInterrupted {
				stopped = true
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "write"})
	})
	waitFor(t, h, "approval", func() bool { return approval != nil })
	if err := p.Abort(t.Context(), s.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, h, "stop", func() bool { return stopped })
	if _, err := os.Stat(filepath.Join(dir, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("write executed: %v", err)
	}
	if err := p.Reply(t.Context(), agent.Reply{Session: s.ID, Approval: approval.ID, Decision: agent.Once}); err == nil {
		t.Fatal("stale approval accepted")
	}
}
func TestRewindPreservesOriginalAndAttachmentMetadata(t *testing.T) {
	h, p, faux := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("First")}}, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Second")}}})
	e, _ := p.current()
	c, _ := e.get(s.ID)
	for _, text := range []string{"one", "two"} {
		call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
			return p.Send(ctx, agent.Send{Session: s.ID, Text: text})
		})
		waitFor(t, h, "idle", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.busy })
	}
	original := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	if len(original) != 4 {
		t.Fatalf("original %+v", original)
	}
	fork := call(t, h, func(ctx context.Context) (agent.SessionID, error) { return p.Rewind(ctx, s.ID, original[2].ID) })
	if fork == s.ID {
		t.Fatal("rewind reused original")
	}
	kept := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, fork) })
	if len(kept) != 2 || kept[0].Text != "one" {
		t.Fatalf("fork %+v", kept)
	}
	still := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	if len(still) != 4 {
		t.Fatal("original was modified")
	}
}
func TestPendingInteractionPreservesEmptyAndCancellation(t *testing.T) {
	h, p, _ := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	e, _ := p.current()
	c, _ := e.get(s.ID)
	for _, cancelled := range []bool{false, true} {
		result := make(chan error, 1)
		go func() {
			answer, err := e.question(t.Context(), c, agent.Interaction{Kind: agent.Input, Title: "Question"})
			if !cancelled && answer.Value != "" {
				err = errors.New("empty answer changed")
			}
			result <- err
		}()
		var pending []agent.Interaction
		waitFor(t, h, "pending question", func() bool { pending, _ = p.Pending(t.Context(), s.ID); return len(pending) == 1 })
		if err := p.Answer(t.Context(), agent.InteractionAnswer{Session: s.ID, ID: pending[0].ID, Cancelled: cancelled, Value: ""}); err != nil {
			t.Fatal(err)
		}
		var err error
		waitFor(t, h, "question answer", func() bool {
			select {
			case err = <-result:
				return true
			default:
				return false
			}
		})
		if cancelled != errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled=%v err=%v", cancelled, err)
		}
	}
}
func TestProviderFormRendersAndMasksSecrets(t *testing.T) {
	h, p, _ := startPig(t)
	waitFor(t, h, "provider list", func() bool { return len(p.providers.list) > 0 })
	p.providers.selected = "fixture"
	p.providers.reply = make(chan string, 1)
	p.providers.prompt = "API key"
	p.providers.secret = true
	p.providers.answer = "secret-fixture-value"
	tester := ui.NewTester(p.providersSection, 700, 600)
	tester.Frame()
	if strings.Contains(strings.Join(tester.Texts(), "\n"), "secret-fixture-value") {
		t.Fatal("secret displayed as plain text")
	}
	if !strings.Contains(strings.Join(tester.Texts(), "\n"), "Continue") {
		t.Fatalf("form missing: %s", tester.Texts())
	}
	h.Flush()
}

func TestProjectInstructionsAndSkills(t *testing.T) {
	h, p, _ := startPig(t)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "fixture")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: fixture\ndescription: A test skill\n---\nUse this fixture skill.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Repository-specific fixture instructions."), 0600); err != nil {
		t.Fatal(err)
	}
	skills := call(t, h, func(ctx context.Context) ([]agent.Skill, error) { return p.Skills(ctx, dir) })
	if !slices.ContainsFunc(skills, func(skill agent.Skill) bool { return skill.Name == "fixture" }) {
		t.Fatal("project skill missing")
	}
	s := createChat(t, h, p, dir)
	e, _ := p.current()
	c, _ := e.get(s.ID)
	if !strings.Contains(c.session.SystemPrompt(), "Repository-specific fixture instructions.") {
		t.Fatal("repository instructions missing")
	}
}

func TestAPIKeyLoginUsesWadaStorageAndUpdatesModels(t *testing.T) {
	h, p, faux := startPig(t)
	e, _ := p.current()
	provider := faux.Provider()
	provider.Auth.APIKey = &ai.APIKeyAuth{Name: "Fixture key", Login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
		key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Fixture API key"})
		return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, err
	}, Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		if input.Credential == nil || input.Credential.Key == "" {
			return nil, nil
		}
		return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Source: "stored"}, nil
	}}
	if err := e.models.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	p.login("wada-test", ai.CredentialAPIKey)
	waitFor(t, h, "login prompt", func() bool { return p.providers.reply != nil })
	if !p.providers.secret {
		t.Fatal("credential prompt is not masked")
	}
	p.answerLogin("fixture-api-key")
	waitFor(t, h, "login finished", func() bool { return p.providers.selected == "" })
	cred, err := e.services.Credentials().Read(t.Context(), "wada-test")
	if err != nil || cred == nil || cred.Key != "fixture-api-key" {
		t.Fatalf("credential not saved: %v", err)
	}
	if !strings.HasPrefix(e.services.Auth().Path(), filepath.Join(p.host.DataDir(), "pig")) {
		t.Fatal("credentials escaped Wada storage")
	}
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, e.models.Logout(ctx, "wada-test") })
	cred, err = e.services.Credentials().Read(t.Context(), "wada-test")
	if err != nil || cred != nil {
		t.Fatal("disconnect retained stored credential")
	}
}
func TestEmptySessionFirstSendAfterRestartKeepsHistory(t *testing.T) {
	h, p, _ := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	if err := h.Disable("pig"); err != nil {
		t.Fatal(err)
	}
	if err := h.Enable("pig"); err != nil {
		t.Fatal(err)
	}
	service, _ := kernel.Use(h, agent.ServiceKey)
	p = service.(*Plugin)
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	e, _ := p.current()
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "wada-test", Model: "test-model"})
	if err := e.models.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Persisted")}}})
	done := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.TurnSucceeded {
				done = true
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Retain me"})
	})
	waitFor(t, h, "turn", func() bool { return done })
	if err := h.Disable("pig"); err != nil {
		t.Fatal(err)
	}
	if err := h.Enable("pig"); err != nil {
		t.Fatal(err)
	}
	service, _ = kernel.Use(h, agent.ServiceKey)
	p = service.(*Plugin)
	waitFor(t, h, "ready again", func() bool { return p.Status().State == agent.Ready })
	messages := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	if len(messages) != 2 || messages[0].Text != "Retain me" {
		t.Fatalf("history lost: %+v", messages)
	}
}
func TestMultipleWritesAndSeparateSessionChanges(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	s := createChat(t, h, p, dir)
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "nested/a.txt", "content": "A"}, nil), ai.FauxToolCall("write", map[string]any{"path": "nested/b.txt", "content": "B"}, nil)}, StopReason: "toolUse"}, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done")}}})
	done := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.TurnSucceeded {
				done = true
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Write two", Mode: modeFull})
	})
	waitFor(t, h, "two writes", func() bool { return done })
	changes := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Changes(ctx, s.ID) })
	if len(changes) != 2 {
		t.Fatalf("changes %+v", changes)
	}
	other := createChat(t, h, p, dir)
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "separate.txt", "content": "Other"}, nil)}, StopReason: "toolUse"}, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done")}}})
	done = false
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: other.ID, Text: "Other file", Mode: modeFull})
	})
	waitFor(t, h, "other write", func() bool { return done })
	changes = call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Changes(ctx, s.ID) })
	if len(changes) != 2 {
		t.Fatal("other conversation's file attributed to first")
	}
}

func TestConcurrentApprovalsAndFixedChildPolicy(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	a := createChat(t, h, p, dir)
	b := createChat(t, h, p, dir)
	write := func(path string) ai.FauxResponse {
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": path, "content": "written"}, nil)}, StopReason: "toolUse"}
	}
	done := ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done")}}
	faux.SetResponses([]ai.FauxResponseStep{write("a.txt"), write("b.txt"), done, done})
	approvals := map[agent.SessionID]*agent.Approval{}
	finished := map[agent.SessionID]bool{}
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Approval != nil {
				approvals[ev.Session] = ev.Approval
			}
			if ev.Kind == agent.TurnSucceeded {
				finished[ev.Session] = true
			}
		}
	})
	for _, id := range []agent.SessionID{a.ID, b.ID} {
		call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
			return p.Send(ctx, agent.Send{Session: id, Text: "Write"})
		})
		waitFor(t, h, "independent approval", func() bool { return approvals[id] != nil })
	}
	for _, id := range []agent.SessionID{b.ID, a.ID} {
		if err := p.Reply(t.Context(), agent.Reply{Session: id, Approval: approvals[id].ID, Decision: agent.Once}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, h, "independent completion", func() bool { return finished[id] })
	}
	for id, path := range map[agent.SessionID]string{a.ID: "a.txt", b.ID: "b.txt"} {
		changes := call(t, h, func(ctx context.Context) ([]agent.FileChange, error) { return p.Changes(ctx, id) })
		if len(changes) != 1 || changes[0].Path != path {
			t.Fatalf("attribution for %s: %+v", id, changes)
		}
	}
	child := call(t, h, func(ctx context.Context) (agent.Session, error) {
		return p.Spawn(ctx, agent.Spawn{Parent: a.ID, Name: "Read only", Directory: dir, Policy: agent.ToolPolicy{Deny: []agent.ToolKind{agent.ToolEdit}}})
	})
	faux.SetResponses([]ai.FauxResponseStep{write("denied.txt"), done})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: child.ID, Mode: modeFull, Text: "Write"})
	})
	waitFor(t, h, "denied child completion", func() bool { return finished[child.ID] })
	if approvals[child.ID] != nil {
		t.Fatal("denied tool requested an override")
	}
	if _, err := os.Stat(filepath.Join(dir, "denied.txt")); !os.IsNotExist(err) {
		t.Fatal("full access bypassed fixed child policy")
	}
}
func TestAlwaysApprovalIsScopedToExactConversationAction(t *testing.T) {
	h, p, faux := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	response := ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "same.txt", "content": "same"}, nil)}, StopReason: "toolUse"}
	done := ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done")}}
	faux.SetResponses([]ai.FauxResponseStep{response, done, response, done})
	requests, finished := 0, 0
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.ApprovalRequested {
				requests++
				if err := p.Reply(t.Context(), agent.Reply{Session: s.ID, Approval: ev.Approval.ID, Decision: agent.Always}); err != nil {
					t.Error(err)
				}
			}
			if ev.Kind == agent.TurnSucceeded {
				finished++
			}
		}
	})
	for i := 1; i <= 2; i++ {
		call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
			return p.Send(ctx, agent.Send{Session: s.ID, Text: "Same action"})
		})
		waitFor(t, h, "remembered action", func() bool { return finished == i })
	}
	if requests != 1 {
		t.Fatalf("asked %d times", requests)
	}
}

func TestHandledReceiptDoesNotSettleIndependentRun(t *testing.T) {
	h, p, _ := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	e, _ := p.current()
	c, _ := e.get(s.ID)
	var events []agent.Event
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.TurnStarted || ev.Kind == agent.TurnSucceeded || ev.Kind == agent.TurnFailed || ev.Kind == agent.TurnInterrupted {
				events = append(events, ev)
			}
		}
	})
	e.onEvent(c, pa.AgentStartEvent{})
	e.finishTurn(c, false, false, nil, nil)
	e.onEvent(c, pa.AgentSettledEvent{})
	waitFor(t, h, "independent settlement", func() bool {
		for _, ev := range events {
			if ev.Kind == agent.TurnSucceeded {
				return true
			}
		}
		return false
	})
	if len(events) != 2 || events[0].Kind != agent.TurnStarted || events[1].Kind != agent.TurnSucceeded {
		t.Fatalf("events %+v", events)
	}
}
func TestUnavailableSnapshotDoesNotBlockPermittedShell(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(9 << 20); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	s := createChat(t, h, p, dir)
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("bash", map[string]any{"command": "printf executed > done.txt"}, nil)}, StopReason: "toolUse"}, ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done")}}})
	done := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Kind == agent.TurnSucceeded {
				done = true
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Mode: modeFull, Text: "Run"})
	})
	waitFor(t, h, "permitted action", func() bool { return done })
	content, err := os.ReadFile(filepath.Join(dir, "done.txt"))
	if err != nil || string(content) != "executed" {
		t.Fatalf("permitted shell did not execute: %v", err)
	}
	if _, err = p.Changes(t.Context(), s.ID); err == nil {
		t.Fatal("incomplete tracking reported as a complete diff")
	}
}

func TestDeletedSessionCannotBeReopenedByAnEarlierCaller(t *testing.T) {
	h, p, _ := startPig(t)
	s := createChat(t, h, p, t.TempDir())
	e, _ := p.current()
	earlier, _ := e.get(s.ID)
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Delete(ctx, s.ID) })
	earlier.mu.Lock()
	err := e.openLocked(earlier)
	earlier.mu.Unlock()
	if err == nil {
		t.Fatal("an earlier caller reopened the deleted session")
	}
}
