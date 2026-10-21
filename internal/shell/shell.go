// Package shell is the native window host. It owns the kernel, the window's
// frame and layout, menus and shortcuts, and draws whatever plugins
// contribute to the points in contract/shell. It knows no feature.
package shell

import (
	"context"
	"log"
	"slices"

	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/native"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/oneo-me/onevra/source/theme"
)

// Options configures a Shell. Post and Store are passed to the kernel.
type Options struct {
	Post   func(func())
	Store  kernel.Store
	Strict bool
	// SetTitle and SetMenu reach the native window and menu bar; nil in tests.
	SetTitle func(string)
	SetMenu  func(*mygo.Menu)
	SetTheme func(mygo.ThemeSource)
	// Notify and Foreground reach the desktop; nil in tests, where nothing
	// is shown and the window counts as in front.
	Notify     func(shell.Notice)
	Foreground func() bool
	// Clipboard reads the clipboard for a paste; nil in tests, where it is empty.
	Clipboard func() shell.Clipboard
}

// Shell is a kernel plus the window that shows it.
type Shell struct {
	Kernel *kernel.Kernel
	host   *host
	opts   Options
	title  string
}

// New installs the shell's own plugin and then plugins, in that order.
func New(opts Options, plugins ...kernel.Plugin) (*Shell, error) {
	s := &Shell{opts: opts, host: &host{setTheme: opts.SetTheme, notify: opts.Notify, foreground: opts.Foreground,
		clipboard: opts.Clipboard}}
	s.Kernel = kernel.New(kernel.Options{Post: opts.Post, Store: opts.Store, Strict: opts.Strict})
	s.host.k = s.Kernel
	if err := s.Kernel.Install(append([]kernel.Plugin{s.host}, plugins...)...); err != nil {
		return nil, err
	}
	return s, nil
}

// Start sets the plugins up and builds the menu bar.
func (s *Shell) Start() error {
	kernel.On(s.Kernel, kernel.PluginsChanged, s.pluginsChanged)
	err := s.Kernel.Start()
	s.buildMenu()
	return err
}

// Close disposes every plugin and waits for their workers.
func (s *Shell) Close(ctx context.Context) error { return s.Kernel.Close(ctx) }

// NoticeClicked tells plugins the user clicked the notification with this ID.
// Call it on the UI thread.
func (s *Shell) NoticeClicked(id string) { kernel.Emit(s.Kernel, shell.NoticeClicked, id) }

// pluginsChanged rebuilds the menu and reports plugins that just failed.
func (s *Shell) pluginsChanged(all []kernel.Status) {
	s.buildMenu()
	for _, st := range all {
		failed := st.State == kernel.Failed
		if failed && !s.host.failed[st.Manifest.ID] {
			log.Printf("plugin %s failed: %v", st.Manifest.ID, st.Err)
			s.host.Toast(ui.Toast{Title: st.Manifest.Title + " stopped working", Description: st.Err.Error(),
				Type: components.ToastError})
		}
		s.host.failed[st.Manifest.ID] = failed
	}
}

const (
	trafficLightButton = 14
	trafficLightY      = 18
	headerStrip        = trafficLightButton + 2*trafficLightY

	sidebarDefaultWidth float32 = 300
	sidebarMinWidth     float32 = 220
	sidebarMaxWidth     float32 = 520
	pageMinWidth        float32 = 380
)

// View draws one frame.
func (s *Shell) View(c *ui.Context) {
	theme.Apply(c)
	h := s.host
	h.services = c.Services()
	for _, t := range h.toasts {
		c.AddToast(t)
	}
	h.toasts = nil
	p := native.PaletteFor(c)
	page, hasPage := s.currentPage()
	s.syncTitle(page, hasPage)
	s.shortcuts(c)
	bar := c.TitleBar()
	headerH := max(bar.Height, float32(headerStrip))
	sidebar := kernel.Items(s.Kernel, shell.Sidebar)
	pageHidesSidebar := hasPage && page.Value.HideSidebar
	showSidebar := len(sidebar) > 0 && !h.sidebarHidden.Get() && !pageHidesSidebar

	titleText := func() {
		if hasPage && page.Value.Title != nil {
			var title string
			kernel.Invoke(s.Kernel, page, func(pg shell.Page) { title = pg.Title() })
			ui.Text(c, title).FontSize(14).FontWeight(500).TextColor(p.Text).
				Shrink(1).MinWidth(0).SingleLine().Ellipsis("…")
		}
	}
	toggle := func() {
		label := "Hide sidebar"
		if !showSidebar {
			label = "Show sidebar"
		}
		iconButton(c, p, label, lucide.PanelLeft(), h.toggleSidebar)
	}
	body := func() {
		ui.Column(c).Fill().MinWidth(0).MinHeight(0).Children(func() {
			if hasPage {
				kernel.Invoke(s.Kernel, page, func(pg shell.Page) { pg.Render(c) })
			}
		})
	}
	canvas := func() {
		ui.Column(c).Fill().MinWidth(0).MinHeight(0).
			Radius(16).Background(p.CanvasBg).Border(1, p.Hairline.Alpha(.8)).
			Shadow(0, 2, 8, 0, p.Text.Alpha(.08)).Clip().Children(func() {
			body()
		})
	}
	ui.Column(c).Fill().Background(p.RailBg).Children(func() {
		ui.Row(c).FillWidth().Height(headerH).Shrink(0).DragWindow().AlignItems(ui.Center).Children(func() {
			if showSidebar {
				ui.Row(c).Width(h.sidebarWidth()+1).Shrink(0).FillHeight().AlignItems(ui.Center).
					Padding(0, 12, 0, bar.Left+12).Children(toggle)
			}
			left := float32(12)
			if showSidebar {
				left = 8
			}
			ui.Row(c).Fill().MinWidth(0).AlignItems(ui.Center).Padding(0, 12, 0, left).Gap(8).Children(func() {
				// Balance the controls around the center of the conversation pane.
				reserve := float32(120)
				if !showSidebar {
					reserve = max(reserve, bar.Left+44)
				}
				ui.Row(c).Width(0).Grow(1).MinWidth(reserve).AlignItems(ui.Center).Children(func() {
					if !showSidebar && len(sidebar) > 0 && !pageHidesSidebar {
						ui.Row(c).Padding(0, 0, 0, bar.Left).Children(toggle)
					}
				})
				ui.Row(c).MinWidth(0).MaxWidth(480).Shrink(1).AlignItems(ui.Center).Children(titleText)
				ui.Row(c).Width(0).Grow(1).MinWidth(reserve).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					s.render(c, kernel.Items(s.Kernel, shell.TitleBar))
				})
			})
		})
		if !showSidebar {
			ui.Column(c).Fill().MinWidth(0).MinHeight(0).Padding(4, 12, 12, 12).Children(canvas)
			return
		}
		panel := func() {
			ui.Column(c).Fill().MinWidth(0).MinHeight(0).Padding(4, 12, 12, 8).Children(canvas)
		}
		width := h.sidebarWidth()
		// The resize handle blends into the window; panes keep their normal theme.
		frameTheme := c.Theme()
		splitTheme := *frameTheme
		splitTheme.Border = p.RailBg
		c.SetTheme(&splitTheme)
		split := components.Split(c, &width, func() {
			c.SetTheme(frameTheme)
			ui.Column(c).Width(width).FillHeight().MinHeight(0).Padding(4, 10, 12, 10).Gap(2).Children(func() {
				s.render(c, sidebar)
			})
			c.SetTheme(&splitTheme)
		}, func() {
			c.SetTheme(frameTheme)
			panel()
			c.SetTheme(&splitTheme)
		}, components.SplitOptions{FirstMin: sidebarMinWidth, SecondMin: pageMinWidth, Default: sidebarDefaultWidth})
		c.SetTheme(frameTheme)
		split.Label("Resize sidebar").Fill()
		if split.Changed() {
			h.sidebarW.Set(min(max(width, sidebarMinWidth), sidebarMaxWidth))
		}
	})
	s.render(c, kernel.Items(s.Kernel, shell.Overlays))
	components.Toasts(c, components.ToastOptions{Placement: components.ToastTopEnd, Inset: headerH})
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
	title := "Wada"
	if ok && page.Value.Title != nil {
		kernel.Invoke(s.Kernel, page, func(pg shell.Page) {
			if t := pg.Title(); t != "" {
				title = t + " — Wada"
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

// shortcuts registers window keys for commands that aren't in the menu bar;
// the menu bar owns its items' keys.
func (s *Shell) shortcuts(c *ui.Context) {
	for _, e := range kernel.Items(s.Kernel, kernel.Commands) {
		keys := e.Value.Keys
		if e.Value.Menu != "" && len(keys) > 0 {
			keys = keys[1:]
		}
		for _, k := range keys {
			a, err := parseAccelerator(k)
			if err != nil {
				continue
			}
			c.OnShortcut(a.mods, a.key, func() { s.run(e) })
		}
	}
}

func (s *Shell) run(e kernel.Entry[kernel.Command]) {
	kernel.Invoke(s.Kernel, e, func(cmd kernel.Command) {
		if cmd.Run != nil && (cmd.Enabled == nil || cmd.Enabled()) {
			cmd.Run()
		}
	})
}

// menuOrder places the app's own menus between the standard ones.
var menuOrder = []string{"File", "Chat", "View", "Agent", "Help"}

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
	for _, name := range menuOrder {
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

func iconButton(c *ui.Context, p native.Palette, label string, icon *ui.SVG, on func()) {
	b := ui.ButtonBase(c).Label(label).Size(30, 30).Radius(8).Center().Cursor(ui.CursorPointer).Tooltip(label)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.OnClick(on)
	b.Children(func() { ui.Icon(c, icon).FontSize(17).TextColor(p.Text) })
}

// host is the shell's own plugin: it provides shell.Host and the window's
// commands.
type host struct {
	k             *kernel.Kernel
	setTheme      func(mygo.ThemeSource)
	notify        func(shell.Notice)
	clipboard     func() shell.Clipboard
	foreground    func() bool
	appearance    *kernel.Setting[string]
	page          string
	toasts        []ui.Toast
	services      ui.Services
	failed        map[string]bool
	sidebarHidden *kernel.Setting[bool]
	sidebarW      *kernel.Setting[float32]
}

var _ shell.Host = (*host)(nil)

func (*host) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "shell", Title: "Window", Locked: true, Provides: []kernel.Named{shell.HostKey, kernel.InspectorKey}}
}

func (h *host) Setup(c *kernel.Context) error {
	h.failed = map[string]bool{}
	h.sidebarHidden = kernel.NewSetting(c, "sidebar-hidden", false)
	h.sidebarW = kernel.NewSetting(c, "sidebar-width", sidebarDefaultWidth)
	h.appearance = kernel.NewSetting(c, "appearance", "system")
	h.applyTheme(h.Appearance())
	if err := kernel.Provide[shell.Host](c, shell.HostKey, h); err != nil {
		return err
	}
	if err := kernel.Provide[kernel.Inspector](c, kernel.InspectorKey, h.k); err != nil {
		return err
	}
	return kernel.Contribute(c, kernel.Commands, "shell.toggle-sidebar", 0, kernel.Command{
		Title: "Toggle Sidebar", Menu: "View", Keys: []string{"cmd+b"}, Run: h.toggleSidebar})
}

func (h *host) sidebarWidth() float32 {
	return min(max(h.sidebarW.Get(), sidebarMinWidth), sidebarMaxWidth)
}

func (h *host) toggleSidebar() { h.sidebarHidden.Set(!h.sidebarHidden.Get()) }

func (h *host) ShowPage(id string) { h.page = id }
func (h *host) Page() string       { return h.page }

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
