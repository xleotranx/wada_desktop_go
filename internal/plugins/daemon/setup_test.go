package daemon

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/protocol"
)

func TestProviderSetupIsNeededUntilAProviderConnects(t *testing.T) {
	p := &Plugin{status: agent.Status{State: agent.Ready}}
	p.providers.loaded = true
	p.providers.list = []protocol.ProviderInfo{{ID: "opencode-go", Name: "OpenCode Go", SupportsAPIKey: true}}
	if !p.providerNeeded() {
		t.Fatal("a ready daemon with no connected provider does not ask for one")
	}
	p.providers.list[0].Connected = true
	if p.providerNeeded() {
		t.Fatal("a connected provider still asks for one")
	}
	p.providers.list = nil
	if !p.providerNeeded() {
		t.Fatal("a loaded empty provider list does not ask for one")
	}
	p.providers.loaded = false
	if p.providerNeeded() {
		t.Fatal("the setup shows before the provider list has loaded")
	}
	p.providers.loaded = true
	p.status.State = agent.Connecting
	if p.providerNeeded() {
		t.Fatal("the setup shows while the daemon is still connecting")
	}
}

func TestProviderSetupListsProvidersAndTheirActions(t *testing.T) {
	p := &Plugin{status: agent.Status{State: agent.Ready}}
	p.providers = providerForm{loaded: true, list: []protocol.ProviderInfo{
		{ID: "opencode-go", Name: "OpenCode Go", SupportsAPIKey: true, SupportsOAuth: true},
		{ID: "other", Name: "Other", SupportsAPIKey: true},
	}}
	tt := ui.NewTester(p.providerSetup, 900, 700)
	tt.Frame()
	for _, want := range []string{"Connect a model provider", "OpenCode Go", "API key", "Sign in", "Other"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
}

func TestProviderSetupShowsTheLoginPrompt(t *testing.T) {
	p := &Plugin{status: agent.Status{State: agent.Ready}}
	p.providers = providerForm{
		loaded:         true,
		selected:       "opencode-go",
		prompt:         "Paste the API key",
		awaitingAnswer: true,
		secret:         true,
		list:           []protocol.ProviderInfo{{ID: "opencode-go", Name: "OpenCode Go", SupportsAPIKey: true}},
	}
	tt := ui.NewTester(p.providerSetup, 900, 700)
	tt.Frame()
	if !tt.HasText("Connect OpenCode Go") || !tt.HasText("Paste the API key") || !tt.HasText("Continue") {
		t.Fatalf("login form %q", tt.Texts())
	}
}

func TestProviderSetupFiltersTheList(t *testing.T) {
	p := &Plugin{status: agent.Status{State: agent.Ready}}
	p.providers = providerForm{loaded: true, query: "open", list: []protocol.ProviderInfo{
		{ID: "opencode-go", Name: "OpenCode Go", SupportsAPIKey: true},
		{ID: "other", Name: "Other", SupportsAPIKey: true},
	}}
	tt := ui.NewTester(p.providerSetup, 900, 700)
	tt.Frame()
	if !tt.HasText("OpenCode Go") || tt.HasText("Other") {
		t.Fatalf("filtered list %q", tt.Texts())
	}
}
