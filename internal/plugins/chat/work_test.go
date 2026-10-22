package chat

import (
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
)

// workPlugin contributes two work sections, one shown only while full is set.
type workPlugin struct{ full bool }

func (*workPlugin) Manifest() kernel.Manifest { return kernel.Manifest{ID: "work"} }

func (w *workPlugin) Setup(c *kernel.Context) error {
	if err := kernel.Contribute(c, chat.Work, "work.always", 0, chat.WorkSection{Title: "Always",
		Render: func(c *ui.Context) { ui.Text(c, "always body") }}); err != nil {
		return err
	}
	return kernel.Contribute(c, chat.Work, "work.full", 1, chat.WorkSection{Title: "Sometimes",
		Shown: func() bool { return w.full }, Render: func(c *ui.Context) { ui.Text(c, "sometimes body") }})
}

func TestWorkPanelShowsBesideAStoredConversation(t *testing.T) {
	w := &workPlugin{}
	h, p, _, _ := start(t, w)
	h.Flush()
	tt := ui.NewTester(p.view, 1100, 700)
	tt.Frame()
	if p.WorkShown() || tt.HasText("Always") {
		t.Fatalf("a new chat has a work panel: %q", tt.Texts())
	}
	p.Open(agent.Session{ID: "s1", Title: "One"})
	h.Flush()
	tt.Frame()
	if !p.WorkShown() || !tt.HasText("always body") || tt.HasText("Sometimes") {
		t.Fatalf("panel %v: %q", p.WorkShown(), tt.Texts())
	}
	if panel, ok := tt.Find("Work panel"); !ok || panel.X < 1100-workWidth-1 {
		t.Fatalf("the panel is not at the end: %+v", panel)
	}
	w.full = true
	tt.Frame()
	if !tt.HasText("sometimes body") {
		t.Fatalf("a section that has something to show is missing: %q", tt.Texts())
	}
	for _, e := range kernel.Items(h, kernel.Commands) {
		if e.ID == "chat.work" {
			e.Value.Run()
		}
	}
	tt.Frame()
	if p.WorkShown() || tt.HasText("always body") {
		t.Fatalf("Toggle Work Panel left it: %q", tt.Texts())
	}
}
