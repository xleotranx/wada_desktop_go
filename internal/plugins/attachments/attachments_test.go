package attachments

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Host, string) {
	t.Helper()
	dir := t.TempDir()
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	os.WriteFile(filepath.Join(dir, "shot.png"), img.Bytes(), 0o600)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o600)
	os.Mkdir(filepath.Join(dir, "folder"), 0o700)
	host := &plugintest.Host{}
	p := &Plugin{}
	h := kerneltest.New(t, host, p)
	return h, p, host, dir
}

func drop(h *kerneltest.Harness, paths ...string) {
	for _, e := range kernel.Items(h, chat.FileDrop) {
		kernel.Invoke(h, e, func(fn func([]string)) { fn(paths) })
	}
}

func names(p *Plugin) []string {
	var out []string
	for _, f := range p.pending {
		out = append(out, f.file.Name)
	}
	return out
}

func TestDroppedFilesGoWithTheNextMessage(t *testing.T) {
	h, p, host, dir := start(t)
	drop(h, filepath.Join(dir, "shot.png"), filepath.Join(dir, "notes.txt"), filepath.Join(dir, "folder"),
		filepath.Join(dir, "missing"), filepath.Join(dir, "notes.txt"))
	if _, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: "early"}); err == nil {
		t.Fatal("sent while files were being read")
	}
	h.Flush()
	if !slices.Equal(names(p), []string{"shot.png", "notes.txt"}) || p.pending[0].thumb == nil {
		t.Fatalf("pending %q", names(p))
	}
	if !slices.Equal(host.Toasts, []string{"Some files were not attached"}) {
		t.Fatalf("toasts %q", host.Toasts)
	}
	drop(h, filepath.Join(dir, "notes.txt"))
	h.Flush()
	if len(p.pending) != 2 {
		t.Fatalf("a file was added twice: %q", names(p))
	}
	d, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: "look"})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 2 || d.Files[0].MIME != "image/png" || d.Files[1].MIME != "text/plain" ||
		string(d.Files[1].Data) != "hello" || len(p.pending) != 0 {
		t.Fatalf("draft files %+v, pending %q", d.Files, names(p))
	}
}

func TestPickAndRemove(t *testing.T) {
	h, p, _, dir := start(t)
	p.choose = func() ([]string, error) { return []string{filepath.Join(dir, "notes.txt")}, nil }
	for _, e := range kernel.Items(h, kernel.Commands) {
		if e.ID == "attachments.attach" {
			kernel.Invoke(h, e, func(cmd kernel.Command) { cmd.Run() })
		}
	}
	h.Flush()
	if !slices.Equal(names(p), []string{"notes.txt"}) {
		t.Fatalf("pending %q", names(p))
	}
	tt := ui.NewTester(p.list, 600, 100)
	tt.Frame()
	if !tt.HasText("notes.txt") || !tt.HasText("5 B") {
		t.Fatalf("chips %q", tt.Texts())
	}
	if err := tt.Click("Remove notes.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	if len(p.pending) != 0 {
		t.Fatalf("pending %q after remove", names(p))
	}
}

func TestLimits(t *testing.T) {
	h, p, host, dir := start(t)
	var paths []string
	for i := range maxFiles + 2 {
		path := filepath.Join(dir, string(rune('a'+i))+".txt")
		os.WriteFile(path, []byte("x"), 0o600)
		paths = append(paths, path)
	}
	big := filepath.Join(dir, "big.bin")
	os.WriteFile(big, make([]byte, maxFileBytes+1), 0o600)
	drop(h, append(paths, big)...)
	h.Flush()
	if len(p.pending) != maxFiles || len(host.Toasts) != 1 {
		t.Fatalf("%d pending, toasts %q", len(p.pending), host.Toasts)
	}
}

func TestMIME(t *testing.T) {
	for _, c := range []struct {
		path string
		data []byte
		want string
	}{
		{"a.ts", []byte("const x = 1"), "text/plain"},
		{"a.html", []byte("<p>Hi</p>"), "text/html"},
		{"Makefile", []byte("all:\n"), "text/plain"},
		{"a.png", []byte{0x89, 'P', 'N', 'G', 0}, "image/png"},
		{"blob", []byte{0, 1, 2}, "application/octet-stream"},
	} {
		if got := mimeOf(c.path, c.data); got != c.want {
			t.Errorf("mimeOf(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func paste(h *kerneltest.Harness, text string) bool {
	took := false
	for _, e := range kernel.Items(h, chat.Paste) {
		kernel.Invoke(h, e, func(fn func(string) bool) { took = took || fn(text) })
	}
	return took
}

func TestPasteTakesFilesAndImages(t *testing.T) {
	h, p, host, dir := start(t)
	if paste(h, "plain words") {
		t.Fatal("took a text paste")
	}
	host.Clip = shell.Clipboard{Files: []string{filepath.Join(dir, "notes.txt")}}
	if !paste(h, "notes.txt") {
		t.Fatal("did not take pasted files")
	}
	shot, _ := os.ReadFile(filepath.Join(dir, "shot.png"))
	host.Clip = shell.Clipboard{PNG: shot}
	if paste(h, "a caption") {
		t.Fatal("took an image pasted with text")
	}
	for range 2 {
		if !paste(h, "") {
			t.Fatal("did not take a pasted image")
		}
	}
	h.Flush()
	if !slices.Equal(names(p), []string{"notes.txt", "Pasted image.png", "Pasted image 2.png"}) || p.pending[1].thumb == nil {
		t.Fatalf("pending %q", names(p))
	}
}

// types is a backend that takes only text.
type types struct{}

func (types) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "types", Provides: []kernel.Named{agent.FileTypesKey}}
}
func (types) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.FileTypes](c, agent.FileTypesKey, types{})
}
func (types) Accepts(mime string) bool { return mime == "text/plain" }
func (types) Summary() string          { return "text files" }

func TestRefusesWhatTheBackendDoesNotTake(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.zip"), []byte{0, 1}, 0o600)
	host := &plugintest.Host{}
	p := &Plugin{}
	h := kerneltest.New(t, host, types{}, p)
	drop(h, filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.zip"))
	h.Flush()
	if !slices.Equal(names(p), []string{"a.txt"}) || len(host.Toasts) != 1 {
		t.Fatalf("pending %q toasts %q", names(p), host.Toasts)
	}
}
