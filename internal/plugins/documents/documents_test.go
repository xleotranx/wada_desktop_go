package documents

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"wada-desktop-go/internal/plugintest"

	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

// start installs the plugin over a page that answers as anydoc does, without
// a window or the WebAssembly build.
func start(t *testing.T, answer func(code string) (any, error)) *Plugin {
	t.Helper()
	host := &kittest.Host{Data: t.TempDir()}
	host.Web = func(int) *kittest.WebView {
		return &kittest.WebView{EvalFunc: func(_ context.Context, code string) (any, error) {
			return answer(code)
		}}
	}
	var p *Plugin
	kerneltest.New(t, host.New, (&plugintest.Chat{}).New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	return p
}

// boot answers what the engine asks before it is ready.
func boot(code string) (any, bool) {
	switch {
	case strings.Contains(code, "__anydocReady"):
		return true, true
	case strings.Contains(code, ".chunk("):
		return 1, true
	}
	return nil, false
}

// converted answers a successful conversion.
func converted(format, markdown string) func(string) (any, error) {
	return func(code string) (any, error) {
		if v, ok := boot(code); ok {
			return v, nil
		}
		return map[string]any{"ok": true, "markdown": markdown, "format": format}, nil
	}
}

// write puts content in path.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIngestReadsDocumentsInOrder(t *testing.T) {
	p := start(t, converted("docx", "# A\n\nbody"))
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.docx"), "a")
	write(t, filepath.Join(dir, "b.PDF"), "b")
	write(t, filepath.Join(dir, "notes.txt"), "not a document")
	write(t, filepath.Join(dir, ".hidden.docx"), "hidden")
	if err := os.Mkdir(filepath.Join(dir, "folder.docx"), 0o700); err != nil {
		t.Fatal(err)
	}

	res, err := p.engine.Ingest(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, src := range res.Sources {
		names = append(names, filepath.Base(src.Path))
	}
	if !slices.Equal(names, []string{"a.docx", "b.PDF"}) {
		t.Fatalf("sources %q", names)
	}
	if res.Read != 2 || res.Chars == 0 || !res.Sources[0].Converted() {
		t.Fatalf("result %+v", res)
	}
}

func TestScannedPagesAreSkippedWithTheirNumbers(t *testing.T) {
	p := start(t, func(code string) (any, error) {
		if v, ok := boot(code); ok {
			return v, nil
		}
		return map[string]any{"ok": false, "code": "needsOcr",
			"message": "all 2 pages need OCR", "pages": []any{float64(1), float64(2)}, "format": "pdf"}, nil
	})
	path := filepath.Join(t.TempDir(), "scan.pdf")
	write(t, path, "scanned")

	src, err := p.engine.Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if src.Skip != "Scanned pages need OCR" || !slices.Equal(src.Pages, []int{1, 2}) {
		t.Fatalf("source %+v", src)
	}
	if src.Converted() {
		t.Fatal("a skipped source reports conversion")
	}
}

func TestConversionIsCachedByContent(t *testing.T) {
	conversions := 0
	p := start(t, func(code string) (any, error) {
		if v, ok := boot(code); ok {
			return v, nil
		}
		conversions++
		return map[string]any{"ok": true, "markdown": "# Cached", "format": "docx"}, nil
	})
	path := filepath.Join(t.TempDir(), "a.docx")
	write(t, path, "first bytes")
	ctx := context.Background()
	if _, err := p.engine.Read(ctx, path); err != nil {
		t.Fatal(err)
	}
	if conversions != 1 {
		t.Fatalf("conversions %d after the first read", conversions)
	}
	src, err := p.engine.Read(ctx, path)
	if err != nil || src.Markdown != "# Cached" || src.Format != "docx" {
		t.Fatalf("cached source %+v, %v", src, err)
	}
	if conversions != 1 {
		t.Fatalf("conversions %d after the second read", conversions)
	}
	write(t, path, "other bytes")
	if _, err := p.engine.Read(ctx, path); err != nil {
		t.Fatal(err)
	}
	if conversions != 2 {
		t.Fatalf("conversions %d after the file changed", conversions)
	}
}

func TestUnknownErrorsBecomeSkips(t *testing.T) {
	p := start(t, func(code string) (any, error) {
		if v, ok := boot(code); ok {
			return v, nil
		}
		return map[string]any{"ok": false, "code": "encrypted", "message": "protected"}, nil
	})
	path := filepath.Join(t.TempDir(), "locked.xlsx")
	write(t, path, "locked")
	src, err := p.engine.Read(context.Background(), path)
	if err != nil || src.Skip != "Password-protected" {
		t.Fatalf("source %+v, %v", src, err)
	}
}

func TestPageCarriesAnydoc(t *testing.T) {
	page := string(pageHTML())
	for _, want := range []string{"initSync({ module: bytes })", "toMarkdownBytes(u)", "__anydocReady = true"} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page is missing %q", want)
		}
	}
	if len(page) <= len(anydocWASM) {
		t.Fatalf("the page is %d bytes, shorter than the wasm", len(page))
	}
}

func TestSkipReasonFallsBackToTheEngineMessage(t *testing.T) {
	if got := skipReason("", "boom"); got != "boom" {
		t.Fatalf("skip reason %q", got)
	}
	if got := skipReason("", ""); got != "Could not be read" {
		t.Fatalf("empty skip reason %q", got)
	}
}
