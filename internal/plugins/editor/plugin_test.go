package editor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/tabs"
	"wada-desktop-go/internal/plugintest"
)

type fileBackend struct{}

func (*fileBackend) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "editor-fake", Provides: []kernel.Named{agent.EditorKey}}
}

func (f *fileBackend) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.WorkspaceEditor](c, agent.EditorKey, f)
}

func (*fileBackend) FindWorkspaceFiles(context.Context, agent.WorkspaceID, string) ([]string, error) {
	return []string{"notes.txt"}, nil
}

func (*fileBackend) OpenWorkspaceFile(context.Context, agent.WorkspaceID, string) (agent.WorkspaceFile, error) {
	return agent.WorkspaceFile{Text: "original", Revision: "r0"}, nil
}

func (*fileBackend) SaveWorkspaceFile(context.Context, agent.WorkspaceID, string, string, string) (agent.WorkspaceFile, error) {
	return agent.WorkspaceFile{}, errors.New("file changed on disk")
}

func TestSaveConflictKeepsTheEditedDraft(t *testing.T) {
	ws := &plugintest.Workspace{Dirs: []string{"/w/repo"}}
	var p *Plugin
	h := kerneltest.New(t, ws.New, func() kernel.Plugin { return &fileBackend{} },
		kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	tab := tabs.Tab{Folder: "/w/repo", Pane: pane}
	p.show(tab)
	tt := ui.NewTester(func(c *ui.Context) { p.view(c, tab) }, 820, 550)
	wait := func(test func() bool) {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			h.Flush()
			tt.Frame()
			if test() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("state never appeared: %v", tt.Texts())
	}
	wait(func() bool { return tt.HasText("notes.txt") })
	if err := tt.Click("notes.txt"); err != nil {
		t.Fatal(err)
	}
	w, _ := ws.At(tab.Folder)
	d := p.docs[w.ID]
	wait(func() bool { return d.file.Text == "original" })
	d.text = "edited draft"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return tt.HasText("Save failed: file changed on disk") })
	if !d.dirty() || d.text != "edited draft" {
		t.Fatalf("conflict lost draft: %q", d.text)
	}
}
