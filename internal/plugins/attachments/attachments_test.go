package attachments

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	attachment "wada-desktop-go/internal/contract/attachments"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

func start(t *testing.T) (*kerneltest.Harness, *Plugin, *kittest.Host, string) {
	t.Helper()
	dir := t.TempDir()
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	os.WriteFile(filepath.Join(dir, "shot.png"), img.Bytes(), 0o600)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o600)
	os.Mkdir(filepath.Join(dir, "folder"), 0o700)
	host := &kittest.Host{}
	var p *Plugin
	h := kerneltest.New(t, host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
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
	if _, err := send(h, chat.Draft{Text: "early"}); err == nil {
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
	d, err := send(h, chat.Draft{Text: "look"})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 2 || d.Files[0].MIME != "image/png" || d.Files[1].MIME != "text/plain" ||
		string(d.Files[1].Data) != "hello" || len(p.pending) != 0 {
		t.Fatalf("draft files %+v, pending %q", d.Files, names(p))
	}
}

func TestFilesWaitWhenALaterTransformRejectsTheMessage(t *testing.T) {
	h, p, _, dir := start(t)
	drop(h, filepath.Join(dir, "notes.txt"))
	h.Flush()
	reject := true
	kernel.Transform(h, chat.Outgoing, 2000, func(d chat.Draft) (chat.Draft, func(), error) {
		if reject {
			return d, nil, errors.New("not yet")
		}
		return d, nil, nil
	})
	if _, err := send(h, chat.Draft{Text: "look"}); err == nil || !slices.Equal(names(p), []string{"notes.txt"}) {
		t.Fatalf("rejected send: %v, pending %q", err, names(p))
	}
	reject = false
	if d, err := send(h, chat.Draft{Text: "look"}); err != nil || len(d.Files) != 1 || len(p.pending) != 0 {
		t.Fatalf("accepted send: %+v, %v, pending %q", d.Files, err, names(p))
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

func (f types) New() kernel.Plugin { return f }
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
	host := &kittest.Host{}
	var p *Plugin
	h := kerneltest.New(t, host.New, types{}.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	drop(h, filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.zip"))
	h.Flush()
	if !slices.Equal(names(p), []string{"a.txt"}) || len(host.Toasts) != 1 {
		t.Fatalf("pending %q toasts %q", names(p), host.Toasts)
	}
}

func TestImageReplacementChecksIdentityRevisionAndMessageLimits(t *testing.T) {
	h, p, _, dir := start(t)
	drop(h, filepath.Join(dir, "shot.png"))
	h.Flush()
	service, ok := kernel.Use(h, attachment.ServiceKey)
	if !ok {
		t.Fatal("image service missing")
	}
	source, ok := service.Snapshot(p.pending[0].id)
	if !ok {
		t.Fatal("pending image missing")
	}
	// Snapshots cannot mutate the pending message's data.
	source.File.Data[0] = 0
	if p.pending[0].file.Data[0] == 0 {
		t.Fatal("snapshot exposed mutable attachment bytes")
	}
	png := agent.File{Name: "shot.png", MIME: "image/png", Data: bytes.Clone(p.pending[0].file.Data)}
	preview := p.pending[0].thumb
	if err := service.Replace(source, png, preview, "1. Box: this", nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Replace(source, png, preview, "", nil); err == nil {
		t.Fatal("stale revision replaced an image")
	}
	current, _ := service.Snapshot(source.ID)
	if current.Notes != "1. Box: this" {
		t.Fatalf("notes %q", current.Notes)
	}
	big := png
	big.Data = make([]byte, maxFileBytes+1)
	if err := service.Replace(current, big, preview, "", nil); err == nil {
		t.Fatal("oversized image accepted")
	}
	if err := service.Replace(current, agent.File{Name: "a.txt", MIME: "text/plain", Data: []byte("x")}, preview, "", nil); err == nil {
		t.Fatal("a text file replaced an image")
	}
	p.remove(p.pending[0].path)
	drop(h, filepath.Join(dir, "shot.png"))
	h.Flush()
	if err := service.Replace(current, png, preview, "", nil); err == nil {
		t.Fatal("removing and reattaching a file reused its image identity")
	}
}

func TestDetailsGoOutRightAfterTheirImageAndCountAsFiles(t *testing.T) {
	h, p, _, dir := start(t)
	drop(h, filepath.Join(dir, "shot.png"), filepath.Join(dir, "notes.txt"))
	h.Flush()
	service, _ := kernel.Use(h, attachment.ServiceKey)
	source, _ := service.Snapshot(p.pending[0].id)
	part := agent.File{Name: "shot mark 1.png", MIME: "image/png", Data: []byte{9}}
	details := []attachment.Detail{{Name: "mark 1 at full resolution", File: part}}
	if err := service.Replace(source, source.File, p.pending[0].thumb, "1. Box", details); err != nil {
		t.Fatal(err)
	}
	current, _ := service.Snapshot(source.ID)
	many := make([]attachment.Detail, maxFiles-1)
	for i := range many {
		many[i] = details[0]
	}
	if err := service.Replace(current, current.File, p.pending[0].thumb, "", many); err == nil {
		t.Fatal("details past the message's file limit were taken")
	}
	d, err := send(h, chat.Draft{Text: "Look"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Look\n\n[Image 1: shot.png]\n1. Box\n\n[Image 2: mark 1 at full resolution, from image 1]"
	if d.Text != want || len(d.Files) != 3 || d.Files[1].Name != "shot mark 1.png" || d.Files[2].Name != "notes.txt" {
		t.Fatalf("text %q files %+v", d.Text, names(p))
	}
}

func TestAPluginCanAttachAnImageWithinTheLimits(t *testing.T) {
	h, p, _, dir := start(t)
	service, _ := kernel.Use(h, attachment.ServiceKey)
	data, _ := os.ReadFile(filepath.Join(dir, "shot.png"))
	thumb, _ := ui.DecodeBitmap(data)
	id, err := service.Attach(agent.File{Name: "Screenshot.png", MIME: "image/png", Data: data}, thumb)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := service.Snapshot(id); !ok || got.File.Name != "Screenshot.png" || p.pending[0].thumb != thumb {
		t.Fatal("the attached image is not pending")
	}
	if _, err := service.Attach(agent.File{Name: "a.txt", MIME: "text/plain", Data: []byte("x")}, thumb); err == nil {
		t.Fatal("a text file was attached as an image")
	}
	if _, err := service.Attach(agent.File{Name: "bad.png", MIME: "image/png", Data: []byte("not a png")}, thumb); err == nil {
		t.Fatal("an undecodable image was attached")
	}
	for range maxFiles - 1 {
		if _, err := service.Attach(agent.File{Name: "Screenshot.png", MIME: "image/png", Data: data}, thumb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Attach(agent.File{Name: "Screenshot.png", MIME: "image/png", Data: data}, thumb); err == nil {
		t.Fatal("an eleventh file was attached")
	}
	if d, _ := send(h, chat.Draft{Text: "x"}); len(d.Files) != maxFiles {
		t.Fatalf("sent %d files", len(d.Files))
	}
}

func TestImageNotesGoOutNamingTheirImage(t *testing.T) {
	h, p, _, dir := start(t)
	drop(h, filepath.Join(dir, "notes.txt"), filepath.Join(dir, "shot.png"))
	h.Flush()
	service, _ := kernel.Use(h, attachment.ServiceKey)
	source, _ := service.Snapshot(p.pending[1].id)
	if err := service.Replace(source, source.File, p.pending[1].thumb, "1. Box: move this", nil); err != nil {
		t.Fatal(err)
	}
	earlier := agent.File{Name: "other.png", MIME: "image/png", Data: []byte{1}}
	d, err := send(h, chat.Draft{Text: "Fix it", Files: []agent.File{earlier}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Text != "Fix it\n\n[Image 2: shot.png]\n1. Box: move this" || len(d.Files) != 3 {
		t.Fatalf("text %q files %d", d.Text, len(d.Files))
	}
	drop(h, filepath.Join(dir, "shot.png"))
	h.Flush()
	source, _ = service.Snapshot(p.pending[0].id)
	service.Replace(source, source.File, p.pending[0].thumb, "note", nil)
	p.remove(p.pending[0].path)
	if d, _ := send(h, chat.Draft{Text: "Hi"}); d.Text != "Hi" {
		t.Fatalf("a removed image's notes went out: %q", d.Text)
	}
}

// send runs the outgoing pipeline as the chat does, committing what it accepts.
func send(h *kerneltest.Harness, d chat.Draft) (chat.Draft, error) {
	out, commit, err := kernel.Run(h, chat.Outgoing, d)
	if err == nil {
		commit()
	}
	return out, err
}
