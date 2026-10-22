package opencode

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/settings"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// popular are the providers listed before the user searches.
var popular = []string{"anthropic", "openai", "google", "openrouter", "github-copilot", "opencode", "opencode-go", "deepseek", "mistral", "groq"}

// signInPoll is how often a browser sign-in is checked.
var signInPoll = 1500 * time.Millisecond

// providers is the Providers section's state. UI thread.
type providers struct {
	list    []provider
	loaded  bool
	err     string
	attempt uint64 // the connection attempt the list came from
	load    kernel.Task
	act     kernel.Task
	query   string

	// The connect form of one provider.
	open    string // provider ID, or ""
	method  int    // index into the provider's offered methods
	key     string
	answer  map[string]*string // every form field's answer, by key
	busy    bool
	formErr string
	signIn  *attempt
	code    string
	poll    *kernel.Scope // checks an automatic sign-in
}

func (p *Plugin) contributeProviders(c *kernel.Context) error {
	return kernel.Contribute(c, settings.Sections, "opencode.providers", 11, settings.Section{
		Title:       "Providers",
		Description: "Connect the model providers OpenCode uses. Wada keeps them apart from the OpenCode CLI.",
		Render:      p.providersSection,
	})
}

// statusForProviders reloads the list once per connection.
func (p *Plugin) statusForProviders(st agent.Status) {
	if st.State == agent.Ready && st.Attempt != p.prov.attempt {
		p.prov.attempt = st.Attempt
		p.loadProviders()
	}
}

func (p *Plugin) loadProviders() {
	kernel.Go(p.ctx, &p.prov.load, func(ctx context.Context) ([]provider, error) {
		var out []provider
		err := p.with(func(c *client) (err error) { out, err = c.providers(ctx); return err })
		return out, err
	}, func(list []provider, err error) {
		if err != nil {
			p.prov.err = err.Error()
			return
		}
		p.prov.list, p.prov.loaded, p.prov.err = list, true, ""
	})
}

// providersChanged reloads the list and tells the model picker after a provider was
// connected or removed.
func (p *Plugin) providersChanged() {
	p.loadProviders()
	kernel.Emit(p.ctx, agent.ModelsChanged, struct{}{})
}

// openForm opens the connect form of provider id, with every field of its
// methods at its default.
func (p *Plugin) openForm(pr provider) {
	p.closeForm()
	p.prov.open, p.prov.answer = pr.ID, map[string]*string{}
	for _, m := range offeredMethods(pr) {
		for _, f := range m.Form {
			if p.prov.answer[f.Key] == nil {
				v := f.Default
				p.prov.answer[f.Key] = &v
			}
		}
	}
}

func (p *Plugin) closeForm() {
	if a, id := p.prov.signIn, p.prov.open; a != nil {
		// Best effort: an abandoned attempt also expires on the server.
		kernel.Go(p.ctx, new(kernel.Task), func(ctx context.Context) (struct{}, error) {
			return struct{}{}, p.with(func(c *client) error { return c.cancelSignIn(ctx, id, a.ID) })
		}, func(struct{}, error) {})
	}
	p.stopPoll()
	p.prov.act.Cancel()
	p.prov.open, p.prov.method, p.prov.key, p.prov.answer = "", 0, "", nil
	p.prov.busy, p.prov.formErr, p.prov.signIn, p.prov.code = false, "", nil, ""
}

func (p *Plugin) stopPoll() {
	if p.prov.poll != nil {
		p.prov.poll.Dispose()
		p.prov.poll = nil
	}
}

func (p *Plugin) answers() map[string]string {
	out := map[string]string{}
	for k, v := range p.prov.answer {
		out[k] = *v
	}
	return out
}

// act runs one mutation of the open form. It is never retried: on failure
// the form shows why and the user decides.
func (p *Plugin) act(work func(context.Context, *client) error, done func()) {
	p.prov.busy, p.prov.formErr = true, ""
	kernel.Go(p.ctx, &p.prov.act, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.with(func(c *client) error { return work(ctx, c) })
	}, func(_ struct{}, err error) {
		p.prov.busy = false
		if err != nil {
			p.prov.formErr = err.Error()
			return
		}
		done()
	})
}

func (p *Plugin) connect(pr provider, m method) {
	answer, err := formAnswer(m, p.answers())
	if err != nil {
		p.prov.formErr = err.Error()
		return
	}
	connected := func() { p.closeForm(); p.providersChanged() }
	switch m.Type {
	case "key":
		key := strings.TrimSpace(p.prov.key)
		if key == "" {
			p.prov.formErr = "Enter the API key"
			return
		}
		p.act(func(ctx context.Context, c *client) error { return c.connectKey(ctx, pr.ID, key, answer) }, connected)
	case "external":
		p.act(func(ctx context.Context, c *client) error { return c.connectExternal(ctx, pr.ID, m.ID, answer) }, connected)
	case "oauth":
		var a attempt
		p.act(func(ctx context.Context, c *client) (err error) {
			a, err = c.beginSignIn(ctx, pr.ID, m.ID, answer)
			return err
		}, func() {
			p.prov.signIn = &a
			p.host.OpenURL(a.URL)
			if a.Mode != "code" {
				p.watchSignIn(pr.ID, a.ID)
			}
		})
	}
}

// watchSignIn checks an automatic sign-in until it ends.
func (p *Plugin) watchSignIn(id, attemptID string) {
	p.stopPoll()
	poll := p.ctx.NewScope()
	p.prov.poll = poll
	poll.Spawn(func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(signInPoll):
			}
			var status, message string
			err := p.with(func(c *client) (err error) {
				status, message, err = c.signInStatus(ctx, id, attemptID)
				return err
			})
			if ctx.Err() != nil {
				return
			}
			if err == nil && status == "pending" {
				continue
			}
			poll.Post(func() {
				p.prov.poll = nil
				switch {
				case err != nil:
					p.prov.formErr = err.Error()
				case status == "complete":
					p.prov.signIn = nil
					p.closeForm()
					p.providersChanged()
				default:
					p.prov.signIn, p.prov.formErr = nil, cmp.Or(message, "The sign-in did not finish")
				}
				poll.Dispose()
			})
			return
		}
	})
}

func (p *Plugin) disconnect(conn connection) {
	kernel.Go(p.ctx, &p.prov.act, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, p.with(func(c *client) error { return c.removeCredential(ctx, conn.ID) })
	}, func(_ struct{}, err error) {
		if err != nil {
			p.prov.err = err.Error()
			return
		}
		p.providersChanged()
	})
}

// shown is what the list holds: matches of the query, or the popular
// providers before a search. Connected providers are listed above it.
func (p *Plugin) shown() []provider {
	q := strings.ToLower(strings.TrimSpace(p.prov.query))
	var out []provider
	for _, pr := range p.prov.list {
		if len(pr.Connections) > 0 {
			continue
		}
		if q == "" {
			if slices.Contains(popular, pr.ID) {
				out = append(out, pr)
			}
		} else if strings.Contains(strings.ToLower(pr.Name), q) || strings.Contains(pr.ID, q) {
			out = append(out, pr)
		}
	}
	if q == "" {
		slices.SortFunc(out, func(a, b provider) int {
			return slices.Index(popular, a.ID) - slices.Index(popular, b.ID)
		})
	}
	return out[:min(len(out), 30)]
}

func offeredMethods(pr provider) []method {
	var out []method
	for _, m := range pr.Methods {
		if m.offered() {
			out = append(out, m)
		}
	}
	return out
}

func (p *Plugin) providersSection(c *ui.Context) {
	pal := native.PaletteFor(c)
	if p.status.State != agent.Ready {
		ui.Text(c, "Providers appear once OpenCode is connected.").FontSize(12.5).TextColor(pal.Muted)
		return
	}
	if p.prov.err != "" {
		ui.Text(c, p.prov.err).FontSize(12).TextColor(pal.Danger)
	}
	if !p.prov.loaded {
		ui.Text(c, "Loading providers…").FontSize(12.5).TextColor(pal.Muted)
		return
	}
	var connected []provider
	for _, pr := range p.prov.list {
		if len(pr.Connections) > 0 {
			connected = append(connected, pr)
		}
	}
	ui.Text(c, "Connected").FontSize(14).FontWeight(500).TextColor(pal.Text)
	if len(connected) == 0 {
		ui.Text(c, "No provider is connected yet. Connect one below to choose its models.").FontSize(12).TextColor(pal.Muted)
	}
	for _, pr := range connected {
		c.Key("connected:" + pr.ID)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Padding(4, 0).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, pr.Name).FontSize(13.5).FontWeight(500).TextColor(pal.Text)
				for _, conn := range pr.Connections {
					line := conn.Label
					if conn.Type == "env" {
						line = "From $" + conn.Name
					}
					if conn.Status != nil && conn.Status.Message != "" {
						line += " · " + conn.Status.Message
					}
					ui.Text(c, line).FontSize(12).TextColor(pal.Muted)
				}
			})
			for _, conn := range pr.Connections {
				if conn.Type != "credential" {
					continue
				}
				c.Key("remove:" + conn.ID)
				components.Button(c, "Disconnect", components.ButtonSecondary).OnClick(func() { p.disconnect(conn) })
			}
		})
	}
	ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
	ui.Text(c, "Add a provider").FontSize(14).FontWeight(500).TextColor(pal.Text)
	c.Key("providers.search")
	components.TextBox(c, &p.prov.query, components.TextBoxOptions{}).FillWidth().
		Placeholder("Search " + strconv.Itoa(len(p.prov.list)) + " providers").FontSize(13)
	list := p.shown()
	if len(list) == 0 {
		ui.Text(c, "No provider matches.").FontSize(12).TextColor(pal.Muted)
	}
	for i, pr := range list {
		if i > 0 {
			ui.Box(c).FillWidth().Height(1).Background(pal.Hairline)
		}
		c.Key("provider:" + pr.ID)
		ui.Column(c).FillWidth().Gap(12).Padding(4, 0).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, pr.Name).FontSize(13.5).FontWeight(500).TextColor(pal.Text)
					ui.Text(c, pr.ID).FontSize(11.5).TextColor(pal.Muted)
				})
				if p.prov.open != pr.ID {
					b := components.Button(c, "Connect", components.ButtonSecondary)
					if len(offeredMethods(pr)) == 0 {
						b.Disabled(true)
					}
					b.OnClick(func() { p.openForm(pr) })
				}
			})
			if p.prov.open == pr.ID {
				p.connectForm(c, pr)
			}
		})
	}
}

func (p *Plugin) connectForm(c *ui.Context, pr provider) {
	pal := native.PaletteFor(c)
	methods := offeredMethods(pr)
	if len(methods) == 0 {
		return
	}
	p.prov.method = min(p.prov.method, len(methods)-1)
	m := methods[p.prov.method]
	if a := p.prov.signIn; a != nil {
		if a.Instructions != "" {
			ui.Text(c, a.Instructions).FontSize(12.5).TextColor(pal.Text)
		}
		if a.Mode == "code" {
			c.Key("signin.code")
			components.TextBox(c, &p.prov.code, components.TextBoxOptions{}).FillWidth().
				Label("Code").Placeholder("Paste the code the provider shows").FontSize(13)
		} else {
			ui.Text(c, "Waiting for you to finish signing in…").FontSize(12).TextColor(pal.Muted)
		}
	} else {
		if len(methods) > 1 {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for i, mm := range methods {
					c.Key("method:" + strconv.Itoa(i))
					native.Choice(c, mm.title(), i == p.prov.method, func() { p.prov.method, p.prov.formErr = i, "" })
				}
			})
		}
		answers := p.answers()
		for _, f := range m.Form {
			if !f.visible(answers) {
				continue
			}
			v := p.prov.answer[f.Key]
			c.Key("field:" + f.Key)
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				if len(f.Options) > 0 && !f.Custom {
					ui.Text(c, cmp.Or(f.Title, f.Key)).FontSize(12.5).TextColor(pal.Text)
					ui.Row(c).Gap(8).Wrap().Children(func() {
						for _, o := range f.Options {
							c.Key("option:" + o.Value)
							native.Choice(c, o.Label, *v == o.Value, func() { *v = o.Value })
						}
					})
				} else {
					components.TextBox(c, v, components.TextBoxOptions{}).FillWidth().
						Label(cmp.Or(f.Title, f.Key)).Placeholder(f.Placeholder).FontSize(13)
				}
				if f.Description != "" {
					ui.Text(c, f.Description).FontSize(11.5).TextColor(pal.Muted)
				}
			})
		}
		if m.Type == "key" {
			c.Key("field.key")
			components.TextBox(c, &p.prov.key, components.TextBoxOptions{}).FillWidth().
				Label("API key").Placeholder("Paste the key").FontSize(13).Password()
		}
	}
	if p.prov.formErr != "" {
		ui.Text(c, p.prov.formErr).FontSize(12).TextColor(pal.Danger)
	}
	ui.Row(c).Gap(8).Justify(ui.End).FillWidth().Children(func() {
		components.Button(c, "Cancel", components.ButtonSecondary).OnClick(p.closeForm)
		switch a := p.prov.signIn; {
		case a != nil:
			components.Button(c, "Open sign-in page", components.ButtonSecondary).OnClick(func() { p.host.OpenURL(a.URL) })
			if a.Mode == "code" {
				finish := components.Button(c, "Finish", components.ButtonPrimary)
				finish.Disabled(p.prov.busy || strings.TrimSpace(p.prov.code) == "")
				finish.OnClick(func() {
					id, code := pr.ID, strings.TrimSpace(p.prov.code)
					p.act(func(ctx context.Context, c *client) error { return c.finishSignIn(ctx, id, a.ID, code) },
						func() { p.prov.signIn = nil; p.closeForm(); p.providersChanged() })
				})
			}
		default:
			label := "Connect"
			if m.Type == "oauth" {
				label = "Sign in"
			}
			primary := components.Button(c, label, components.ButtonPrimary)
			primary.Disabled(p.prov.busy)
			primary.OnClick(func() { p.connect(pr, m) })
		}
	})
}
