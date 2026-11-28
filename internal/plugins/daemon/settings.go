package daemon

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
	"wada-desktop-go/internal/client"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/protocol"
)

type remoteForm struct {
	hosts         []client.HostRecord
	newName       string
	newTarget     string
	newPassword   string
	newCredential string
	newRelayToken string
	// passwords are the hosts' passwords for this run of the app, by name;
	// they are never written to disk.
	passwords      map[string]string
	authFailed     bool   // the active host refused the connection
	authPassword   string // typed in answer to that
	daemonPassword string
	pairingURL     string
	pairingKey     string
	notice         string
	err            string

	loadHostsTask  kernel.Task
	saveHostTask   kernel.Task
	removeHostTask kernel.Task
	pairTask       kernel.Task
	passwordTask   kernel.Task
}

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
		p.remote.loadHostsTask.Cancel()
		p.remote.saveHostTask.Cancel()
		p.remote.removeHostTask.Cancel()
		p.remote.pairTask.Cancel()
		p.remote.passwordTask.Cancel()
	})
}

func (p *Plugin) daemonSection(c *ui.Context) {
	pal := native.PaletteFor(c)
	activeHost := ""
	if p.activeHost != nil {
		activeHost = p.activeHost.Get()
	}
	targetLabel := cmp.Or(activeHost, "Local wadad")

	text := "Connecting to " + targetLabel + "…"
	switch {
	case p.status.Err != nil:
		text = "Daemon error: " + p.status.Err.Error()
	case p.status.State == agent.Ready:
		mode := "external"
		if p.managed {
			mode = "managed"
		} else if activeHost != "" {
			mode = "remote"
		}
		ver := cmp.Or(p.status.Version, version)
		text = targetLabel + " (" + ver + " · " + mode + ")"
	case p.status.State == agent.Down:
		text = targetLabel + " disconnected"
	case p.status.State == agent.Stopped:
		text = targetLabel + " stopped"
	}
	ui.Text(c, text).TextColor(pal.Text)
	if p.status.Origin != "" {
		ui.Text(c, p.status.Origin).FontSize(11.5).TextColor(pal.Muted)
	}
	ui.Row(c).Gap(8).Children(func() {
		components.Button(c, "Reconnect", components.ButtonSecondary).OnClick(p.Reconnect)
		if activeHost == "" {
			components.Button(c, "Restart Daemon", components.ButtonSecondary).OnClick(p.Restart)
		}
	})
	p.remoteSettingsSection(c, pal, activeHost)
}

func (p *Plugin) remoteSettingsSection(c *ui.Context, pal native.Palette, activeHost string) {
	if p.remote.err != "" {
		ui.Text(c, p.remote.err).FontSize(12).TextColor(pal.DangerText)
	}
	if p.remote.notice != "" {
		ui.Text(c, p.remote.notice).FontSize(12).TextColor(pal.GreenBadge)
	}
	if p.remote.authFailed && activeHost != "" {
		ui.Column(c.Key("host-auth")).FillWidth().Gap(6).Padding(8, 0, 0, 0).Children(func() {
			ui.Text(c, activeHost+" needs its password").FontSize(13).FontWeight(600).TextColor(pal.Text)
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c.Key("host-auth-input")).Grow(1).MinWidth(0).Children(func() {
					components.TextBox(c, &p.remote.authPassword, components.TextBoxOptions{}).
						FillWidth().Placeholder("Remote daemon password").Password().OnSubmit(p.retryWithPassword)
				})
				components.Button(c, "Connect", components.ButtonPrimary).OnClick(p.retryWithPassword)
			})
		})
	}

	if p.status.State == agent.Ready {
		ui.Column(c.Key("daemon-pairing")).FillWidth().Gap(8).Padding(8, 0, 0, 0).Children(func() {
			ui.Text(c, "Remote pairing & password").FontSize(13).FontWeight(600).TextColor(pal.Text)
			ui.Row(c).Gap(8).Children(func() {
				components.Button(c, "Copy Pairing Link", components.ButtonSecondary).OnClick(p.copyPairingLink)
			})
			if p.remote.pairingURL != "" {
				ui.Box(c).FillWidth().Padding(8).Radius(6).Background(pal.DetailBg).Children(func() {
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						ui.Text(c, p.remote.pairingURL).Font("monospace").FontSize(11).TextColor(pal.Text)
						if p.remote.pairingKey != "" {
							ui.Text(c, "Key: "+p.remote.pairingKey).Font("monospace").FontSize(10.5).TextColor(pal.Muted)
						}
					})
				})
			}
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c.Key("daemon-pw-input")).Grow(1).MinWidth(0).Children(func() {
					components.TextBox(c, &p.remote.daemonPassword, components.TextBoxOptions{}).
						FillWidth().Placeholder("Optional remote daemon password").Password()
				})
				components.Button(c, "Set Password", components.ButtonSecondary).OnClick(func() {
					// Clearing is the Clear button's: an empty field is a slip.
					if p.remote.daemonPassword == "" {
						p.remote.err = "Enter a password to set, or use Clear to remove it"
						p.remote.notice = ""
						p.ctx.Invalidate()
						return
					}
					p.updateDaemonPassword(p.remote.daemonPassword)
				})
				components.Button(c, "Clear", components.ButtonGhost).OnClick(func() {
					p.remote.daemonPassword = ""
					p.updateDaemonPassword("")
				})
			})
		})
	}

	ui.Column(c.Key("daemon-hosts")).FillWidth().Gap(8).Padding(8, 0, 0, 0).Children(func() {
		ui.Text(c, "Saved daemon hosts").FontSize(13).FontWeight(600).TextColor(pal.Text)
		p.hostRow(c, pal, "Local wadad", "", "Default local daemon", activeHost == "")
		for _, h := range p.remote.hosts {
			rec := h
			detail := cmp.Or(rec.RelayURL, rec.Endpoint)
			p.hostRow(c, pal, rec.Name, rec.Name, detail, activeHost == rec.Name)
		}

		ui.Text(c, "Add remote host").FontSize(12).FontWeight(500).TextColor(pal.Muted).Padding(4, 0, 0, 0)
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			ui.Box(c.Key("host-name")).Width(120).Children(func() {
				components.TextBox(c, &p.remote.newName, components.TextBoxOptions{}).FillWidth().Placeholder("Name")
			})
			ui.Box(c.Key("host-target")).Grow(1).MinWidth(0).Children(func() {
				components.TextBox(c, &p.remote.newTarget, components.TextBoxOptions{}).
					FillWidth().Placeholder("#offer= link or host:port")
			})
		})
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			ui.Box(c.Key("host-credential")).Grow(1).MinWidth(0).Children(func() {
				components.TextBox(c, &p.remote.newCredential, components.TextBoxOptions{}).
					FillWidth().Placeholder("Daemon credential (optional)").Password()
			})
			ui.Box(c.Key("host-relay-token")).Grow(1).MinWidth(0).Children(func() {
				components.TextBox(c, &p.remote.newRelayToken, components.TextBoxOptions{}).
					FillWidth().Placeholder("Relay token (optional)").Password()
			})
		})
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Box(c.Key("host-pass")).Grow(1).MinWidth(0).Children(func() {
				components.TextBox(c, &p.remote.newPassword, components.TextBoxOptions{}).
					FillWidth().Placeholder("Remote password (optional)").Password()
			})
			components.Button(c, "Add & Connect", components.ButtonPrimary).OnClick(p.addAndConnectHost)
		})
	})
}

func (p *Plugin) hostRow(c *ui.Context, pal native.Palette, label, hostID, detail string, active bool) {
	ui.Row(c.Key("host-row:"+cmp.Or(hostID, "local"))).FillWidth().AlignItems(ui.Center).Gap(8).
		Padding(6, 8).Radius(6).Background(pal.Raised).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(pal.Text).SingleLine()
			ui.Text(c, detail).FontSize(11).TextColor(pal.Muted).SingleLine().Ellipsis("…")
		})
		if active {
			ui.Box(c).Padding(2, 8).Radius(99).Background(pal.GreenBadge.Alpha(.15)).Children(func() {
				ui.Text(c, "Active").FontSize(11).FontWeight(500).TextColor(pal.GreenBadge)
			})
		} else {
			id := hostID
			components.Button(c, "Connect", components.ButtonSecondary).OnClick(func() { p.switchHost(id) })
		}
		if hostID != "" {
			id := hostID
			components.Button(c, "Remove", components.ButtonGhost).OnClick(func() { p.removeHost(id) })
		}
	})
}

func (p *Plugin) loadHosts() {
	dataDir := p.host.DataDir()
	kernel.Go(p.ctx, &p.remote.loadHostsTask, func(context.Context) ([]client.HostRecord, error) {
		return client.LoadHosts(dataDir)
	}, func(list []client.HostRecord, err error) {
		if err != nil {
			p.remote.err = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.remote.hosts = list
		p.ctx.Invalidate()
	})
}

// switchHost connects to a saved host, with the password given for it in
// this run of the app, if any.
func (p *Plugin) switchHost(name string) {
	if p.activeHost == nil {
		return
	}
	p.remote.err = ""
	p.remote.notice = ""
	_ = p.activeHost.Set(name)
	p.Reconnect()
}

func (p *Plugin) setHostPassword(name, pw string) {
	if p.remote.passwords == nil {
		p.remote.passwords = map[string]string{}
	}
	if pw == "" {
		delete(p.remote.passwords, name)
		return
	}
	p.remote.passwords[name] = pw
}

// retryWithPassword reconnects to the active host that refused the
// connection, with the password typed for it.
func (p *Plugin) retryWithPassword() {
	if p.activeHost == nil {
		return
	}
	name := p.activeHost.Get()
	if name == "" {
		return
	}
	p.setHostPassword(name, p.remote.authPassword)
	p.remote.authPassword = ""
	p.switchHost(name)
}

func (p *Plugin) addAndConnectHost() {
	name := strings.TrimSpace(p.remote.newName)
	target := strings.TrimSpace(p.remote.newTarget)
	pw := p.remote.newPassword
	if name == "" || target == "" {
		p.remote.err = "Host name and #offer= link or endpoint are required"
		p.ctx.Invalidate()
		return
	}
	rec := client.HostRecord{
		Name:       name,
		Credential: strings.TrimSpace(p.remote.newCredential),
		RelayToken: strings.TrimSpace(p.remote.newRelayToken),
	}
	rec.Offer, rec.Endpoint = client.ParseHostTarget(target)
	dataDir := p.host.DataDir()
	kernel.Go(p.ctx, &p.remote.saveHostTask, func(context.Context) (struct{}, error) {
		return struct{}{}, client.SaveHost(dataDir, rec)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.remote.err = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.remote.newName = ""
		p.remote.newTarget = ""
		p.remote.newPassword = ""
		p.remote.newCredential = ""
		p.remote.newRelayToken = ""
		p.remote.err = ""
		p.setHostPassword(name, pw)
		p.loadHosts()
		p.switchHost(name)
	})
}

func (p *Plugin) removeHost(name string) {
	dataDir := p.host.DataDir()
	wasActive := p.activeHost != nil && p.activeHost.Get() == name
	kernel.Go(p.ctx, &p.remote.removeHostTask, func(context.Context) (struct{}, error) {
		return struct{}{}, client.RemoveHost(dataDir, name)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.remote.err = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.setHostPassword(name, "")
		p.loadHosts()
		if wasActive {
			p.switchHost("")
		}
	})
}

func (p *Plugin) copyPairingLink() {
	cli, err := p.current()
	if err != nil {
		p.remote.err = err.Error()
		p.ctx.Invalidate()
		return
	}
	kernel.Go(p.ctx, &p.remote.pairTask, func(ctx context.Context) (protocol.PairingOfferResponse, error) {
		return cli.PairingOffer(ctx, "")
	}, func(resp protocol.PairingOfferResponse, err error) {
		if err != nil {
			p.remote.err = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.remote.err = ""
		p.remote.pairingURL = resp.URL
		p.remote.pairingKey = resp.ServerPubKey
		if cerr := p.host.Copy(resp.URL); cerr == nil {
			p.remote.notice = "Copied pairing link to clipboard"
			p.host.Toast(ui.Toast{Title: "Copied pairing link", Type: "success"})
		} else {
			p.remote.notice = "Pairing link ready"
		}
		p.ctx.Invalidate()
	})
}

func (p *Plugin) updateDaemonPassword(pw string) {
	cli, err := p.current()
	if err != nil {
		p.remote.err = err.Error()
		p.ctx.Invalidate()
		return
	}
	kernel.Go(p.ctx, &p.remote.passwordTask, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, cli.SetPassword(ctx, pw)
	}, func(_ struct{}, err error) {
		if err != nil {
			p.remote.err = err.Error()
			p.ctx.Invalidate()
			return
		}
		p.remote.err = ""
		p.remote.daemonPassword = ""
		if host := p.activeHost.Get(); host != "" {
			// The connection to it stays open; the next one needs the password.
			p.setHostPassword(host, pw)
		}
		if pw == "" {
			p.remote.notice = "Remote daemon password cleared"
		} else {
			p.remote.notice = "Remote daemon password updated"
		}
		p.ctx.Invalidate()
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
	if p.run == nil {
		p.resetLoginState()
		p.providers.err = agent.ErrNotConnected.Error()
		p.ctx.Invalidate()
		return
	}
	p.providers.busy = true
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
	// The login it ends must not be cancelled or answered meanwhile.
	p.providers.loginTask.Cancel()
	p.providers.answerTask.Cancel()
	// Each cancel has a task of its own: a later login or cancel must not
	// replace it before it reaches the daemon. The connection's scope ends it.
	kernel.Go(p.run, new(kernel.Task), func(ctx context.Context) (struct{}, error) {
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
