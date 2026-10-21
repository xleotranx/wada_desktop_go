package settings

import (
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/settings"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

type contributor struct{}

func (contributor) Manifest() kernel.Manifest { return kernel.Manifest{ID: "extra", Title: "Extra"} }
func (contributor) Setup(c *kernel.Context) error {
	return kernel.Contribute(c, settings.Sections, "extra.section", 50, settings.Section{
		Title: "Extra section", Render: func(c *ui.Context) { ui.Text(c, "Extra body") }})
}

func TestPageShowsSectionsAndChangesSettings(t *testing.T) {
	host := &plugintest.Host{Mode: "system"}
	p := &Plugin{}
	h := kerneltest.New(t, host, p, contributor{})
	host.PluginList = h.Plugins()
	tt := ui.NewTester(p.view, 900, 1200)
	tt.Frame()
	for _, want := range []string{"Appearance", "Extra section", "Extra body", "Plugins", "Required"} {
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
	if err := tt.Click("Turn off Extra"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if host.Mode != "dark" || !slices.Equal(host.Toggled, []string{"-extra"}) {
		t.Fatalf("mode %q toggled %q", host.Mode, host.Toggled)
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	if host.PageID != "" {
		t.Fatalf("page %q after Done", host.PageID)
	}
}
