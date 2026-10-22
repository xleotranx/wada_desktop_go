// Package settings declares the settings page's extension point. Any plugin
// may contribute a section; it renders only while the settings plugin is
// installed.
package settings

import (
	"github.com/xleotranx/wada-kit/kernel"

	"github.com/egoist/mygo/ui"
)

// Section is one titled group on the settings page, ordered by Order.
type Section struct {
	Title       string
	Description string
	Render      func(c *ui.Context)
}

var Sections = kernel.NewPoint[Section]("settings.sections")

// PageID is the settings page in shell.Pages; show it with shell.Host.ShowPage.
const PageID = "settings.page"
