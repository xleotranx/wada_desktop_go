package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"

	"github.com/egoist/mygo"
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
	s, err := New(Options{Name: "Kit", Post: func(fn func()) { queue = append(queue, fn) }, Store: kerneltest.NewStore()}, plugins...)
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
	for _, want := range []string{"Badge", "Main page", "Main body"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	// Without a Strip view the page's title leads the title strip.
	if visibleTitle, ok := tt.Find("Main page"); !ok || visibleTitle.Y > float32(headerStrip) || visibleTitle.X > 24 {
		t.Fatalf("title does not lead the strip: %+v", visibleTitle)
	}
	if badge, ok := tt.Find("Badge"); !ok || badge.X+badge.W < 1200-16 {
		t.Fatalf("title-bar item is not at the end of the strip: %+v", badge)
	}
	if title != "Main page — Kit" {
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
}

func TestStripReplacesThePageTitle(t *testing.T) {
	p := &plugin{id: "tabs", setup: func(c *kernel.Context) error {
		kernel.Contribute(c, shell.Strip, "tabs.strip", 0, shell.View{Grow: true, Render: func(c *ui.Context) {
			ui.Row(c).Fill().Children(func() { ui.Text(c, "First tab") })
		}})
		kernel.Contribute(c, shell.TitleBar, "tabs.badge", 0, text("Badge"))
		return kernel.Contribute(c, shell.Pages, "tabs.main", 0, shell.Page{Title: func() string { return "Main page" },
			Render: func(c *ui.Context) { ui.Text(c, "Main body") }})
	}}
	s, _ := newShell(t, p)
	var title string
	s.opts.SetTitle = func(t string) { title = t }
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()
	if tt.HasText("Main page") {
		t.Fatalf("strip layout drew the page title: %q", tt.Texts())
	}
	if title != "Main page — Kit" {
		t.Fatalf("window title %q", title)
	}
	tab, ok := tt.Find("First tab")
	if !ok || tab.Y > float32(headerStrip) {
		t.Fatalf("strip view not in the title strip: %+v", tab)
	}
	if badge, ok := tt.Find("Badge"); !ok || badge.X+badge.W < 1200-16 || badge.Y > float32(headerStrip) {
		t.Fatalf("title-bar item is not at the end of the strip: %+v", badge)
	}
	// The page sits in the panel: below the strip and inset from the edges.
	if body, ok := tt.Find("Main body"); !ok || body.Y < float32(headerStrip) || body.X < 8 {
		t.Fatalf("page not in the panel: %+v", body)
	}
}

func TestShowPageAnnouncesEachChange(t *testing.T) {
	var got []string
	p := &plugin{id: "watch", setup: func(c *kernel.Context) error {
		kernel.On(c, shell.PageChanged, func(id string) { got = append(got, id) })
		return nil
	}}
	s, _ := newShell(t, p)
	host, _ := kernel.Use(s.Kernel, shell.HostKey)
	for _, id := range []string{"a", "a", "b", "a"} {
		host.ShowPage(id)
	}
	if !slices.Equal(got, []string{"a", "b", "a"}) {
		t.Fatalf("PageChanged %q", got)
	}
}

func TestPanickingViewFailsOnlyItsPlugin(t *testing.T) {
	bad := &plugin{id: "bad", setup: func(c *kernel.Context) error {
		return kernel.Contribute(c, shell.TitleBar, "bad.view", 0, shell.View{Render: func(*ui.Context) { panic("broken view") }})
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

func TestNoticesReachTheDesktopAndClicksReachPlugins(t *testing.T) {
	var shown []shell.Notice
	var clicked []string
	front := false
	listener := &plugin{id: "listener", setup: func(c *kernel.Context) error {
		kernel.On(c, shell.NoticeClicked, func(id string) { clicked = append(clicked, id) })
		return nil
	}}
	s, err := New(Options{Post: func(fn func()) { fn() }, Store: kerneltest.NewStore(),
		Notify: func(n shell.Notice) { shown = append(shown, n) }, Foreground: func() bool { return front }}, listener)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host, _ := kernel.Use(s.Kernel, shell.HostKey)
	host.Notify(shell.Notice{ID: "chat:s1", Title: "Done"})
	s.NoticeClicked("chat:s1")
	if len(shown) != 1 || shown[0].ID != "chat:s1" || host.Foreground() || len(clicked) != 1 || clicked[0] != "chat:s1" {
		t.Fatalf("shown %+v foreground %v clicked %q", shown, host.Foreground(), clicked)
	}
}

func TestDevelopmentKeepsItsOwnDataFolder(t *testing.T) {
	if dataFolder("notes", true) != "notes-dev" || dataFolder("notes", false) != "notes" {
		t.Fatalf("dev %q, production %q", dataFolder("notes", true), dataFolder("notes", false))
	}
}

// menuLabels names the menus after the app menu.
func menuLabels(m *mygo.Menu) []string {
	var out []string
	for _, it := range m.Items()[1:] {
		out = append(out, it.Label)
	}
	return out
}

func TestMenusFollowTheAppsOrder(t *testing.T) {
	p := &plugin{id: "notes", setup: func(c *kernel.Context) error {
		for i, menu := range []string{"Help", "Notes", "File", "Elsewhere"} {
			if err := kernel.Contribute(c, kernel.Commands, fmt.Sprintf("notes.c%d", i), i,
				kernel.Command{Title: "Run " + menu, Menu: menu, Run: func() {}}); err != nil {
				return err
			}
		}
		return nil
	}}
	for _, tc := range []struct {
		menus []string
		want  []string
	}{
		{[]string{"File", "Notes", "Help"}, []string{"File", "Edit", "Notes", "Help", "Window"}},
		{nil, []string{"File", "Edit", "View", "Help", "Window"}}, // View holds the developer tools
	} {
		var built *mygo.Menu
		s, err := New(Options{Menus: tc.menus, Post: func(fn func()) { fn() }, Store: kerneltest.NewStore(),
			SetMenu: func(m *mygo.Menu) { built = m }}, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		if got := menuLabels(built); !slices.Equal(got, tc.want) {
			t.Errorf("menus %q: menu bar %q, want %q", tc.menus, got, tc.want)
		}
		s.Close(context.Background())
	}
}

func TestAppNeedsOnePlainFolderName(t *testing.T) {
	for folder, ok := range map[string]bool{"notes": true, "": false, ".": false, "..": false,
		"../shared": false, "/tmp/notes": false, "a/b": false} {
		if err := (App{Name: "Notes", Folder: folder}).check(); (err == nil) != ok {
			t.Errorf("folder %q: %v", folder, err)
		}
	}
	if (App{Folder: "notes"}).check() == nil {
		t.Error("an App without a Name was accepted")
	}
}
