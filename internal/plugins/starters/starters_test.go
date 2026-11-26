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

type extension struct{ setup func(*kernel.Context) error }

func (f *extension) New() kernel.Plugin            { return f }
func (*extension) Manifest() kernel.Manifest       { return kernel.Manifest{ID: "extension"} }
func (e *extension) Setup(c *kernel.Context) error { return e.setup(c) }

func reviewedWork() *extension {
	return &extension{setup: func(c *kernel.Context) error {
		return kernel.Contribute(c, chat.ReviewedWork, "extension.review", 0, chat.Workflow{Title: "Plan, work, review"})
	}}
}

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

func TestDocumentsOfferReviewedWork(t *testing.T) {
	h, p, ctl := start(t, reviewedWork().New)
	if got := kernel.Items(h, chat.ReviewedWork); len(got) != 1 {
		t.Fatalf("reviewed work %+v", got)
	}
	dir := t.TempDir()
	write(t, dir, "q3-report.docx", "notes.pdf", "budget.xlsx")
	ctl.NewChat(dir)
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	for _, want := range []string{"Summarize these documents", "Draft a report from them", "Compare them", "Reviewed work", "q3-report.docx"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Explain this project") {
		t.Fatal("a documents folder shows code starters")
	}
	var selected string
	kernel.On(h, chat.WorkflowSelected, func(id string) { selected = id })
	if err := tt.Click("Summarize these documents"); err != nil {
		t.Fatal(err)
	}
	if selected != "extension.review" {
		t.Fatalf("workflow %q", selected)
	}
	if !strings.HasPrefix(ctl.Draft, "Read the documents in this folder") {
		t.Fatalf("draft %q", ctl.Draft)
	}
}

func TestAProjectOffersCodeStarters(t *testing.T) {
	h, p, ctl := start(t, reviewedWork().New)
	dir := t.TempDir()
	write(t, dir, "main.go", "go.mod", "notes.md")
	ctl.NewChat(dir)
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	if !tt.HasText("Explain this project") || tt.HasText("Summarize these documents") {
		t.Fatalf("starters %q", tt.Texts())
	}
	if tt.HasText("Reviewed work") {
		t.Fatal("code starters claim reviewed work")
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

func TestAnEmptyFolderOffersTheFirstDocument(t *testing.T) {
	h, p, ctl := start(t)
	ctl.NewChat(t.TempDir())
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	if !tt.HasText("Start a document") || !tt.HasText("What is in this folder?") {
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

func TestMarkdownIsADocument(t *testing.T) {
	h, p, ctl := start(t)
	dir := t.TempDir()
	write(t, dir, "brief.md")
	ctl.NewChat(dir)
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	if !tt.HasText("Summarize these documents") {
		t.Fatalf("starters %q", tt.Texts())
	}
}

func TestChangingTheFolderChangesTheStarters(t *testing.T) {
	h, p, ctl := start(t)
	docs := t.TempDir()
	write(t, docs, "report.docx")
	code := t.TempDir()
	write(t, code, "main.go")
	ctl.NewChat(docs)
	h.Flush()
	tt := ui.NewTester(p.view, 700, 500)
	tt.Frame()
	if !tt.HasText("Summarize these documents") {
		t.Fatalf("documents: %q", tt.Texts())
	}
	ctl.SetDirectory(code)
	h.Flush()
	tt.Frame()
	if !tt.HasText("Explain this project") || tt.HasText("Summarize these documents") {
		t.Fatalf("code: %q", tt.Texts())
	}
}
