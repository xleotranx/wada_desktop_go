// Package shell declares the window's extension points and the host service.
// The shell reads these points every frame and renders the entries through
// kernel.Invoke, so a panicking view fails only its plugin.
package shell

import (
	"wada-desktop-go/internal/kernel"

	"github.com/egoist/mygo/ui"
)

// View draws part of the window. Render runs on the UI thread during a frame:
// it builds elements and performs no external I/O or kernel mutation.
type View struct {
	Render func(c *ui.Context)
	// Grow lets a sidebar section take the remaining height, as a list does.
	Grow bool
}

// Page fills the window's main area. The shell shows one page at a time.
type Page struct {
	Title  func() string // shown in the title bar and the window title; may be nil
	Render func(c *ui.Context)
}

var (
	// Sidebar sections, stacked top to bottom by Order.
	Sidebar = kernel.NewPoint[View]("shell.sidebar")
	// Pages for the main area. The page with the lowest Order is the default.
	Pages = kernel.NewPoint[Page]("shell.pages")
	// TitleBar items, laid out at the end of the title strip.
	TitleBar = kernel.NewPoint[View]("shell.title-bar")
	// Overlays are drawn above everything: dialogs, popovers.
	Overlays = kernel.NewPoint[View]("shell.overlays")

	HostKey = kernel.NewKey[Host]("shell.host")
)

// Host is what the shell does for plugins. Call it on the UI thread.
type Host interface {
	// ShowPage shows the Pages entry with this ID.
	ShowPage(id string)
	// Page is the ID of the page on show.
	Page() string
	// Toast shows a transient notice.
	Toast(t ui.Toast)
	// Copy puts text on the clipboard.
	Copy(text string) error
	// OpenURL opens a web or mail address with the desktop.
	OpenURL(url string)

	// Appearance is "system", "light" or "dark".
	Appearance() string
	SetAppearance(mode string)

	// Plugins reports every installed plugin; Enable and Disable turn one on
	// or off and remember the choice.
	Plugins() []kernel.Status
	Enable(id string) error
	Disable(id string) error
}
