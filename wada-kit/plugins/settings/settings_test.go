package settings

import (
	"slices"
	"testing"

	"github.com/xleotranx/wada-kit/contract/settings"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"

	"github.com/egoist/mygo/ui"
)

type contributor struct{}

func (contributor) Manifest() kernel.Manifest { return kernel.Manifest{ID: "extra", Title: "Extra"} }
func (contributor) Setup(c *kernel.Context) error {
	return kernel.Contribute(c, settings.Sections, "extra.section", 50, settings.Section{
		Title: "Extra section", Render: func(c *ui.Context) { ui.Text(c, "Extra body") }})
}

func TestPageShowsSectionsAndChangesSettings(t *testing.T) {
	host := &kittest.Host{Mode: "system"}
	p := &Plugin{}
	h := kerneltest.New(t, host, p, contributor{})
	host.PluginList = h.Plugins()
	tt := ui.NewTester(p.view, 900, 1200)
	tt.Frame()
	for _, want := range []string{"Appearance", "Extra section", "Plugins"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if i, j := slices.Index(tt.Texts(), "Extra section"), slices.Index(tt.Texts(), "Plugins"); i > j {
		t.Fatal("sections are out of order")
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Extra body") {
		t.Fatal("inactive section rendered")
	}
	if err := tt.Click("Extra section"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Extra body") {
		t.Fatal("selected section did not render")
	}
	if err := tt.Click("Plugins"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Required") {
		t.Fatal("missing required plugin status")
	}
	if err := tt.Click("Turn off Extra"); err != nil {
		t.Fatal(err)
	}
	h.RunPosts()
	tt.Frame()
	if host.Mode != "dark" || !slices.Equal(host.Toggled, []string{"-extra"}) {
		t.Fatalf("mode %q toggled %q", host.Mode, host.Toggled)
	}
	if tt.HasText("Back to chat") {
		t.Fatal("the page leads back to the chat; its tab does that")
	}
}

func TestSelectionFallsBackWhenASectionIsRemoved(t *testing.T) {
	host := &kittest.Host{Mode: "system"}
	p := &Plugin{}
	h := kerneltest.New(t, host, p, contributor{})
	tt := ui.NewTester(p.view, 900, 550)
	if err := tt.Click("Extra section"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Extra body") {
		t.Fatal("section did not open")
	}
	if err := h.Disable("extra"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Extra body") || !tt.HasText("Color theme") {
		t.Fatalf("missing fallback: %q", tt.Texts())
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if host.Mode != "dark" {
		t.Fatal("appearance control stopped working after section removal")
	}
}
