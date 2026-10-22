// Package shell declares the window's extension points and the host service.
// The shell reads these points every frame and renders the entries through
// kernel.Invoke, so a panicking view fails only its plugin.
package shell

import (
	"github.com/xleotranx/wada-kit/kernel"

	"github.com/egoist/mygo/ui"
)

// View draws part of the window. Render runs on the UI thread during a frame:
// it builds elements and performs no external I/O or kernel mutation.
type View struct {
	Render func(c *ui.Context)
	// Grow lets a Strip view take the remaining width.
	Grow bool
}

// Page fills the window's main area. The shell shows one page at a time.
type Page struct {
	// Title names the page in the window title, and in the title strip when
	// no Strip view is contributed; may be nil.
	Title  func() string
	Render func(c *ui.Context)
}

var (
	// Pages for the main area. The page with the lowest Order is the default.
	Pages = kernel.NewPoint[Page]("shell.pages")
	// TitleBar items, laid out at the end of the title strip.
	TitleBar = kernel.NewPoint[View]("shell.title-bar")
	// Strip views take the title strip after the window controls, laid out in
	// a row before the TitleBar items; one with Grow takes the remaining width.
	// While one exists the strip shows no page title. The page sits in a
	// rounded panel below the strip either way.
	Strip = kernel.NewPoint[View]("shell.strip")
	// Overlays are drawn above everything: dialogs, popovers.
	Overlays = kernel.NewPoint[View]("shell.overlays")

	HostKey = kernel.NewKey[Host]("shell.host")

	// NoticeClicked carries the ID of a desktop notification the user clicked,
	// after the shell has brought the window to the front.
	NoticeClicked = kernel.NewTopic[string]("shell.notice-clicked")

	// PageChanged carries the ID that Host.ShowPage chose, each time it
	// chooses a different page than the last one.
	PageChanged = kernel.NewTopic[string]("shell.page-changed")
)

// SearchCommand is the ID of the kernel.Commands entry that opens the app's
// search. The tab strip's search button runs it when a plugin contributes it.
const SearchCommand = "palette.open"

// Clipboard is what a paste can attach: local file paths, or an image.
type Clipboard struct {
	Files []string
	PNG   []byte // an image as PNG; nil without one
}

// Notice is a desktop notification. A notice with the ID of one still shown
// replaces it.
type Notice struct {
	ID       string // names what it is about, e.g. "chat:<session>"
	Title    string
	Subtitle string
	Body     string
}

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
	// Clipboard reads the files and image on the clipboard, for a paste.
	Clipboard() Clipboard
	// OpenURL opens a web or mail address with the desktop.
	OpenURL(url string)
	// Notify shows a desktop notification, when the platform can.
	Notify(n Notice)
	// Foreground reports that the window is on screen and focused.
	Foreground() bool
	// DataDir is the folder where the app keeps its data; a plugin keeps
	// its files there, named after its ID. Empty when nothing may be written,
	// as in tests.
	DataDir() string

	// Appearance is "system", "light" or "dark".
	Appearance() string
	SetAppearance(mode string)
	// Backdrop is what shows behind the strip and the panel: "clear" (the
	// desktop as it is, under a tint), "frosted" (the desktop blurred, where
	// the platform can) or "solid".
	Backdrop() string
	SetBackdrop(mode string)

	// Plugins reports every installed plugin; Enable and Disable turn one on
	// or off and remember the choice.
	Plugins() []kernel.Status
	Enable(id string) error
	Disable(id string) error
}
