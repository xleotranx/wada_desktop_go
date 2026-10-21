package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"
)

const fixture = `{"type":"session","version":3,"id":"old-1","cwd":"/work/app"}
{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":[{"type":"text","text":"Fix the build\nplease"}]}}
{"type":"message","id":"a1","parentId":"u1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Looking"},{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"make"}}],"provider":"p","model":"m1","usage":{"input":100,"output":5,"reasoning":2,"cacheRead":40,"cacheWrite":3,"cost":{"total":0.25}}}}
{"type":"message","id":"r1","parentId":"a1","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false}}
{"type":"message","id":"x1","parentId":"u1","message":{"role":"assistant","content":[{"type":"text","text":"abandoned branch"}]}}
{"type":"model_change","id":"m1","parentId":"r1","provider":"p","modelId":"m2"}
{"type":"message","id":"a2","parentId":"m1","message":{"role":"assistant","content":[{"type":"text","text":"Fixed"}],"usage":{"cost":{"total":0.25}}}}
`

func startPi(t *testing.T) (*kerneltest.Harness, *Plugin, string) {
	t.Helper()
	t.Setenv("WADA_FAKE_PI", "1")
	dir := t.TempDir()
	old := filepath.Join(dir, "--work-app--", "2026_old-1.jsonl")
	os.MkdirAll(filepath.Dir(old), 0o700)
	os.WriteFile(old, []byte(fixture), 0o600)
	store := kerneltest.NewStore()
	raw, _ := json.Marshal(Settings{Executable: os.Args[0], SessionDir: dir})
	store.Save(context.Background(), []kernel.Value{{Namespace: "pi", Key: "settings", JSON: raw}})
	p := &Plugin{}
	h := kerneltest.Start(t, kerneltest.Config{Store: store}, p)
	if err := h.Enable("pi"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	return h, p, dir
}

func waitFor(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}

func call[T any](t *testing.T, h *kerneltest.Harness, fn func(context.Context) (T, error)) T {
	t.Helper()
	done := make(chan struct{})
	var v T
	var err error
	go func() { v, err = fn(context.Background()); close(done) }()
	for {
		select {
		case <-done:
			if err != nil {
				t.Fatal(err)
			}
			return v
		case <-time.After(time.Millisecond):
			h.Flush()
		}
	}
}

func TestBackendContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attempts")
	t.Setenv("WADA_FAKE_PI_ATTEMPTS", path)
	h, p, _ := startPi(t)
	plugintest.CheckBackend(t, plugintest.BackendCase{Harness: h, Service: p, PluginID: "pi", Session: "old-1",
		WaitReady: func() { waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready }) },
		RejectSend: func() (string, func() int) {
			return "reject-contract-send", func() int {
				raw, _ := os.ReadFile(path)
				return strings.Count(string(raw), "attempt\n")
			}
		},
	})
}

func TestReadsSessionFiles(t *testing.T) {
	h, p, _ := startPi(t)
	if st := p.Status(); st.Version != "9.9.9" {
		t.Fatalf("status %+v", st)
	}
	page := call(t, h, func(ctx context.Context) (agent.SessionPage, error) { return p.Sessions(ctx, agent.SessionQuery{}) })
	if len(page.Sessions) != 1 {
		t.Fatalf("page %+v", page)
	}
	s := page.Sessions[0]
	if s.ID != "old-1" || s.Title != "Fix the build" || s.Directory != "/work/app" || s.Cost != 0.5 || s.Model.ID != "m2" {
		t.Fatalf("session %+v", s)
	}
	if got := call(t, h, func(ctx context.Context) (agent.SessionPage, error) {
		return p.Sessions(ctx, agent.SessionQuery{Search: "nothing"})
	}); len(got.Sessions) != 0 {
		t.Fatalf("search %+v", got)
	}
	messages := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, "old-1") })
	var shape []string
	for _, m := range messages {
		for _, part := range m.Parts {
			switch part.Kind {
			case agent.PartText:
				shape = append(shape, "text:"+part.Text)
			case agent.PartTool:
				shape = append(shape, "tool:"+part.Tool.Command+"="+part.Tool.Output[0])
			case agent.PartReasoning:
				shape = append(shape, "thinking")
			case agent.PartOther:
			}
		}
		if m.Role == agent.RoleUser {
			shape = append(shape, "user:"+m.Text)
		}
	}
	want := []string{"user:Fix the build\nplease", "thinking", "text:Looking", "tool:make=ok", "text:Fixed"}
	if !slices.Equal(shape, want) {
		t.Fatalf("messages\n got %q\nwant %q", shape, want)
	}
	// Pi counts reasoning within output; the contract counts it apart.
	if got := messages[1].Tokens; got != (agent.Tokens{Input: 100, Output: 3, Reasoning: 2, CacheRead: 40, CacheWrite: 3}) {
		t.Fatalf("tokens %+v", got)
	}
}

func events(h *kerneltest.Harness) *[]agent.Event {
	var got []agent.Event
	kernel.On(h, agent.Events, func(batch []agent.Event) { got = append(got, batch...) })
	return &got
}

func kinds(evs []agent.Event) []agent.EventKind {
	var out []agent.EventKind
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func TestNewConversationStreamsAndIsWritten(t *testing.T) {
	h, p, _ := startPi(t)
	got := events(h)
	s := call(t, h, func(ctx context.Context) (agent.Session, error) {
		return p.Create(ctx, agent.CreateSession{Directory: t.TempDir(), Model: agent.ModelRef{Provider: "p", ID: "m1", Variant: "max"}})
	})
	if s.ID != "new-1" || s.Model.Variant != "max" {
		t.Fatalf("created %+v", s)
	}
	call(t, h, func(ctx context.Context) (struct{}, error) {
		_, err := p.Send(ctx, agent.Send{Session: s.ID, Text: "hi"})
		return struct{}{}, err
	})
	waitFor(t, h, "the turn", func() bool { return slices.Contains(kinds(*got), agent.TurnSucceeded) })
	want := []agent.EventKind{agent.TurnStarted, agent.TextStarted, agent.TextDelta, agent.TextDelta, agent.TextEnded,
		agent.ToolCalled, agent.ToolFinished, agent.TurnSucceeded}
	if !slices.Equal(kinds(*got), want) {
		t.Fatalf("events %v", kinds(*got))
	}
	for _, e := range *got {
		if e.Session != "new-1" {
			t.Fatalf("event for %q", e.Session)
		}
	}
	if called, done := (*got)[5].Tool, (*got)[6].Tool; called.Command != "ls" || done.Exit != 1 || done.Output[0] != "a\nb" {
		t.Fatalf("tool %+v then %+v", called, done)
	}
	messages := call(t, h, func(ctx context.Context) ([]agent.Message, error) { return p.Messages(ctx, s.ID) })
	if len(messages) != 2 || messages[1].Parts[0].Text != "Hello" {
		t.Fatalf("written messages %+v", messages)
	}
	page := call(t, h, func(ctx context.Context) (agent.SessionPage, error) { return p.Sessions(ctx, agent.SessionQuery{}) })
	if len(page.Sessions) != 2 {
		t.Fatalf("listed %+v", page.Sessions)
	}
}

func TestSendReportsPromptAdmission(t *testing.T) {
	h, p, _ := startPi(t)
	for text, want := range map[string]agent.SendDisposition{
		"hi": agent.SendAccepted, "/handled": agent.SendHandled, "/queued": agent.SendQueued,
	} {
		got := call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
			return p.Send(ctx, agent.Send{Session: "old-1", Text: text})
		})
		if got != want {
			t.Errorf("Send(%q) = %v; want %v", text, got, want)
		}
	}
}

func TestDialogsBecomeInteractions(t *testing.T) {
	h, p, _ := startPi(t)
	got := events(h)
	call(t, h, func(ctx context.Context) (struct{}, error) {
		_, err := p.Send(ctx, agent.Send{Session: "old-1", Text: "confirm please"})
		return struct{}{}, err
	})
	waitFor(t, h, "the question", func() bool { return slices.Contains(kinds(*got), agent.InteractionRequested) })
	var question *agent.Interaction
	for _, e := range *got {
		if e.Interaction != nil {
			question = e.Interaction
		}
	}
	if question.Kind != agent.Confirm || question.Title != "Run it?" || question.Message != "rm -rf build" {
		t.Fatalf("question %+v", question)
	}
	call(t, h, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.Answer(ctx, agent.InteractionAnswer{Session: "old-1", ID: question.ID, Confirmed: true})
	})
	waitFor(t, h, "the answer", func() bool { return slices.Contains(kinds(*got), agent.TurnSucceeded) })
	for _, e := range *got {
		if e.Kind == agent.TextEnded && e.Text.Text != "confirmed=true" {
			t.Fatalf("answer %q", e.Text.Text)
		}
	}
}

func TestRenameModelAndDelete(t *testing.T) {
	h, p, dir := startPi(t)
	got := events(h)
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Rename(ctx, "old-1", "Renamed") })
	call(t, h, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.SetModel(ctx, "old-1", agent.ModelRef{Provider: "p", ID: "m2"})
	})
	waitFor(t, h, "rename and model events", func() bool { return len(*got) >= 2 })
	if (*got)[0].Kind != agent.SessionRenamed || (*got)[0].Title != "Renamed" || (*got)[1].Kind != agent.ModelChanged {
		t.Fatalf("events %+v", *got)
	}
	models := call(t, h, func(ctx context.Context) ([]agent.Model, error) { return p.Models(ctx) })
	if len(models) != 2 || !slices.Equal(models[0].Variants, []string{"minimal", "low", "medium", "high", "max"}) ||
		models[1].Variants != nil || !models[0].Free || models[1].Free || models[0].Context != 200000 || models[1].Context != 0 {
		t.Fatalf("models %+v", models)
	}
	def := call(t, h, func(ctx context.Context) (agent.ModelRef, error) { return p.DefaultModel(ctx) })
	if def != (agent.ModelRef{Provider: "p", ID: "m1"}) {
		t.Fatalf("default %+v", def)
	}
	call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Delete(ctx, "old-1") })
	if _, err := os.Stat(filepath.Join(dir, "--work-app--", "2026_old-1.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
	if err := h.Disable("pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Sessions(context.Background(), agent.SessionQuery{}); err != agent.ErrNotConnected {
		t.Fatalf("disabled: %v", err)
	}
}

func TestRewindForksAndTheProcessFollows(t *testing.T) {
	h, p, _ := startPi(t)
	got := events(h)
	forked := call(t, h, func(ctx context.Context) (agent.SessionID, error) { return p.Rewind(ctx, "old-1", "u1") })
	if forked != "old-1-fork" {
		t.Fatalf("forked into %q", forked)
	}
	call(t, h, func(ctx context.Context) (agent.SendDisposition, error) {
		return p.Send(ctx, agent.Send{Session: forked, Text: "hi"})
	})
	waitFor(t, h, "the forked turn", func() bool { return slices.Contains(kinds(*got), agent.TurnSucceeded) })
	for _, e := range *got {
		if e.Session != forked {
			t.Fatalf("event of %q after the fork: %+v", e.Session, e)
		}
	}
	cancelled := call(t, h, func(ctx context.Context) (error, error) {
		_, err := p.Rewind(ctx, forked, "cancel-me")
		return err, nil
	})
	if cancelled == nil {
		t.Fatal("a cancelled fork succeeded")
	}
}

func TestIdleProcessesAreStopped(t *testing.T) {
	h, p, dir := startPi(t)
	p.mu.Lock()
	c := p.conn
	p.mu.Unlock()
	c.maxIdle = 1
	for _, id := range []string{"s2", "s3"} {
		f := filepath.Join(dir, "--x--", "2026_"+id+".jsonl")
		os.MkdirAll(filepath.Dir(f), 0o700)
		os.WriteFile(f, []byte(`{"type":"session","version":3,"id":"`+id+`","cwd":"/"}`+"\n"), 0o600)
	}
	for _, id := range []agent.SessionID{"old-1", "s2", "s3"} {
		call(t, h, func(ctx context.Context) (struct{}, error) { return struct{}{}, p.Rename(ctx, id, "x") })
	}
	c.mu.Lock()
	n := len(c.sessions)
	_, newest := c.sessions["s3"]
	c.mu.Unlock()
	if n != 1 || !newest {
		t.Fatalf("%d processes kept, newest kept %v", n, newest)
	}
}

func TestFailedTurnSaysWhy(t *testing.T) {
	h, p, _ := startPi(t)
	got := events(h)
	call(t, h, func(ctx context.Context) (struct{}, error) {
		_, err := p.Send(ctx, agent.Send{Session: "old-1", Text: "fail please"})
		return struct{}{}, err
	})
	waitFor(t, h, "the failure", func() bool { return slices.Contains(kinds(*got), agent.TurnFailed) })
	last := (*got)[len(*got)-1]
	if last.Kind != agent.TurnFailed || last.Error != "401: Invalid credential (after 3 tries)" {
		t.Fatalf("last event %+v", last)
	}
}

func TestPromptCarriesFiles(t *testing.T) {
	cmd, err := prompt(agent.Send{Text: "Look", Files: []agent.File{
		{Name: "shot.png", MIME: "image/png", Data: []byte{0x89, 'P'}},
		{Name: "notes.md", MIME: "text/markdown", Data: []byte("# Notes\n")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	images, _ := cmd["images"].([]map[string]string)
	if cmd["message"] != "Look\n\n(file: notes.md)\n```\n# Notes\n```" || len(images) != 1 ||
		images[0]["mimeType"] != "image/png" || images[0]["data"] != "iVA=" {
		t.Fatalf("prompt %+v", cmd)
	}
	_, err = prompt(agent.Send{Text: "x", Files: []agent.File{{Name: "a.zip", MIME: "application/zip", Data: []byte{0, 1}}}})
	if !errors.Is(err, agent.ErrNotSent) {
		t.Fatalf("binary file: %v", err)
	}
	stored := messagesOf([]entry{{Type: "message", ID: "u", Message: json.RawMessage(
		`{"role":"user","content":[{"type":"text","text":"Look"},{"type":"image","data":"iVA=","mimeType":"image/png"}]}`)}})
	if len(stored) != 1 || len(stored[0].Files) != 1 || stored[0].Files[0].MIME != "image/png" {
		t.Fatalf("stored %+v", stored)
	}
}

func TestImmediateReconnectAndDisable(t *testing.T) {
	h, p, _ := startPi(t)
	for range 20 {
		p.Reconnect()
		runtime.Gosched() // let supervision race with the next UI action
	}
	if err := h.Disable("pi"); err != nil {
		t.Fatal(err)
	}
	h.Close()
	if _, err := p.current(); !errors.Is(err, agent.ErrNotConnected) {
		t.Fatalf("disabled connection: %v", err)
	}
}

func TestToolKinds(t *testing.T) {
	for name, want := range map[string]agent.ToolKind{"bash": agent.ToolShell, "read": agent.ToolRead, "ls": agent.ToolSearch,
		"grep": agent.ToolSearch, "edit": agent.ToolEdit, "write": agent.ToolEdit, "my_extension_tool": agent.ToolOther} {
		if got := toolKind(name); got != want {
			t.Errorf("toolKind(%q) = %v, want %v", name, got, want)
		}
	}
}
