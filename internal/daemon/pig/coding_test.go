package pig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"wada-desktop-go/internal/protocol"
)

func TestCodingIdentityAndBuiltinToolsEnabled(t *testing.T) {
	rt, _ := newTestRuntime(t)
	s, err := rt.Create(t.Context(), protocol.CreateSessionRequest{
		Directory: t.TempDir(),
		Title:     "Coding tools",
		Model:     protocol.ModelRef{Provider: "wada-test", ID: "test-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	e, release, err := rt.acquire()
	defer release()
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.get(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	prompt := c.session.SystemPrompt()
	if !strings.Contains(prompt, "Wada") || !strings.Contains(prompt, "software engineering") {
		t.Fatalf("unexpected coding system prompt: %q", prompt)
	}
	names := c.session.ActiveToolNames()
	for _, name := range []string{"read", "bash", "edit", "write"} {
		if !slices.Contains(names, name) {
			t.Fatalf("missing built-in coding tool %s: %v", name, names)
		}
	}
	c.turn = &turnState{}
	if result := e.toolHook(c)(t.Context(), "read", "read", json.RawMessage(`{"path":"main.go"}`)); result.Block {
		t.Fatalf("read was blocked in ask mode: %s", result.Reason)
	}
	if got := permissionFor(modeAsk, nil, toolKind("bash")); got != ask {
		t.Fatalf("bash permission in ask mode = %v, want ask", got)
	}
	if got := permissionFor(modeAsk, nil, toolKind("write")); got != ask {
		t.Fatalf("write permission in ask mode = %v, want ask", got)
	}
	c.turn = nil
}

func TestPerDirectoryMutationLockAllowsConcurrentWorkspaces(t *testing.T) {
	rt, _ := newTestRuntime(t)
	dirA := t.TempDir()
	dirB := t.TempDir()
	sA, err := rt.Create(t.Context(), protocol.CreateSessionRequest{Directory: dirA, Model: protocol.ModelRef{Provider: "wada-test", ID: "test-model"}})
	if err != nil {
		t.Fatal(err)
	}
	sB, err := rt.Create(t.Context(), protocol.CreateSessionRequest{Directory: dirB, Model: protocol.ModelRef{Provider: "wada-test", ID: "test-model"}})
	if err != nil {
		t.Fatal(err)
	}
	e, release, err := rt.acquire()
	defer release()
	if err != nil {
		t.Fatal(err)
	}
	cA, _ := e.get(sA.ID)
	cB, _ := e.get(sB.ID)

	if e.mutationLock(dirA) != e.mutationLock(filepath.Join(dirA, ".")) {
		t.Fatal("normalized paths did not share a mutation lock")
	}
	if err := e.beginMutation(t.Context(), cA, "mA", "write", json.RawMessage(`{"path":"a.go"}`)); err != nil {
		t.Fatal(err)
	}
	if err := e.beginMutation(t.Context(), cB, "mB", "write", json.RawMessage(`{"path":"b.go"}`)); err != nil {
		e.finishMutation(cA, "mA")
		t.Fatalf("distinct workspace blocked on unrelated mutation lock: %v", err)
	}
	e.finishMutation(cB, "mB")
	e.finishMutation(cA, "mA")
}

func TestBaselinesAreLazyOnDiskAndSurviveRestartDeleteRewind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "pig")
	rt, err := New(t.Context(), Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "wada-test", Model: "test-model"})
	if err := rt.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := rt.Create(t.Context(), protocol.CreateSessionRequest{
		Directory: dir,
		Model:     protocol.ModelRef{Provider: "wada-test", ID: "test-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("write", map[string]any{"path": "main.go", "content": "package main\n// edited\n"}, nil)}, StopReason: "toolUse"},
		ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Updated")}},
	})
	if _, err := rt.Send(t.Context(), protocol.SendRequest{Session: s.ID, Mode: modeFull, Text: "Edit main.go"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Session == s.ID && ev.Kind == protocol.EventTurnSucceeded {
				goto doneTurn
			}
			if ev.Session == s.ID && ev.Kind == protocol.EventTurnFailed {
				t.Fatalf("turn failed: %s", ev.Error)
			}
		case <-deadline:
			t.Fatal("timed out waiting for edit turn")
		}
	}
doneTurn:
	e, release, _ := rt.acquire()
	c, _ := e.get(s.ID)
	if len(c.rec.Baselines) != 0 {
		release()
		t.Fatalf("baselines retained in RAM: %d", len(c.rec.Baselines))
	}
	if _, err := os.Stat(e.baselinePath(s.ID)); err != nil {
		release()
		t.Fatalf("baseline file missing: %v", err)
	}

	// Simulate legacy inline Baselines in metadata/<id>.json to verify migration on restart.
	c.mu.Lock()
	c.rec.Baselines = map[string]fileSnapshot{"legacy.txt": {Exists: true, Data: []byte("old\n")}}
	if err := e.saveLocked(c); err != nil {
		c.mu.Unlock()
		release()
		t.Fatal(err)
	}
	c.mu.Unlock()
	release()
	if err := os.WriteFile(filepath.Join(dir, "legacy.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = rt.Close()

	rt, err = New(t.Context(), Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	e, release, _ = rt.acquire()
	c, _ = e.get(s.ID)
	if len(c.rec.Baselines) != 0 {
		release()
		t.Fatalf("migrated baselines still kept in RAM: %d", len(c.rec.Baselines))
	}
	release()

	changes, err := rt.Changes(t.Context(), s.ID)
	if err != nil || len(changes) != 2 {
		t.Fatalf("expected 2 changes after restart & legacy migration, got %+v, err=%v", changes, err)
	}

	msgs, err := rt.Messages(t.Context(), s.ID)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("messages = %+v, err=%v", msgs, err)
	}
	forkID, err := rt.Rewind(t.Context(), s.ID, msgs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Delete(t.Context(), s.ID); err != nil {
		t.Fatal(err)
	}
	e, release, _ = rt.acquire()
	if _, err := os.Stat(e.baselinePath(s.ID)); !os.IsNotExist(err) {
		release()
		t.Fatalf("deleted session baseline still exists: %v", err)
	}
	release()
	forkChanges, err := rt.Changes(t.Context(), forkID)
	if err != nil || len(forkChanges) != 2 {
		t.Fatalf("fork lost baselines after original delete: %+v, err=%v", forkChanges, err)
	}
}

func TestProvidersListAndLoginCancel(t *testing.T) {
	rt, _ := newTestRuntime(t)
	list, err := rt.Providers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var targetID string
	for _, pr := range list {
		if pr.SupportsAPIKey && !pr.Connected {
			targetID = pr.ID
			break
		}
	}
	if targetID == "" {
		t.Fatalf("expected at least one API-key capable provider, got %+v", list)
	}

	loginID, err := rt.StartProviderLogin(t.Context(), protocol.ProviderLoginStartRequest{
		LoginID:    "login-test-1",
		ProviderID: targetID,
		AuthType:   protocol.ProviderAuthAPIKey,
	})
	if err != nil || loginID != "login-test-1" {
		t.Fatalf("StartProviderLogin = %q, err=%v", loginID, err)
	}

	deadline := time.After(5 * time.Second)
	gotPrompt := false
	for !gotPrompt {
		select {
		case ev := <-rt.Events():
			if ev.Kind == protocol.EventProviderAuth && ev.ProviderAuth != nil && ev.ProviderAuth.LoginID == loginID {
				switch ev.ProviderAuth.Kind {
				case protocol.ProviderAuthPromptText, protocol.ProviderAuthPromptSecret,
					protocol.ProviderAuthPromptCode, protocol.ProviderAuthPromptSelect:
					gotPrompt = true
				case protocol.ProviderAuthError:
					t.Fatalf("unexpected login error: %s", ev.ProviderAuth.Message)
				case protocol.ProviderAuthURL, protocol.ProviderAuthDeviceCode, protocol.ProviderAuthInfo,
					protocol.ProviderAuthProgress, protocol.ProviderAuthDone:
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for provider auth prompt")
		}
	}

	if err := rt.CancelProviderLogin(t.Context(), loginID); err != nil {
		t.Fatal(err)
	}
}
