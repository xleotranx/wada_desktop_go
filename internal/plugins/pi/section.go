package pi

import (
	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

func (p *Plugin) contributeSection(c *kernel.Context) error {
	return kernel.Contribute(c, settings.Sections, "pi.settings", 11, settings.Section{
		Title:       "Pi",
		Description: "Pi runs on this Mac and keeps conversations as files in its session folder.",
		Render:      p.section,
	})
}

func (p *Plugin) section(c *ui.Context) {
	pal := native.PaletteFor(c)
	if !p.formDirty {
		p.form = p.cfg.Get()
	}
	edit := func() { p.formDirty = true }
	ui.Text(c, statusText(p.status)).FontSize(12.5).TextColor(pal.Muted)
	exe := components.TextBox(c, &p.form.Executable, components.TextBoxOptions{}).FillWidth().
		Label("Executable").Placeholder("pi (from PATH)").FontSize(13)
	exe.OnChange(edit)
	dir := components.TextBox(c, &p.form.SessionDir, components.TextBoxOptions{}).FillWidth().
		Label("Session folder").Placeholder("~/.pi/agent/sessions").FontSize(13)
	dir.OnChange(edit)
	ui.Row(c).Gap(8).Justify(ui.End).FillWidth().Children(func() {
		components.Button(c, "Restart Pi", components.ButtonSecondary).OnClick(p.Reconnect)
		save := components.Button(c, "Save", components.ButtonPrimary)
		if !p.formDirty {
			save.Disabled(true)
		}
		save.OnClick(func() {
			p.Configure(p.form)
			p.formDirty = false
		})
	})
}

func statusText(st agent.Status) string {
	switch st.State {
	case agent.Ready:
		if st.Version != "" {
			return "Running · Pi " + st.Version
		}
		return "Running"
	case agent.Connecting:
		return "Starting…"
	case agent.Down:
		if st.Err != nil {
			return "Not running: " + st.Err.Error()
		}
		return "Not running"
	case agent.Stopped:
	}
	return "Stopped"
}
