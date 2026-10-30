package folderpicker

import (
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Chat, *plugintest.Workspace) {
	t.Helper()
	ctl := &plugintest.Chat{}
	ws := &plugintest.Workspace{FolderSet: []workspace.Folder{{Path: "/w/app"}}}
	var p *Plugin
	h := kerneltest.New(t, ctl.New, ws.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	h.Flush()
	return h, p, ctl, ws
}

func TestComposerChipSetsTheNewChatsFolder(t *testing.T) {
	h, p, ctl, ws := start(t)
	ctl.NewChat("")
	tt := ui.NewTester(p.chip, 400, 300)
	tt.Frame()
	if !tt.HasText("No folder") {
		t.Fatalf("chip %q", tt.Texts())
	}
	p.pick("/w/app")
	tt.Frame()
	if ctl.Conv.Directory != "/w/app" || !tt.HasText("app") {
		t.Fatalf("conv %+v chip %q", ctl.Conv, tt.Texts())
	}
	ws.Chosen = "/w/other"
	p.pick(chooseFolder)
	h.Flush()
	if ctl.Conv.Directory != "/w/other" {
		t.Fatalf("conv %+v", ctl.Conv)
	}
	p.pick(noFolder)
	ctl.Open(agent.Session{ID: "s1"})
	tt.Frame()
	if ctl.Conv.Directory != "" || tt.HasText("No folder") {
		t.Fatalf("a stored conversation shows the chip: %q", tt.Texts())
	}
}

func TestOpenFolderStartsANewChatThere(t *testing.T) {
	h, _, ctl, ws := start(t)
	ctl.Open(agent.Session{ID: "s1", Directory: "/w/app"})
	ws.Chosen = "/w/picked"
	for _, e := range kernel.Items(h, kernel.Commands) {
		if e.ID == "folderpicker.open-folder" {
			kernel.Invoke(h, e, func(c kernel.Command) { c.Run() })
		}
	}
	h.Flush()
	if ctl.Conv.Session != "" || ctl.Conv.Directory != "/w/picked" {
		t.Fatalf("conv %+v", ctl.Conv)
	}
}
