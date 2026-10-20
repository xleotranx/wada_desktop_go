package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestBrowseQuotesChosenFiles opens the attach dialog, chooses files with
// the system picker (stubbed here), and leaves their fenced contents in
// the draft with the dialog closed.
func TestBrowseQuotesChosenFiles(t *testing.T) {
	a := newTestApp(t)
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte("package sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.runtime.pickAttach = func(done func(paths []string)) { done([]string{path}) }
	tt := ui.NewTester(a.view, 1400, 920)
	a.openAttachDialog()
	tt.Frame()

	if err := tt.Click("Browse…"); err != nil {
		t.Fatalf("the browse action is missing: %v", err)
	}
	tt.Frame()
	if a.layout.attach != nil {
		t.Fatal("the attach dialog stayed open after choosing files")
	}
	abs, _ := filepath.Abs(path)
	if !strings.Contains(a.conversation.draft, "(file: "+abs+")") ||
		!strings.Contains(a.conversation.draft, "package sample") {
		t.Fatalf("the chosen file was not quoted: %q", a.conversation.draft)
	}
}

// TestQuoteFilesBatchesAndSkipsRepeat quotes a drop of several files:
// good files land once, binary and missing files are named in a toast
// instead of spoiling the rest, and quoting the same path again adds
// nothing.
func TestQuoteFilesBatchesAndSkipsRepeat(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.md")
	binary := filepath.Join(dir, "image.bin")
	if err := os.WriteFile(first, []byte("first body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("# second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.txt")

	a.quoteFiles([]string{first, second, binary, missing, first})
	if got := strings.Count(a.conversation.draft, "(file: "); got != 2 {
		t.Fatalf("quoted %d files, want the two readable ones: %q", got, a.conversation.draft)
	}
	var failure bool
	for _, toast := range a.layout.toasts {
		if toast.Title == "Some files were not quoted" {
			failure = true
		}
	}
	if !failure {
		t.Fatal("unreadable files were not reported")
	}

	a.quoteFiles([]string{first})
	if got := strings.Count(a.conversation.draft, "(file: "); got != 2 {
		t.Fatalf("quoting an attached file again added it: %q", a.conversation.draft)
	}
}

// TestQuoteFilesRefusesRemoteChats keeps this machine's paths out of a
// remote server's draft: the server cannot read them, so the files stay
// where they are and the toast says why.
func TestQuoteFilesRefusesRemoteChats(t *testing.T) {
	a := newTestApp(t)
	a.connections.servers = append(a.connections.servers,
		serverConn{id: "srv-remote", name: "Remote", url: "http://10.0.0.2:4096"})
	a.connections.activeServer = "srv-remote"
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("remote cannot read this"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.quoteFiles([]string{path})
	if a.conversation.draft != "" {
		t.Fatalf("a remote chat quoted a local path: %q", a.conversation.draft)
	}
	if len(a.layout.toasts) == 0 || a.layout.toasts[0].Title != "Files cannot be quoted into a remote chat" {
		t.Fatalf("the refusal is missing: %+v", a.layout.toasts)
	}
}
