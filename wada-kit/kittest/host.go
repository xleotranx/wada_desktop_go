// Package kittest has fakes of what the shell provides, for plugin tests.
package kittest

import (
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"

	"github.com/egoist/mygo/ui"
)

// Host provides shell.HostKey and records what plugins ask of the window.
type Host struct {
	ctx        *kernel.Context
	PageID     string
	Toasts     []string
	Copied     string
	Opened     []string
	Behind     string // what Backdrop reports
	Mode       string
	PluginList []kernel.Status
	Toggled    []string
	Notices    []shell.Notice
	Clip       shell.Clipboard // what Clipboard returns
	Background bool            // the window is not in front
	Data       string          // what DataDir returns
}

func (*Host) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "fake-host", Provides: []kernel.Named{shell.HostKey}}
}

func (h *Host) Setup(c *kernel.Context) error {
	h.ctx = c
	return kernel.Provide[shell.Host](c, shell.HostKey, h)
}

// ShowPage records the page and emits shell.PageChanged when it changed, as
// the shell does.
func (h *Host) ShowPage(id string) {
	if id == h.PageID {
		return
	}
	h.PageID = id
	if h.ctx != nil {
		kernel.Emit(h.ctx, shell.PageChanged, id)
	}
}

func (h *Host) Page() string               { return h.PageID }
func (h *Host) Toast(t ui.Toast)           { h.Toasts = append(h.Toasts, t.Title) }
func (h *Host) Copy(text string) error     { h.Copied = text; return nil }
func (h *Host) Clipboard() shell.Clipboard { return h.Clip }
func (h *Host) OpenURL(url string)         { h.Opened = append(h.Opened, url) }
func (h *Host) Appearance() string         { return h.Mode }
func (h *Host) SetAppearance(mode string)  { h.Mode = mode }
func (h *Host) Backdrop() string           { return h.Behind }
func (h *Host) SetBackdrop(mode string)    { h.Behind = mode }
func (h *Host) Plugins() []kernel.Status   { return h.PluginList }
func (h *Host) Enable(id string) error     { h.Toggled = append(h.Toggled, "+"+id); return nil }
func (h *Host) Disable(id string) error    { h.Toggled = append(h.Toggled, "-"+id); return nil }
func (h *Host) Notify(n shell.Notice)      { h.Notices = append(h.Notices, n) }
func (h *Host) Foreground() bool           { return !h.Background }
func (h *Host) DataDir() string            { return h.Data }
