// Package shell is the native window host. It owns the kernel, the window's
// frame and layout, menus and shortcuts, and draws whatever plugins
// contribute to the points in contract/shell. It knows no feature.
package shell

import (
	"context"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/native"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/theme"
)

// Options configures a Shell. Post and Store are passed to the kernel.
type Options struct {
	// Name ends the window title, as "Page — Name".
	Name string
	// Menus are the menus of the menu bar between the app menu and Window,
	// in order: a command shows in the menu its Menu names, and one named
	// nowhere here in none. File is followed by Edit, and View ends with the
	// developer tools. Without them: File, View, Help.
	Menus  []string
	Post   func(func())
	Store  kernel.Store
	Strict bool
	// SetTitle and SetMenu reach the native window and menu bar; nil in tests.
	SetTitle func(string)
	SetMenu  func(*mygo.Menu)
	SetTheme func(mygo.ThemeSource)
	// SetVibrancy shows or hides the blurred material behind the window.
	SetVibrancy func(on bool)
	// Notify and Foreground reach the desktop; nil in tests, where nothing
	// is shown and the window counts as in front.
	Notify     func(shell.Notice)
	Foreground func() bool
	// Clipboard reads the clipboard for a paste; nil in tests, where it is empty.
	Clipboard func() shell.Clipboard
	// DataDir is where plugins keep their files; empty in tests.
	DataDir string
}

// Shell is a kernel plus the window that shows it.
type Shell struct {
	Kernel *kernel.Kernel
	host   *host
	opts   Options
	title  string
	accel  map[string]accelerator // parsed command shortcut strings, remembered so frames need not parse them
	keys   shortcutTable
	failed map[string]failure // the failure last reported for each plugin, by ID
}

// failure is what the shell last told the user about a plugin.
type failure uint8

const (
	working failure = iota
	restarting
	stopped
)

// restartPolicy brings a plugin back after a run-time failure, unless it
// fails again and again.
var restartPolicy = kernel.Restart{Max: 3, Within: time.Minute}

// New installs the shell's own plugin and then plugins, in that order.
func New(opts Options, plugins ...kernel.Factory) (*Shell, error) {
	s := &Shell{opts: opts, failed: map[string]failure{}}
	s.Kernel = kernel.New(kernel.Options{Post: opts.Post, Store: opts.Store, Strict: opts.Strict, Restart: restartPolicy})
	// The shell reaches its own plugin as the one set up now.
	newHost := func() kernel.Plugin {
		s.host = &host{k: s.Kernel, setTheme: opts.SetTheme, setVibrancy: opts.SetVibrancy, notify: opts.Notify,
			foreground: opts.Foreground, clipboard: opts.Clipboard, dataDir: opts.DataDir}
		return s.host
	}
	if err := s.Kernel.Install(append([]kernel.Factory{newHost}, plugins...)...); err != nil {
		return nil, err
	}
	return s, nil
}

// Start sets the plugins up and builds the menu bar.
func (s *Shell) Start() error {
	kernel.On(s.Kernel, kernel.PluginsChanged, s.pluginsChanged)
	kernel.On(s.Kernel, kernel.PointChanged, func(point string) {
		if point == kernel.Commands.Name() {
			s.buildMenu()
		}
	})
	err := s.Kernel.Start()
	s.buildMenu()
	return err
}

// Close disposes every plugin and waits for their workers.
func (s *Shell) Close(ctx context.Context) error { return s.Kernel.Close(ctx) }

// NoticeClicked tells plugins the user clicked the notification with this ID.
// Call it on the UI thread.
func (s *Shell) NoticeClicked(id string) { kernel.Emit(s.Kernel, shell.NoticeClicked, id) }

// pluginsChanged rebuilds the menu and reports plugins that just failed:
// one the kernel restarts as a warning, one that stays stopped as an error.
// The menu is rebuilt as well whenever the commands change.
func (s *Shell) pluginsChanged(all []kernel.Status) {
	s.buildMenu()
	for _, st := range all {
		now := working
		if st.State == kernel.Failed {
			now = stopped
			if st.Restarting {
				now = restarting
			}
		}
		if now != working && now != s.failed[st.Manifest.ID] {
			log.Printf("plugin %s failed: %v", st.Manifest.ID, st.Err)
			toast := ui.Toast{Title: st.Manifest.Title + " stopped working", Description: st.Err.Error(),
				Type: components.ToastError}
			if now == restarting {
				toast.Title, toast.Type = st.Manifest.Title+" restarted after an error", components.ToastWarning
			}
			s.host.Toast(toast)
		}
		s.failed[st.Manifest.ID] = now
	}
}

const (
	trafficLightButton = 14
	trafficLightY      = 13
	headerStrip        = trafficLightButton + 2*trafficLightY
)

// View draws one frame: the title strip across the top, past the window
// controls, and the page in a rounded panel below it. The strip holds the
// Strip views, or the page's title when there are none, then the TitleBar
// items at its end.
func (s *Shell) View(c *ui.Context) {
	theme.Apply(c)
	h := s.host
	h.services = c.Services()
	for _, t := range h.toasts {
		c.AddToast(t)
	}
	h.toasts = nil
	p := native.PaletteFor(c)
	if p.StripBg.A < 255 {
		c.Root().Background(ui.Transparent)
	}
	page, hasPage := s.currentPage()
	s.syncTitle(page, hasPage)
	s.shortcuts(c)
	bar := c.TitleBar()
	headerH := max(bar.Height, float32(headerStrip))
	strip := kernel.Items(s.Kernel, shell.Strip)
	ui.Column(c).Fill().Background(p.StripBg).Children(func() {
		ui.Row(c).FillWidth().Height(headerH).Shrink(0).DragWindow().Padding(0, 12, 0, bar.Left+12).Gap(8).
			Children(func() {
				if len(strip) == 0 {
					ui.Row(c).Grow(1).MinWidth(0).FillHeight().AlignItems(ui.Center).Children(func() {
						s.pageTitle(c, p, page, hasPage)
					})
				}
				for _, e := range strip {
					c.Key(e.ID)
					box := ui.Box(c).FillHeight()
					if e.Value.Grow {
						box.Grow(1).MinWidth(0)
					}
					box.Children(func() {
						kernel.Invoke(s.Kernel, e, func(v shell.View) { v.Render(c) })
					})
				}
				ui.Row(c).Shrink(0).AlignItems(ui.Center).Children(func() {
					s.render(c, kernel.Items(s.Kernel, shell.TitleBar))
				})
			})
		ui.Column(c).Fill().MinWidth(0).MinHeight(0).Padding(0, 8, 8, 8).Children(func() {
			ui.Column(c).Fill().MinWidth(0).MinHeight(0).Background(p.PanelBg).Radius(12).Clip().Children(func() {
				if hasPage {
					kernel.Invoke(s.Kernel, page, func(pg shell.Page) { pg.Render(c) })
				}
			})
		})
	})
	s.render(c, kernel.Items(s.Kernel, shell.Overlays))
	components.Toasts(c, components.ToastOptions{Placement: components.ToastTopEnd, Inset: headerH})
}

func (s *Shell) pageTitle(c *ui.Context, p native.Palette, page kernel.Entry[shell.Page], ok bool) {
	if !ok || page.Value.Title == nil {
		return
	}
	var title string
	kernel.Invoke(s.Kernel, page, func(pg shell.Page) { title = pg.Title() })
	ui.Text(c, title).FontSize(14).FontWeight(500).TextColor(p.Text).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
}

func (s *Shell) render(c *ui.Context, items []kernel.Entry[shell.View]) {
	for _, e := range items {
		c.Key(e.ID)
		box := ui.Box(c)
		if e.Value.Grow {
			box.Grow(1).MinHeight(0)
		}
		box.Children(func() {
			kernel.Invoke(s.Kernel, e, func(v shell.View) { v.Render(c) })
		})
	}
}

// currentPage is the page on show: the chosen one while it exists, else the
// default.
func (s *Shell) currentPage() (kernel.Entry[shell.Page], bool) {
	pages := kernel.Items(s.Kernel, shell.Pages)
	if len(pages) == 0 {
		return kernel.Entry[shell.Page]{}, false
	}
	if i := slices.IndexFunc(pages, func(e kernel.Entry[shell.Page]) bool { return e.ID == s.host.page }); i >= 0 {
		return pages[i], true
	}
	return pages[0], true
}

func (s *Shell) syncTitle(page kernel.Entry[shell.Page], ok bool) {
	title := s.opts.Name
	if ok && page.Value.Title != nil {
		kernel.Invoke(s.Kernel, page, func(pg shell.Page) {
			if t := pg.Title(); t != "" {
				title = strings.TrimSuffix(t+" — "+s.opts.Name, " — ")
			}
		})
	}
	if title != s.title {
		s.title = title
		if s.opts.SetTitle != nil {
			s.opts.SetTitle(title)
		}
	}
}

// shortcutTable is the window's shortcuts, built from the commands it was
// built from; Items returns the same slice until the commands change.
type shortcutTable struct {
	from     []kernel.Entry[kernel.Command]
	bindings []binding
}

type binding struct {
	accelerator
	run func()
}

// shortcuts registers window keys for commands that aren't in the menu bar;
// the menu bar owns its items' keys. The table is built again only when the
// commands change, so a frame registers the keys without building anything.
func (s *Shell) shortcuts(c *ui.Context) {
	commands := kernel.Items(s.Kernel, kernel.Commands)
	if t := &s.keys; len(commands) != len(t.from) || len(commands) > 0 && &commands[0] != &t.from[0] {
		t.from, t.bindings = commands, t.bindings[:0]
		for _, e := range commands {
			keys := e.Value.Keys
			if e.Value.Menu != "" && len(keys) > 0 {
				keys = keys[1:]
			}
			for _, k := range keys {
				if a, ok := s.accelerator(k); ok {
					t.bindings = append(t.bindings, binding{a, func() { s.run(e) }})
				}
			}
		}
	}
	for _, b := range s.keys.bindings {
		c.OnShortcut(b.mods, b.key, b.run)
	}
}

// accelerator parses a command shortcut string, remembering the parse: the
// call runs every frame, and the strings change only when commands do.
func (s *Shell) accelerator(k string) (accelerator, bool) {
	if s.accel == nil {
		s.accel = map[string]accelerator{}
	}
	if a, ok := s.accel[k]; ok {
		return a, a.key != 0 // the zero value marks a string that failed to parse
	}
	a, err := parseAccelerator(k)
	if err != nil {
		s.accel[k] = accelerator{}
		return accelerator{}, false
	}
	s.accel[k] = a
	return a, true
}

func (s *Shell) run(e kernel.Entry[kernel.Command]) {
	kernel.Invoke(s.Kernel, e, func(cmd kernel.Command) {
		if cmd.Run != nil && (cmd.Enabled == nil || cmd.Enabled()) {
			cmd.Run()
		}
	})
}

// defaultMenus are the menus of an app that names none.
var defaultMenus = []string{"File", "View", "Help"}

func (s *Shell) buildMenu() {
	if s.opts.SetMenu == nil {
		return
	}
	groups := map[string][]*mygo.MenuItem{}
	for _, e := range kernel.Items(s.Kernel, kernel.Commands) {
		if e.Value.Menu == "" {
			continue
		}
		item := &mygo.MenuItem{Label: e.Value.Title, Click: func(*mygo.MenuItem, *mygo.Window) {
			s.opts.Post(func() { s.run(e) })
		}}
		if len(e.Value.Keys) > 0 {
			if a, err := parseAccelerator(e.Value.Keys[0]); err == nil {
				item.Accelerator = a.menu
			}
		}
		groups[e.Value.Menu] = append(groups[e.Value.Menu], item)
	}
	menus := []*mygo.MenuItem{{Role: mygo.RoleAppMenu}}
	order := s.opts.Menus
	if len(order) == 0 {
		order = defaultMenus
	}
	for _, name := range order {
		if name == "View" {
			groups[name] = append(groups[name], mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleToggleDevTools})
		}
		if len(groups[name]) > 0 {
			menus = append(menus, &mygo.MenuItem{Label: name, Submenu: groups[name]})
		}
		if name == "File" {
			menus = append(menus, &mygo.MenuItem{Role: mygo.RoleEditMenu})
		}
	}
	menus = append(menus, &mygo.MenuItem{Role: mygo.RoleWindowMenu})
	s.opts.SetMenu(mygo.NewMenu(menus))
}

// host is the shell's own plugin: it provides shell.Host and the window's
// commands.
type host struct {
	k           *kernel.Kernel
	setTheme    func(mygo.ThemeSource)
	setVibrancy func(on bool)
	backdrop    *kernel.Setting[string]
	notify      func(shell.Notice)
	clipboard   func() shell.Clipboard
	foreground  func() bool
	dataDir     string
	appearance  *kernel.Setting[string]
	page        string
	toasts      []ui.Toast
	services    ui.Services
}

var _ shell.Host = (*host)(nil)

func (*host) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "shell", Title: "Window", Locked: true, Provides: []kernel.Named{shell.HostKey, kernel.InspectorKey}}
}

func (h *host) Setup(c *kernel.Context) error {
	h.appearance = kernel.NewSetting(c, "appearance", "dark")
	h.applyTheme(h.Appearance())
	h.backdrop = kernel.NewSetting(c, "backdrop", "clear")
	h.applyBackdrop(h.Backdrop())
	if err := kernel.Provide[shell.Host](c, shell.HostKey, h); err != nil {
		return err
	}
	return kernel.Provide[kernel.Inspector](c, kernel.InspectorKey, h.k)
}

func (h *host) ShowPage(id string) {
	if id == h.page {
		return
	}
	h.page = id
	kernel.Emit(h.k, shell.PageChanged, id)
}

func (h *host) Page() string { return h.page }

func (h *host) Toast(t ui.Toast) {
	h.toasts = append(h.toasts, t)
	h.services.Invalidate()
}

func (h *host) Copy(text string) error { return mygo.Clipboard.Write(transfer.TextData(text)) }

func (h *host) Clipboard() shell.Clipboard {
	if h.clipboard == nil {
		return shell.Clipboard{}
	}
	return h.clipboard()
}

func (h *host) OpenURL(url string) { h.services.OpenURL(url) }

func (h *host) Notify(n shell.Notice) {
	if h.notify != nil {
		h.notify(n)
	}
}

func (h *host) Foreground() bool { return h.foreground == nil || h.foreground() }
func (h *host) DataDir() string  { return h.dataDir }

func (h *host) Appearance() string {
	switch v := h.appearance.Get(); v {
	case "light", "dark":
		return v
	}
	return "system"
}

func (h *host) SetAppearance(mode string) {
	h.appearance.Set(mode)
	h.applyTheme(h.Appearance())
}

func (h *host) Backdrop() string { return h.backdrop.Get() }

func (h *host) SetBackdrop(mode string) {
	h.backdrop.Set(mode)
	h.applyBackdrop(mode)
}

// applyBackdrop shows the material for "frosted" and tells the palette when
// the window is clear, which the window itself cannot report.
func (h *host) applyBackdrop(mode string) {
	if h.setVibrancy != nil {
		h.setVibrancy(mode == "frosted")
	}
	native.SetClearWindow(mode == "clear")
}

// applyTheme shows mode without saving it.
func (h *host) applyTheme(mode string) {
	if h.setTheme == nil {
		return
	}
	switch mode {
	case "light":
		h.setTheme(mygo.ThemeLight)
	case "dark":
		h.setTheme(mygo.ThemeDark)
	default:
		h.setTheme(mygo.ThemeSystem)
	}
}

func (h *host) Plugins() []kernel.Status { return h.k.Plugins() }
func (h *host) Enable(id string) error   { return h.k.Enable(id) }
func (h *host) Disable(id string) error  { return h.k.Disable(id) }
