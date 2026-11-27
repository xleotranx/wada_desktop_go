package daemon

import (
	"context"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/protocol"
)

type providerForm struct {
	list                                  []protocol.ProviderInfo
	loaded                                bool
	query, selected, answer, message, err string
	loginID                               string
	busy, awaitingAnswer, secret          bool
	prompt                                string
	options                               []protocol.ProviderSelectOption
	load                                  kernel.Task
	loginTask                             kernel.Task
	answerTask                            kernel.Task
	logoutTask                            kernel.Task
}

func (p *Plugin) contributeSettings(c *kernel.Context) {
	kernel.Contribute(c, settings.Sections, "daemon.agent", 10, settings.Section{
		Title:       "Daemon",
		Description: "The background coding-agent daemon (wadad). Conversations keep running even if you close this window.",
		Render:      p.daemonSection,
	})
	kernel.Contribute(c, settings.Sections, "daemon.providers", 11, settings.Section{
		Title:       "Providers",
		Description: "Connect the model providers wadad uses. Credentials stay on the daemon.",
		Render:      p.providersSection,
	})
	c.OnDispose(func() {
		p.resetLoginState()
		p.providers.load.Cancel()
		p.providers.loginTask.Cancel()
		p.providers.answerTask.Cancel()
		p.providers.logoutTask.Cancel()
	})
}

func (p *Plugin) daemonSection(c *ui.Context) {
	pal := native.PaletteFor(c)
	text := "Connecting to wadad…"
	switch {
	case p.status.Err != nil:
		text = "Daemon error: " + p.status.Err.Error()
	case p.status.State == agent.Ready:
		mode := "external"
		if p.managed {
			mode = "managed"
		}
		ver := p.status.Version
		if ver == "" {
			ver = version
		}
		text = "wadad " + ver + " · " + mode
	case p.status.State == agent.Down:
		text = "Daemon disconnected"
	case p.status.State == agent.Stopped:
		text = "Daemon stopped"
	}
	ui.Text(c, text).TextColor(pal.Text)
	if p.status.Origin != "" {
		ui.Text(c, p.status.Origin).FontSize(11.5).TextColor(pal.Muted)
	}
	ui.Row(c).Gap(8).Children(func() {
		components.Button(c, "Reconnect", components.ButtonSecondary).OnClick(p.Reconnect)
		components.Button(c, "Restart Daemon", components.ButtonSecondary).OnClick(p.Restart)
	})
}

func (p *Plugin) loadProviders() {
	if p.run == nil {
		return
	}
	kernel.Go(p.run, &p.providers.load, func(ctx context.Context) ([]protocol.ProviderInfo, error) {
		cli, err := p.current()
		if err != nil {
			return nil, err
		}
		return cli.Providers(ctx)
	}, func(list []protocol.ProviderInfo, err error) {
		p.providers.loaded = true
		if err != nil {
			p.providers.err = "Could not load providers"
			p.ctx.Invalidate()
			return
		}
		p.providers.err = ""
		p.providers.list = list
		p.ctx.Invalidate()
	})
}

func (p *Plugin) providersSection(c *ui.Context) {
	pal := native.PaletteFor(c)
	components.TextBox(c, &p.providers.query, components.TextBoxOptions{}).Placeholder("Search providers").FillWidth()
	if p.providers.err != "" {
		ui.Text(c, p.providers.err).TextColor(pal.Danger)
	}
	if p.providers.selected != "" {
		ui.Column(c.Key("provider-login")).FillWidth().Gap(8).Children(func() {
			ui.Text(c, p.providers.selected).FontWeight(600).TextColor(pal.Text)
			if p.providers.message != "" {
				ui.Text(c, p.providers.message).TextColor(pal.Muted)
			}
			if p.providers.awaitingAnswer {
				ui.Text(c, p.providers.prompt).TextColor(pal.Text)
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
			components.Button(c, "Cancel", components.ButtonSecondary).OnClick(p.cancelLogin)
		})
		return
	}
	for _, pr := range p.providers.list {
		if !strings.Contains(strings.ToLower(pr.Name+" "+pr.ID), strings.ToLower(p.providers.query)) {
			continue
		}
		c.Key(pr.ID)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, pr.Name).Grow(1).TextColor(pal.Text)
			if pr.Connected {
				ui.Text(c, "Connected · "+pr.Source).TextColor(pal.Muted)
				components.Button(c, "Disconnect", components.ButtonSecondary).OnClick(func() { p.disconnect(pr.ID) })
			} else {
				if pr.SupportsAPIKey {
					components.Button(c, "API key", components.ButtonSecondary).OnClick(func() { p.login(pr.ID, protocol.ProviderAuthAPIKey) })
				}
				if pr.SupportsOAuth {
					components.Button(c, "Sign in", components.ButtonSecondary).OnClick(func() { p.login(pr.ID, protocol.ProviderAuthOAuth) })
				}
			}
		})
	}
}

func (p *Plugin) resetLoginState() {
	p.providers.loginID = ""
	p.providers.selected = ""
	p.providers.answer = ""
	p.providers.awaitingAnswer = false
	p.providers.secret = false
	p.providers.prompt = ""
	p.providers.options = nil
	p.providers.message = ""
	p.providers.busy = false
}

func (p *Plugin) login(id string, kind protocol.ProviderAuthType) {
	p.cancelLogin()
	loginID := uuid.NewString()
	p.providers.loginID = loginID
	p.providers.selected = id
	p.providers.err = ""
	p.providers.busy = true
	if p.run == nil {
		return
	}
	kernel.Go(p.run, &p.providers.loginTask, func(ctx context.Context) (string, error) {
		cli, err := p.current()
		if err != nil {
			return "", err
		}
		return cli.StartProviderLogin(ctx, protocol.ProviderLoginStartRequest{
			LoginID:    loginID,
			ProviderID: id,
			AuthType:   kind,
		})
	}, func(_ string, err error) {
		if err != nil && p.providers.loginID == loginID {
			p.resetLoginState()
			p.providers.err = "Could not start provider login."
			p.ctx.Invalidate()
		}
	})
}

func (p *Plugin) answerLogin(value string) {
	loginID := p.providers.loginID
	if loginID == "" || p.run == nil {
		return
	}
	p.providers.awaitingAnswer = false
	p.providers.answer = ""
	p.providers.options = nil
	kernel.Go(p.run, &p.providers.answerTask, func(ctx context.Context) (struct{}, error) {
		cli, err := p.current()
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, cli.AnswerProviderLogin(ctx, loginID, value)
	}, func(_ struct{}, err error) {
		if err != nil && p.providers.loginID == loginID {
			p.providers.err = "Could not submit credential response."
			p.ctx.Invalidate()
		}
	})
}

func (p *Plugin) cancelLogin() {
	loginID := p.providers.loginID
	p.resetLoginState()
	if loginID == "" || p.run == nil {
		return
	}
	kernel.Go(p.run, &p.providers.loginTask, func(ctx context.Context) (struct{}, error) {
		cli, err := p.current()
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, cli.CancelProviderLogin(ctx, loginID)
	}, func(struct{}, error) {})
}

func (p *Plugin) handleProviderAuth(ev *protocol.ProviderAuthEvent) {
	if ev == nil || ev.LoginID == "" || ev.LoginID != p.providers.loginID {
		return
	}
	switch ev.Kind {
	case protocol.ProviderAuthPromptText:
		p.providers.awaitingAnswer = true
		p.providers.answer = ""
		p.providers.options = nil
		p.providers.secret = false
		p.providers.prompt = ev.Message
	case protocol.ProviderAuthPromptSecret, protocol.ProviderAuthPromptCode:
		p.providers.awaitingAnswer = true
		p.providers.answer = ""
		p.providers.options = nil
		p.providers.secret = true
		p.providers.prompt = ev.Message
	case protocol.ProviderAuthPromptSelect:
		p.providers.awaitingAnswer = true
		p.providers.answer = ""
		p.providers.secret = false
		p.providers.prompt = ev.Message
		p.providers.options = slices.Clone(ev.Options)
	case protocol.ProviderAuthURL:
		p.providers.message = ev.Message
		if ev.URL != "" {
			p.host.OpenURL(ev.URL)
		}
	case protocol.ProviderAuthDeviceCode:
		if ev.UserCode != "" {
			p.providers.message = "Enter code " + ev.UserCode
		} else {
			p.providers.message = ev.Message
		}
		if ev.URL != "" {
			p.host.OpenURL(ev.URL)
		}
	case protocol.ProviderAuthInfo, protocol.ProviderAuthProgress:
		p.providers.message = ev.Message
	case protocol.ProviderAuthDone:
		p.resetLoginState()
		p.providers.err = ""
		kernel.Emit(p.ctx, agent.ModelsChanged, struct{}{})
		p.loadProviders()
	case protocol.ProviderAuthError:
		p.resetLoginState()
		if ev.Message != "" {
			p.providers.err = "The provider could not be connected: " + ev.Message
		} else {
			p.providers.err = "The provider could not be connected. Check your credentials and try again."
		}
		p.loadProviders()
	}
	p.ctx.Invalidate()
}

func (p *Plugin) disconnect(id string) {
	if p.run == nil {
		return
	}
	kernel.Go(p.run, &p.providers.logoutTask, func(ctx context.Context) (struct{}, error) {
		cli, err := p.current()
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, cli.LogoutProvider(ctx, id)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.providers.err = "The provider could not be disconnected"
		}
		p.loadProviders()
		p.ctx.Invalidate()
	})
}
