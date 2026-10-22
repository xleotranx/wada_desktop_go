package opencode

import (
	"slices"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

// providersReady starts the plugin on f and waits for its provider list.
func providersReady(t *testing.T, f *fakeServer) (*kerneltest.Harness, *Plugin, *ui.Tester) {
	t.Helper()
	h, p := start(t, f)
	f.stream(t)
	waitFor(t, h, "providers", func() bool { return p.prov.loaded })
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Children(func() { p.providersSection(c) }) }, 700, 900)
	tt.Frame()
	return h, p, tt
}

func linked(p *Plugin, id string) bool {
	for _, pr := range p.prov.list {
		if pr.ID == id {
			return len(pr.Connections) > 0
		}
	}
	return false
}

func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v in %q", label, err, tt.Texts())
	}
	tt.Frame()
}

func TestProvidersConnectWithAKey(t *testing.T) {
	f := newFakeServer(t)
	h, p, tt := providersReady(t, f)
	if !tt.HasText("OpenCode Go") || !tt.HasText("Anthropic") {
		t.Fatalf("section %q", tt.Texts())
	}
	var changed int
	kernel.On(p.ctx, agent.ModelsChanged, func(struct{}) { changed++ })
	p.prov.query = "anthropic"
	tt.Frame()
	click(t, tt, "Connect")
	click(t, tt, "Connect")
	if p.prov.formErr != "Enter the API key" || slices.Contains(f.seen(), "POST /integration/anthropic/connect/key") {
		t.Fatalf("empty key sent: %q %v", p.prov.formErr, f.seen())
	}
	p.prov.key = " sk-1 "
	tt.Frame()
	click(t, tt, "Connect")
	waitFor(t, h, "anthropic connected", func() bool { return linked(p, "anthropic") })
	if got := f.body("POST /integration/anthropic/connect/key"); got != `{"answer":{},"key":"sk-1"}` {
		t.Fatalf("body %s", got)
	}
	if p.prov.open != "" || changed != 1 {
		t.Fatalf("form %q, models changed %d times", p.prov.open, changed)
	}
}

func TestProvidersAskTheMethodForm(t *testing.T) {
	f := newFakeServer(t)
	h, p, tt := providersReady(t, f)
	p.prov.query = "azure"
	tt.Frame()
	click(t, tt, "Connect")
	p.prov.key = "k"
	tt.Frame()
	click(t, tt, "Connect")
	if p.prov.formErr != "Resource name is required" {
		t.Fatalf("formErr %q", p.prov.formErr)
	}
	*p.prov.answer["resourceName"] = "models"
	tt.Frame()
	click(t, tt, "Connect")
	waitFor(t, h, "azure connected", func() bool { return linked(p, "azure") })
	if got := f.body("POST /integration/azure/connect/key"); got != `{"answer":{"resourceName":"models"},"key":"k"}` {
		t.Fatalf("body %s", got)
	}
}

func TestProvidersSignInInTheBrowser(t *testing.T) {
	defer func(d time.Duration) { signInPoll = d }(signInPoll)
	signInPoll = time.Millisecond
	f := newFakeServer(t)
	h, p, tt := providersReady(t, f)
	p.prov.query = "openai"
	tt.Frame()
	click(t, tt, "Connect")
	click(t, tt, "Sign in with ChatGPT")
	click(t, tt, "Sign in")
	waitFor(t, h, "sign-in started", func() bool { return p.prov.signIn != nil })
	tt.Frame()
	if host := p.host.(*plugintest.Host); !slices.Equal(host.Opened, []string{"https://auth.example/start"}) || !tt.HasText("Finish in your browser") {
		t.Fatalf("opened %v, section %q", host.Opened, tt.Texts())
	}
	waitFor(t, h, "openai connected", func() bool { return linked(p, "openai") })
	if p.prov.open != "" || p.prov.poll != nil {
		t.Fatalf("form %q still open", p.prov.open)
	}
}

func TestProvidersDisconnect(t *testing.T) {
	f := newFakeServer(t)
	h, p, tt := providersReady(t, f)
	click(t, tt, "Disconnect")
	waitFor(t, h, "disconnected", func() bool { return !linked(p, "opencode-go") })
	if !slices.Contains(f.seen(), "DELETE /credential/cred-opencode-go") {
		t.Fatalf("requests %v", f.seen())
	}
}

func TestFormAnswerFollowsConditions(t *testing.T) {
	m := method{Type: "oauth", Form: []field{
		{Key: "kind", Type: "string", Required: true, Options: []option{{"a", "A"}, {"b", "B"}}},
		{Key: "url", Type: "string", Required: true, When: []when{{Key: "kind", Op: "eq", Value: "b"}}},
		{Key: "server", Type: "string", Hidden: true, Default: "https://x"},
	}}
	got, err := formAnswer(m, map[string]string{"kind": "a", "url": "ignored"})
	if err != nil || len(got) != 2 || got["kind"] != "a" || got["server"] != "https://x" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := formAnswer(m, map[string]string{"kind": "b"}); err == nil {
		t.Fatal("a required conditional field was skipped")
	}
}
