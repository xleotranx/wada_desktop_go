package pig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
)

// Open session owners, rather than GC timings, are the deterministic resource
// budget: every owner holds a runtime, services and a subscription.
func openSessionCount(p *Plugin) int {
	e, _ := p.current()
	e.mu.Lock()
	all := make([]*conversation, 0, len(e.sessions))
	for _, c := range e.sessions {
		all = append(all, c)
	}
	e.mu.Unlock()
	n := 0
	for _, c := range all {
		c.mu.Lock()
		if c.session != nil {
			n++
		}
		c.mu.Unlock()
	}
	return n
}

func sessionIsOpen(p *Plugin, id agent.SessionID) bool {
	e, _ := p.current()
	c, _ := e.get(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session != nil
}

func TestIdleSessionsAreBoundedAndRecentlyReadChatsStayOpen(t *testing.T) {
	h, p, _ := startPig(t)
	dir := t.TempDir()
	var sessions []agent.Session
	for range 8 {
		sessions = append(sessions, createChat(t, h, p, dir))
	}
	// Reading the oldest chat makes it more recent than the second chat.
	call(t, h, func(ctx context.Context) ([]agent.Message, error) {
		return p.Messages(ctx, sessions[0].ID)
	})
	sessions = append(sessions, createChat(t, h, p, dir))
	if !sessionIsOpen(p, sessions[0].ID) || sessionIsOpen(p, sessions[1].ID) {
		t.Fatal("retention did not keep the recently read chat and evict the least recently used one")
	}
	for range 3 {
		for _, s := range sessions {
			call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
			if n := openSessionCount(p); n > 8 {
				t.Fatalf("reading chats retained %d idle SDK sessions; want at most 8", n)
			}
		}
	}
}

func TestEvictedSessionRestoresMessagesAttachmentsAndModel(t *testing.T) {
	h, p, _ := startPig(t)
	e, _ := p.current()
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "retention-test", Models: []ai.FauxModelDefinition{{ID: "reasoning-model", Reasoning: true}}})
	if err := e.models.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	ref := agent.ModelRef{Provider: "retention-test", ID: "reasoning-model", Variant: "high"}
	dir := t.TempDir()
	s := call(t, h, func(ctx context.Context) (agent.Session, error) {
		return p.Create(ctx, agent.CreateSession{Directory: dir, Title: "Keep this title", Model: ref, Mode: modeFull})
	})
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Saved answer")}}})
	finished := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Session == s.ID && ev.Kind == agent.TurnSucceeded {
				finished = true
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Remember this", Files: []agent.File{{Name: "note.txt", MIME: "text/plain", Data: []byte("attachment contents")}}})
	})
	waitFor(t, h, "saved turn", func() bool { return finished })
	before := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	for range 9 {
		createChat(t, h, p, dir)
	}
	if sessionIsOpen(p, s.ID) {
		t.Fatal("old idle session was not evicted")
	}
	after := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("reopening changed stored messages or attachments:\nbefore: %+v\nafter: %+v", before, after)
	}
	c, _ := e.get(s.ID)
	c.mu.Lock()
	actualID, actualModel, actualMode := c.session.ID(), sessionModel(c.session), c.rec.Mode
	c.mu.Unlock()
	if actualID != string(s.ID) || actualModel != ref || actualMode != modeFull {
		t.Fatalf("reopened session: id=%s model=%+v mode=%s", actualID, actualModel, actualMode)
	}
	page := call(t, h, func(ctx context.Context) (agent.SessionPage, error) {
		return p.Sessions(ctx, agent.SessionQuery{Search: "Keep this title"})
	})
	if len(page.Sessions) != 1 || page.Sessions[0].ID != s.ID || page.Sessions[0].Model != ref {
		t.Fatalf("reopening lost saved metadata: %+v", page)
	}
}

func TestRetentionPressurePreservesTurnWaitingForApproval(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	s := createChat(t, h, p, dir)
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "approved.txt", "content": "written after approval"}, nil)}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Done")}},
	})
	var approval *agent.Approval
	finished := false
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Session != s.ID {
				continue
			}
			if ev.Kind == agent.ApprovalRequested {
				approval = ev.Approval
			}
			if ev.Kind == agent.TurnSucceeded {
				finished = true
			}
			if ev.Kind == agent.TurnFailed {
				t.Errorf("protected turn failed: %s", ev.Error)
			}
		}
	})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Write after permission"})
	})
	waitFor(t, h, "tool approval", func() bool { return approval != nil })
	for range 12 {
		createChat(t, h, p, dir)
	}
	if !sessionIsOpen(p, s.ID) {
		t.Fatal("retention pressure closed the turn waiting for approval")
	}
	if n := openSessionCount(p); n > 9 {
		t.Fatalf("retained %d sessions; want at most 8 idle plus the protected turn", n)
	}
	if err := p.Reply(t.Context(), agent.Reply{Session: s.ID, Approval: approval.ID, Decision: agent.Once}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, h, "approved turn completion and idle retention", func() bool { return finished && openSessionCount(p) <= 8 })
	data, err := os.ReadFile(filepath.Join(dir, "approved.txt"))
	if err != nil || string(data) != "written after approval" {
		t.Fatalf("approved tool did not finish: data=%q err=%v", data, err)
	}
}

func TestRetentionPressurePreservesModelChangeDialog(t *testing.T) {
	h, p, faux := startPig(t)
	dir := t.TempDir()
	s := createChat(t, h, p, dir)
	e, _ := p.current()
	c, _ := e.get(s.ID)
	next := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "next-retention-test", Model: "other-model"})
	if err := e.models.RegisterNativeProvider(next.Provider()); err != nil {
		t.Fatal(err)
	}
	var runner *inproc.Runner
	runner = inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"model_select": {func(args ...any) (any, error) {
		return runner.GetUIContext().Confirm(args[1].(context.Context), "Keep this dialog", "Change the model?", extension.ExtensionUIDialogOptions{})
	}}}}}, dir)
	c.session.ReplaceRunner(runner)
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Ready")}}})
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: s.ID, Text: "Initialize extensions"})
	})
	waitFor(t, h, "initial turn", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.busy })
	var question *agent.Interaction
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Session == s.ID && ev.Kind == agent.InteractionRequested && !ev.Interaction.Settled {
				question = ev.Interaction
			}
		}
	})
	done := make(chan error, 1)
	ref := agent.ModelRef{Provider: "next-retention-test", ID: "other-model"}
	go func() { done <- p.SetModel(t.Context(), s.ID, ref) }()
	waitFor(t, h, "model change question", func() bool { return question != nil })
	for range 12 {
		createChat(t, h, p, dir)
	}
	if !sessionIsOpen(p, s.ID) {
		t.Fatal("retention pressure closed the session with an open model change dialog")
	}
	if n := openSessionCount(p); n > 9 {
		t.Fatalf("retained %d sessions; want at most 8 idle plus the protected model change", n)
	}
	if err := p.Answer(t.Context(), agent.InteractionAnswer{Session: s.ID, ID: question.ID, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	var result error
	waitFor(t, h, "model change completion", func() bool {
		select {
		case result = <-done:
			return true
		default:
			return false
		}
	})
	if result != nil {
		t.Fatal(result)
	}
	if n := openSessionCount(p); n > 8 {
		t.Fatalf("completed model change retained %d idle sessions; want at most 8", n)
	}
}

func TestCancelledBackgroundDialogReleasesIdleRetention(t *testing.T) {
	h, p, _ := startPig(t)
	dir := t.TempDir()
	s := createChat(t, h, p, dir)
	e, _ := p.current()
	c, _ := e.get(s.ID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var question *agent.Interaction
	kernel.On(h, agent.Events, func(batch []agent.Event) {
		for _, ev := range batch {
			if ev.Session == s.ID && ev.Kind == agent.InteractionRequested && !ev.Interaction.Settled {
				question = ev.Interaction
			}
		}
	})
	// The SDK permits session-info notifications to outlive Rename. Model such
	// a background dialog without an initiating service call or running turn.
	done := make(chan error, 1)
	go func() {
		_, err := e.question(ctx, c, agent.Interaction{Kind: agent.Confirm, Title: "Background confirmation"})
		done <- err
	}()
	waitFor(t, h, "background dialog", func() bool { return question != nil })
	for range 12 {
		createChat(t, h, p, dir)
	}
	if !sessionIsOpen(p, s.ID) {
		t.Fatal("retention pressure closed the session with a pending background dialog")
	}
	cancel()
	var result error
	waitFor(t, h, "dialog cancellation", func() bool {
		select {
		case result = <-done:
			return true
		default:
			return false
		}
	})
	if !errors.Is(result, context.Canceled) {
		t.Fatalf("background dialog returned %v, want cancellation", result)
	}
	// No further service call is needed to reclaim the now-idle owner.
	waitFor(t, h, "idle retention after dialog cancellation", func() bool { return openSessionCount(p) <= 8 })
}
