package pig

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"
)

type providerItem struct {
	id, name, source      string
	key, oauth, connected bool
}
type providerForm struct {
	list                                  []providerItem
	query, selected, answer, message, err string
	busy, secret                          bool
	prompt                                string
	options                               []ai.AuthSelectOption
	reply                                 chan string
	scope                                 *kernel.Scope
	load                                  kernel.Task
}

func (p *Plugin) contributeSettings(c *kernel.Context) {
	kernel.Contribute(c, settings.Sections, "pig.agent", 10, settings.Section{Title: "PiG", Description: "The embedded coding agent. Wada owns its configuration and conversations.", Render: p.agentSection})
	kernel.Contribute(c, settings.Sections, "pig.providers", 11, settings.Section{Title: "Providers", Description: "Connect the model providers PiG uses. Credentials belong to Wada.", Render: p.providersSection})
	c.OnDispose(func() {
		if p.providers.scope != nil {
			p.providers.scope.Dispose()
		}
		p.providers.answer = ""
	})
}
func (p *Plugin) agentSection(c *ui.Context) {
	pal := native.PaletteFor(c)
	text := "Starting PiG…"
	if p.status.Err != nil {
		text = "PiG could not start: " + p.status.Err.Error()
	} else if p.status.Version != "" {
		text = "PiG " + p.status.Version + " · embedded"
	}
	ui.Text(c, text).TextColor(pal.Text)
	components.Button(c, "Restart PiG", components.ButtonSecondary).OnClick(p.Restart)
}
func (p *Plugin) loadProviders() {
	kernel.Go(p.run, &p.providers.load, func(ctx context.Context) ([]providerItem, error) {
		e, release, err := p.acquire()
		defer release()
		if err != nil {
			return nil, err
		}
		var list []providerItem
		for _, pr := range e.models.GetProviders() {
			item := providerItem{id: pr.ID, name: pr.Name, key: pr.Auth.APIKey != nil, oauth: pr.Auth.OAuth != nil}
			auth, err := e.models.CheckAuth(ctx, pr.ID)
			if err == nil && auth != nil {
				item.connected = true
				item.source = string(auth.Source)
			}
			list = append(list, item)
		}
		slices.SortFunc(list, func(a, b providerItem) int {
			if a.connected != b.connected {
				if a.connected {
					return -1
				}
				return 1
			}
			return strings.Compare(a.name, b.name)
		})
		return list, nil
	}, func(list []providerItem, err error) {
		if err != nil {
			p.providers.err = "Could not load providers"
			return
		}
		p.providers.list = list
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
			if p.providers.reply != nil {
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
		if !strings.Contains(strings.ToLower(pr.name+" "+pr.id), strings.ToLower(p.providers.query)) {
			continue
		}
		c.Key(pr.id)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, pr.name).Grow(1).TextColor(pal.Text)
			if pr.connected {
				ui.Text(c, "Connected · "+pr.source).TextColor(pal.Muted)
				components.Button(c, "Disconnect", components.ButtonSecondary).OnClick(func() { p.disconnect(pr.id) })
			} else {
				if pr.key {
					components.Button(c, "API key", components.ButtonSecondary).OnClick(func() { p.login(pr.id, ai.CredentialAPIKey) })
				}
				if pr.oauth {
					components.Button(c, "Sign in", components.ButtonSecondary).OnClick(func() { p.login(pr.id, ai.CredentialOAuth) })
				}
			}
		})
	}
}
func (p *Plugin) answerLogin(value string) {
	if ch := p.providers.reply; ch != nil {
		select {
		case ch <- value:
			p.providers.reply = nil
			p.providers.answer = ""
		default:
		}
	}
}
func (p *Plugin) cancelLogin() {
	if p.providers.scope != nil {
		p.providers.scope.Dispose()
	}
	p.providers.scope = nil
	p.providers.selected = ""
	p.providers.answer = ""
	p.providers.reply = nil
	p.providers.options = nil
	p.providers.message = ""
	p.providers.busy = false
}
func (p *Plugin) login(id string, kind ai.AuthType) {
	p.cancelLogin()
	p.providers.selected = id
	p.providers.err = ""
	p.providers.busy = true
	scope := p.run.NewScope()
	p.providers.scope = scope
	scope.Spawn(func(ctx context.Context) {
		e, release, err := p.acquire()
		defer release()
		if err != nil {
			return
		}
		interaction := ai.AuthInteraction{
			Prompt: func(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
				reply := make(chan string, 1)
				scope.Post(func() {
					p.providers.reply = reply
					p.providers.answer = ""
					p.providers.options = nil
					p.providers.secret = false
					switch prompt := prompt.(type) {
					case ai.AuthTextPrompt:
						p.providers.prompt = prompt.Message
					case ai.AuthSecretPrompt:
						p.providers.prompt = prompt.Message
						p.providers.secret = true
					case ai.AuthManualCodePrompt:
						p.providers.prompt = prompt.Message
						p.providers.secret = true
					case ai.AuthSelectPrompt:
						p.providers.prompt = prompt.Message
						p.providers.options = slices.Clone(prompt.Options)
					}
				})
				select {
				case answer := <-reply:
					return answer, nil
				case <-ctx.Done():
					return "", ctx.Err()
				case <-scope.Lifetime().Done():
					return "", context.Canceled
				}
			},
			Notify: func(event ai.AuthEvent) {
				scope.Post(func() {
					switch ev := event.(type) {
					case ai.AuthURLEvent:
						p.providers.message = ev.Instructions
						p.host.OpenURL(ev.URL)
					case ai.AuthDeviceCodeEvent:
						p.providers.message = "Enter code " + ev.UserCode
						p.host.OpenURL(ev.VerificationURI)
					case ai.AuthInfoEvent:
						p.providers.message = ev.Message
					case ai.AuthProgressEvent:
						p.providers.message = ev.Message
					}
				})
			},
		}
		_, err = e.models.Login(ctx, id, kind, interaction)
		scope.Post(func() {
			p.providers.answer = ""
			p.providers.reply = nil
			p.providers.busy = false
			if err != nil && !errors.Is(err, context.Canceled) {
				p.providers.err = "The provider could not be connected. Check your credentials and try again."
			}
			p.cancelLogin()
			p.loadProviders()
		})
	})
}
func (p *Plugin) disconnect(id string) {
	scope := p.run.NewScope()
	scope.Spawn(func(ctx context.Context) {
		e, release, err := p.acquire()
		defer release()
		if err == nil {
			err = e.models.Logout(ctx, id)
		}
		scope.Post(func() {
			if err != nil {
				p.providers.err = "The provider could not be disconnected"
			}
			p.loadProviders()
			scope.Dispose()
		})
	})
}
