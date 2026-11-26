package pig

import (
	"cmp"
	"strings"

	"wada-desktop-go/internal/contract/agent"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

// providerNeeded reports that PiG is ready but no provider is connected, so a
// new chat has no model to work with. It is false until the provider list has
// loaded, so a connected provider never flashes a setup screen.
func (p *Plugin) providerNeeded() bool {
	if p.status.State != agent.Ready || !p.providers.loaded {
		return false
	}
	for _, pr := range p.providers.list {
		if pr.connected {
			return false
		}
	}
	return true
}

// providerSetup connects a provider from the first run's new chat. It is the
// same provider search, list and login form as Settings → Providers, without
// the settings furniture.
func (p *Plugin) providerSetup(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Column(c).Fill().Center().Padding(32).Children(func() {
		ui.Column(c).AlignItems(ui.Center).MaxWidth(520).Gap(16).Children(func() {
			ui.Box(c).Size(48, 48).Radius(14).Center().Background(pal.Orange.Alpha(.12)).Children(func() {
				ui.Icon(c, lucide.PlugZap()).FontSize(22).TextColor(pal.Orange)
			})
			ui.Column(c).AlignItems(ui.Center).Gap(7).Children(func() {
				ui.Text(c, "Connect a model provider").FontSize(24).FontWeight(600).TextColor(pal.Text).TextAlign(ui.Center)
				ui.Text(c, "Wada needs a model before it can work. Connect the provider you use; credentials stay in Wada.").
					FontSize(13).TextColor(pal.Muted).TextAlign(ui.Center)
			})
			if p.providers.selected != "" {
				p.providerLogin(c, pal)
				return
			}
			if p.providers.err != "" {
				ui.Text(c, p.providers.err).FontSize(12).TextColor(pal.Danger).TextAlign(ui.Center)
			}
			if len(p.providers.list) == 0 {
				ui.Text(c, "No providers are available. Try again after PiG starts.").
					FontSize(12).TextColor(pal.Muted).TextAlign(ui.Center)
				components.Button(c, "Try again", components.ButtonSecondary).OnClick(p.loadProviders)
				return
			}
			components.TextBox(c, &p.providers.query, components.TextBoxOptions{}).
				Placeholder("Search providers").FillWidth()
			query := strings.ToLower(strings.TrimSpace(p.providers.query))
			ui.Scroll(c).FillWidth().Height(280).Children(func() {
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					shown := 0
					for _, pr := range p.providers.list {
						if pr.connected || !strings.Contains(strings.ToLower(pr.name+" "+pr.id), query) {
							continue
						}
						shown++
						c.Key(pr.id)
						ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(10, 12).Radius(10).
							Background(pal.Raised).Border(1, pal.Hairline).Children(func() {
							ui.Text(c, cmp.Or(pr.name, pr.id)).Grow(1).MinWidth(0).FontSize(13.5).FontWeight(500).
								TextColor(pal.Text).SingleLine().Ellipsis("…")
							if pr.key {
								components.Button(c, "API key", components.ButtonSecondary).OnClick(func() { p.login(pr.id, ai.CredentialAPIKey) })
							}
							if pr.oauth {
								components.Button(c, "Sign in", components.ButtonSecondary).OnClick(func() { p.login(pr.id, ai.CredentialOAuth) })
							}
							if !pr.key && !pr.oauth {
								ui.Text(c, "Unavailable").FontSize(12).TextColor(pal.Muted)
							}
						})
					}
					if shown == 0 {
						ui.Text(c, "No providers match").FontSize(12).TextColor(pal.Muted)
					}
				})
			})
			ui.Text(c, "You can change providers later in Settings → Providers.").
				FontSize(11.5).TextColor(pal.Faint).TextAlign(ui.Center)
		})
	})
}

// providerLogin is the login form the setup and the settings section share.
func (p *Plugin) providerLogin(c *ui.Context, pal native.Palette) {
	ui.Column(c).FillWidth().MaxWidth(420).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Text(c, "Connect "+p.providerName(p.providers.selected)).FontSize(16).FontWeight(600).
			TextColor(pal.Text).TextAlign(ui.Center)
		if p.providers.message != "" {
			ui.Text(c, p.providers.message).FontSize(12).TextColor(pal.Muted).TextAlign(ui.Center)
		}
		if p.providers.busy && p.providers.reply == nil {
			ui.Text(c, "Connecting…").FontSize(12).TextColor(pal.Muted)
		}
		if p.providers.reply != nil {
			ui.Text(c, p.providers.prompt).FontSize(12.5).TextColor(pal.Text).TextAlign(ui.Center)
			if len(p.providers.options) > 0 {
				for _, option := range p.providers.options {
					c.Key(option.ID)
					components.Button(c, option.Label, components.ButtonSecondary).OnClick(func() { p.answerLogin(option.ID) })
				}
			} else {
				c.Key("provider-answer")
				input := components.TextBox(c, &p.providers.answer, components.TextBoxOptions{}).FillWidth()
				if p.providers.secret {
					input.Password()
				}
				components.Button(c, "Continue", components.ButtonPrimary).OnClick(func() { p.answerLogin(p.providers.answer) })
			}
		}
		components.Button(c, "Cancel", components.ButtonGhost).OnClick(p.cancelLogin)
	})
}

func (p *Plugin) providerName(id string) string {
	for _, pr := range p.providers.list {
		if pr.id == id {
			return cmp.Or(pr.name, pr.id)
		}
	}
	return id
}
