package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSettingsShowsItsStorageFolder names where the app keeps its own data
// and hands that folder to the clipboard and the desktop.
func TestSettingsShowsItsStorageFolder(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WADA_DATA_DIR", dir)
	a := newTestApp(t)
	a.layout.settingsOpen = true
	a.layout.settingsTab = tabGeneral
	var copied string
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Storage") || !tt.HasText(dir) {
		t.Fatalf("the storage folder is not named: %v", tt.Texts())
	}
	if err := tt.Click("Copy path"); err != nil {
		t.Fatal(err)
	}
	if copied != dir {
		t.Fatalf("copied %q", copied)
	}
	if err := tt.Click("Show folder"); err != nil {
		t.Fatal(err)
	}
	urls := tt.OpenedURLs()
	if len(urls) != 1 || !strings.Contains(urls[0], dir) {
		t.Fatalf("opened %v", urls)
	}
}
