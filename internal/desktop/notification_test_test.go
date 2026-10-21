package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestNotificationTestBanner sends a banner through the real notify hook
// from Settings, and says so when the build has none.
func TestNotificationTestBanner(t *testing.T) {
	a := newTestApp(t)
	a.layout.settingsOpen = true
	a.layout.settingsTab = tabGeneral
	var notices []desktopNotice
	a.runtime.notify = func(n desktopNotice) { notices = append(notices, n) }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Send a test banner"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(notices) != 1 || notices[0].title != "Wada can notify you" {
		t.Fatalf("the test sent %+v", notices)
	}
	if !tt.HasText("Test banner sent") {
		t.Fatalf("the test said nothing: %v", tt.Texts())
	}

	// Without a notification hook the guard still answers.
	b := newTestApp(t)
	b.sendTestBanner()
	if len(b.layout.toasts) != 1 || b.layout.toasts[0].Title != "Banners are not available in this build" {
		t.Fatalf("the guard said %+v", b.layout.toasts)
	}
}
