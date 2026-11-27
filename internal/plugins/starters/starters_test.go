package starters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

func start(t *testing.T, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *plugintest.Chat) {
	t.Helper()
	ctl := &plugintest.Chat{}
	var p *Plugin
	h := kerneltest.New(t, append([]kernel.Factory{
		ctl.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }),
	}, extra...)...)
	h.Flush()
	return h, p, ctl
}

func write(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAProjectOffersCodeStarters(t *testing.T) {
	h, p, ctl := start(t)
	dir := t.TempDir()
	write(t, dir, "main.go", "go.mod", "notes.md")
	ctl.NewChat(dir)
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	for _, want := range []string{"Explain this project", "Review the changes", "Find a bug", "main.go", "go.mod"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	selected := "unset"
	kernel.On(h, chat.WorkflowSelected, func(id string) { selected = id })
	if err := tt.Click("Explain this project"); err != nil {
		t.Fatal(err)
	}
	if selected != "" {
		t.Fatalf("a code starter kept a workflow: %q", selected)
	}
	if !strings.HasPrefix(ctl.Draft, "Give me a tour") {
		t.Fatalf("draft %q", ctl.Draft)
	}
}

func TestAnEmptyFolderOffersScaffoldAndExplore(t *testing.T) {
	h, p, ctl := start(t)
	ctl.NewChat(t.TempDir())
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	if !tt.HasText("Scaffold a new project") || !tt.HasText("Explore this folder") {
		t.Fatalf("starters %q", tt.Texts())
	}
}

func TestNoFolderOffersNothing(t *testing.T) {
	_, p, ctl := start(t)
	ctl.NewChat("")
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	if len(tt.Texts()) != 0 {
		t.Fatalf("starters without a folder: %q", tt.Texts())
	}
}

func TestNonCodeFilesOfferExploreStarters(t *testing.T) {
	h, p, ctl := start(t)
	dir := t.TempDir()
	write(t, dir, "notes.txt")
	ctl.NewChat(dir)
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	if !tt.HasText("Explore this folder") || !tt.HasText("Review the changes") {
		t.Fatalf("starters %q", tt.Texts())
	}
}

func TestChangingTheFolderChangesTheStarters(t *testing.T) {
	h, p, ctl := start(t)
	empty := t.TempDir()
	code := t.TempDir()
	write(t, code, "main.go")
	ctl.NewChat(empty)
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	if !tt.HasText("Scaffold a new project") {
		t.Fatalf("empty: %q", tt.Texts())
	}
	ctl.SetDirectory(code)
	h.Flush()
	tt.Frame()
	if !tt.HasText("Explain this project") || tt.HasText("Scaffold a new project") {
		t.Fatalf("code: %q", tt.Texts())
	}
}
