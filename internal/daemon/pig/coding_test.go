package pig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

	if e.mutationLock(t.Context(), dirA) != e.mutationLock(t.Context(), filepath.Join(dirA, ".")) {
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
	if _, err := os.Stat(e.baselinePath(s.ID)); err != nil {
		release()
		t.Fatalf("baseline file missing: %v", err)
	}
	release()
	_ = rt.Close()

	rt, err = New(t.Context(), Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	changes, err := rt.Changes(t.Context(), s.ID)
	if err != nil || len(changes) != 1 || changes[0].Path != "main.go" {
		t.Fatalf("expected 1 change after restart, got %+v, err=%v", changes, err)
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
	if err != nil || len(forkChanges) != 1 || forkChanges[0].Path != "main.go" {
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

func TestShellCommandsDoNotHoldTheFolderAndUntrackableFilesAreSkipped(t *testing.T) {
	rt, _ := newTestRuntime(t)
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, 9<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	model := protocol.ModelRef{Provider: "wada-test", ID: "test-model"}
	sA, err := rt.Create(t.Context(), protocol.CreateSessionRequest{Directory: dir, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	sB, err := rt.Create(t.Context(), protocol.CreateSessionRequest{Directory: dir, Model: model})
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

	if e.mutationLock(t.Context(), dir) != e.mutationLock(t.Context(), filepath.Join(dir, "sub")) {
		t.Fatal("a repository and its subfolder took different locks")
	}
	// A running shell command in A neither fails on the large file nor
	// keeps B from editing.
	if err := e.beginMutation(t.Context(), cA, "shA", "bash", json.RawMessage(`{"command":"make"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := e.beginMutation(ctx, cB, "edB", "write", json.RawMessage(`{"path":"small.go"}`)); err != nil {
		t.Fatalf("an edit waited for another conversation's running command: %v", err)
	}
	e.finishMutation(cB, "edB")
	if err := os.WriteFile(filepath.Join(dir, "small.go"), []byte("package a\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.finishMutation(cA, "shA")
	changes, err := rt.Changes(t.Context(), sA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "small.go" {
		t.Fatalf("changes = %+v, want small.go only", changes)
	}
}

func TestProjectFilesStopsAWalkAtTheCap(t *testing.T) {
	dir := t.TempDir()
	for i := range maxWalkedFiles + 5 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, truncated, err := projectFiles(t.Context(), dir)
	if err != nil || !truncated || len(files) != maxWalkedFiles {
		t.Fatalf("projectFiles = %d files, truncated %v, err %v", len(files), truncated, err)
	}
	if _, err := workspaceSnapshot(t.Context(), dir); !errors.Is(err, errTooManyFiles) {
		t.Fatalf("workspaceSnapshot of a truncated walk = %v", err)
	}
}
