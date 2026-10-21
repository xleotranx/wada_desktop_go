package opencode

import (
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/settings"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// form is the connection section's unsaved edit.
type form struct {
	conn  Connection
	dirty bool
	err   string
}

func (p *Plugin) contributeSection(c *kernel.Context) error {
	return kernel.Contribute(c, settings.Sections, "opencode.connection", 10, settings.Section{
		Title:       "OpenCode",
		Description: "Run OpenCode on this Mac, or connect to an OpenCode server.",
		Render:      p.section,
	})
}

func (p *Plugin) section(c *ui.Context) {
	pal := native.PaletteFor(c)
	if !p.form.dirty {
		p.form.conn = p.conn.Get()
	}
	edit := func() { p.form.dirty, p.form.err = true, "" }
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
		tint := pal.Muted
		if p.status.State == agent.Ready {
			tint = pal.GreenBadge
		}
		ui.Box(c).Size(7, 7).Radius(4).Background(tint)
		ui.Text(c, statusText(p.status)).FontSize(12.5).TextColor(pal.Muted).Grow(1).MinWidth(0)
	})
	ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
	ui.Text(c, "Connection mode").FontSize(14).FontWeight(500).TextColor(pal.Text)
	ui.Row(c).Gap(8).Children(func() {
		native.Choice(c, "Run locally", !p.form.conn.Remote, func() { p.form.conn.Remote = false; edit() })
		native.Choice(c, "Connect to a server", p.form.conn.Remote, func() { p.form.conn.Remote = true; edit() })
	})
	if p.form.conn.Remote {
		url := components.TextBox(c, &p.form.conn.URL, components.TextBoxOptions{}).FillWidth().
			Label("Server address").Placeholder("http://host:4096").FontSize(13)
		url.OnChange(edit)
		pw := components.TextBox(c, &p.form.conn.Password, components.TextBoxOptions{}).FillWidth().
			Label("Password").Placeholder("Password").FontSize(13).Password()
		pw.OnChange(edit)
	} else {
		ui.Text(c, "Wada starts OpenCode on this Mac and stops it when you quit.").FontSize(12).TextColor(pal.Muted)
	}
	if p.form.err != "" {
		ui.Text(c, p.form.err).FontSize(12).TextColor(pal.Danger)
	}
	ui.Row(c).Gap(8).Justify(ui.End).FillWidth().Children(func() {
		components.Button(c, "Reconnect", components.ButtonSecondary).OnClick(p.Reconnect)
		save := components.Button(c, "Save", components.ButtonPrimary)
		if !p.form.dirty {
			save.Disabled(true)
		}
		save.OnClick(func() {
			if err := p.Configure(p.form.conn); err != nil {
				p.form.err = err.Error()
				return
			}
			p.form = form{}
		})
	})
}

func statusText(st agent.Status) string {
	switch st.State {
	case agent.Ready:
		line := "Connected to " + st.Endpoint
		if st.Version != "" {
			line += " · OpenCode " + st.Version
		}
		return line
	case agent.Connecting:
		return "Connecting…"
	case agent.Down:
		if st.Err != nil {
			return "Not connected: " + st.Err.Error()
		}
		return "Not connected"
	case agent.Stopped:
	}
	return "Stopped"
}
