// Package browser declares the in-app browser's tab, for plugins that open
// a page in it, such as a workspace's running service.
package browser

// PaneKind is the tabs.Panes kind that shows a web page; a tab's Ref is the
// address it opened, as typed or as a full URL.
const PaneKind = "browser.page"
