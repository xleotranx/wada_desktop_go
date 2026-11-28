// Package browser declares the in-app browser's tab, for plugins that open
// a page in it, such as a workspace's running service.
package browser

import (
	"context"
	"github.com/xleotranx/wada-kit/kernel"
)

// PaneKind is the tabs.Panes kind that shows a web page; a tab's Ref is the
// address it opened, as typed or as a full URL.
const PaneKind = "browser.page"

var PreviewKey = kernel.NewKey[Preview]("browser.preview")

type Request struct {
	ID, Workspace, Session, Service, Origin, Action, Value string
}

type Reply struct {
	ID, Text, Error string
}

// Preview communicates with the local daemon's pending browser requests.
type Preview interface {
	PendingPreview(context.Context, string) (*Request, error)
	ClaimPreview(context.Context, string, bool) (*Request, error)
	ReplyPreview(context.Context, Reply) error
}
