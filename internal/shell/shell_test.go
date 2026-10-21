package shell

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"

	"github.com/egoist/mygo/ui"
)

type plugin struct {
	id    string
	setup func(*kernel.Context) error
}

func (p *plugin) Manifest() kernel.Manifest     { return kernel.Manifest{ID: p.id, Title: p.id} }
func (p *plugin) Setup(c *kernel.Context) error { return p.setup(c) }

func text(s string) shell.View {
	return shell.View{Render: func(c *ui.Context) { ui.Text(c, s) }}
}

// newShell starts a lenient shell whose posts run when the test calls run.
func newShell(t *testing.T, plugins ...kernel.Plugin) (*Shell, func()) {
	t.Helper()
	var queue []func()
	s, err := New(Options{Post: func(fn func()) { queue = append(queue, fn) }, Store: kerneltest.NewStore()}, plugins...)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.Close(ctx)
	})
	return s, func() {
		for len(queue) > 0 {
			fn := queue[0]
			queue = queue[1:]
			fn()
		}
	}
}

func TestShellDrawsContributionsAndSwitchesPages(t *testing.T) {
	ran := 0
	p := &plugin{id: "notes", setup: func(c *kernel.Context) error {
		kernel.Contribute(c, shell.Sidebar, "notes.list", 0, text("Sidebar section"))
		kernel.Contribute(c, shell.TitleBar, "notes.badge", 0, text("Badge"))
		kernel.Contribute(c, shell.Pages, "notes.main", 0, shell.Page{Title: func() string { return "Main page" }, Render: func(c *ui.Context) { ui.Text(c, "Main body") }})
		kernel.Contribute(c, shell.Pages, "notes.other", 1, shell.Page{Render: func(c *ui.Context) { ui.Text(c, "Other body") }})
		return kernel.Contribute(c, kernel.Commands, "notes.run", 0, kernel.Command{Title: "Run", Keys: []string{"cmd+shift+r"}, Run: func() { ran++ }})
	}}
	s, _ := newShell(t, p)
	var title string
	s.opts.SetTitle = func(t string) { title = t }
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()
	for _, want := range []string{"Sidebar section", "Badge", "Main page", "Main body"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if title != "Main page — Wada" {
		t.Fatalf("window title %q", title)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyR)
	if ran != 1 {
		t.Fatalf("shortcut ran %d times", ran)
	}
	host, _ := kernel.Use(s.Kernel, shell.HostKey)
	host.ShowPage("notes.other")
	tt.Frame()
	if !tt.HasText("Other body") || tt.HasText("Main body") {
		t.Fatalf("page switch: %q", tt.Texts())
	}
	if err := tt.Click("Hide sidebar"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Sidebar section") {
		t.Fatal("sidebar still shown")
	}
}

func TestPanickingViewFailsOnlyItsPlugin(t *testing.T) {
	bad := &plugin{id: "bad", setup: func(c *kernel.Context) error {
		return kernel.Contribute(c, shell.Sidebar, "bad.view", 0, shell.View{Render: func(*ui.Context) { panic("broken view") }})
	}}
	good := &plugin{id: "good", setup: func(c *kernel.Context) error {
		return kernel.Contribute(c, shell.Pages, "good.page", 0, shell.Page{Render: func(c *ui.Context) { ui.Text(c, "Still here") }})
	}}
	s, _ := newShell(t, bad, good)
	tt := ui.NewTester(s.View, 1000, 700)
	tt.Frame()
	tt.Frame()
	st := s.Kernel.Plugins()
	i := slices.IndexFunc(st, func(x kernel.Status) bool { return x.Manifest.ID == "bad" })
	if st[i].State != kernel.Failed || !tt.HasText("Still here") {
		t.Fatalf("bad=%v texts=%q", st[i].State, tt.Texts())
	}
	if !slices.ContainsFunc(s.host.toasts, func(t ui.Toast) bool { return strings.Contains(t.Title, "bad stopped working") }) &&
		!tt.HasText("bad stopped working") {
		t.Fatal("the failure was not reported")
	}
}

func TestAccelerators(t *testing.T) {
	for in, want := range map[string]accelerator{
		"cmd+n":        {ui.Cmd, ui.KeyN, "CmdOrCtrl+N"},
		"Cmd+Shift+.":  {ui.Cmd | ui.Shift, ui.KeyPeriod, "CmdOrCtrl+Shift+."},
		"ctrl+alt+f12": {ui.Ctrl | ui.Alt, ui.KeyF12, "Ctrl+Alt+F12"},
		"shift+escape": {ui.Shift, ui.KeyEscape, "Shift+Escape"},
	} {
		got, err := parseAccelerator(in)
		if err != nil || got != want {
			t.Errorf("parseAccelerator(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "cmd+", "hyper+k", "cmd+nope"} {
		if _, err := parseAccelerator(bad); err == nil {
			t.Errorf("parseAccelerator(%q) accepted", bad)
		}
	}
}

func TestSQLiteStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	err = s.Save(ctx, []kernel.Value{
		{Namespace: "a", Key: "x", JSON: json.RawMessage(`1`)},
		{Namespace: "a", Key: "y", JSON: json.RawMessage(`"two"`)},
	})
	if err == nil {
		err = s.Save(ctx, []kernel.Value{{Namespace: "a", Key: "x", JSON: json.RawMessage(`3`)}, {Namespace: "a", Key: "y"}})
	}
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	values, err := s.Load(ctx)
	if err != nil || len(values) != 1 || values[0].Key != "x" || string(values[0].JSON) != "3" {
		t.Fatalf("values %+v, %v", values, err)
	}
	if _, err := s.db.Execute(ctx, "PRAGMA user_version = 9"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := openStore(path); err == nil || !strings.Contains(err.Error(), "schema 9") {
		t.Fatalf("other schema opened: %v", err)
	}
}
