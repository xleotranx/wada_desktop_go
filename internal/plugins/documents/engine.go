package documents

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"wada-desktop-go/internal/contract/documents"

	"github.com/xleotranx/wada-kit/contract/shell"
)

// anydocGlue and anydocWASM are anydoc's WebAssembly build, embedded so the
// app carries its own reader. anydoc is MIT licensed; see ANYDOC-LICENSE.
//
//go:embed anydoc_wasm.js
var anydocGlue string

//go:embed anydoc_wasm_bg.wasm
var anydocWASM []byte

// enginePage names the page the engine runs in, including the version, so a
// new anydoc build replaces it rather than reusing a stale one.
const enginePage = "anydoc-0.2.4.html"

// chunkSize is how much base64 the engine takes per Eval: a whole document
// in one call would make one enormous script.
const chunkSize = 256 << 10

// startWait is how long the page has to load the module and start anydoc.
const startWait = 20 * time.Second

// pollEvery is how often the engine is asked whether it started.
const pollEvery = 80 * time.Millisecond

// extensions are the file kinds anydoc converts.
var extensions = map[string]bool{
	".doc": true, ".docx": true, ".docm": true,
	".ppt": true, ".pptx": true, ".pptm": true,
	".pps": true, ".ppsx": true, ".ppsm": true,
	".xls": true, ".xlsx": true, ".xlsm": true, ".xlsb": true,
	".odt": true, ".ods": true, ".odp": true,
	".rtf": true, ".epub": true, ".csv": true, ".pdf": true,
}

// engine runs anydoc in a web page of its own: the library's WebAssembly
// build converts the document, so the app installs nothing else. One page
// serves every conversion, and conversions run one at a time because the
// page's JavaScript is single-threaded.
type engine struct {
	dir    string
	page   string
	newWeb func() (shell.WebView, error)

	mu    sync.Mutex
	web   shell.WebView
	ready bool // the page loaded and anydoc started
}

// newEngine prepares the folder the page and its cache live in.
func newEngine(dir string, newWeb func() (shell.WebView, error)) *engine {
	return &engine{dir: dir, page: filepath.Join(dir, enginePage), newWeb: newWeb}
}

// Read converts one file, from the cache when its bytes were read before.
func (e *engine) Read(ctx context.Context, path string) (documents.Source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return documents.Source{Path: path}, err
	}
	return e.cached(ctx, path, data)
}

// Ingest reads every supported file directly in folder, in name order.
func (e *engine) Ingest(ctx context.Context, folder string) (documents.Result, error) {
	res := documents.Result{Folder: folder}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return res, err
	}
	for _, ent := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		name := ent.Name()
		if ent.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if !extensions[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		src, err := e.Read(ctx, filepath.Join(folder, name))
		if err != nil {
			return res, err
		}
		res.Sources = append(res.Sources, src)
		if src.Converted() {
			res.Read++
			res.Chars += src.Chars
		}
	}
	return res, nil
}

// cached reads the source for bytes of path from the cache, converting and
// then keeping it when it is new. The key is the content, so an edited file
// is never served stale.
func (e *engine) cached(ctx context.Context, path string, data []byte) (documents.Source, error) {
	if err := os.MkdirAll(filepath.Join(e.dir, "cache"), 0o700); err != nil {
		return documents.Source{Path: path}, err
	}
	sum := sha256.Sum256(data)
	key := filepath.Join(e.dir, "cache", hex.EncodeToString(sum[:])+".json")
	if b, err := os.ReadFile(key); err == nil {
		var src documents.Source
		if json.Unmarshal(b, &src) == nil && src.Path != "" {
			src.Path = path
			return src, nil
		}
	}
	src, err := e.convert(ctx, path, data)
	if err != nil {
		return src, err
	}
	if b, mErr := json.Marshal(src); mErr == nil {
		_ = os.WriteFile(key, b, 0o600)
	}
	return src, nil
}

// convert sends the bytes to the page and reads what came back.
func (e *engine) convert(ctx context.Context, path string, data []byte) (documents.Source, error) {
	src := documents.Source{Path: path, Bytes: len(data)}
	if len(data) == 0 {
		src.Skip = "Empty file"
		return src, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	web, err := e.started(ctx)
	if err != nil {
		return src, err
	}
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 0 {
		n := min(len(enc), chunkSize)
		if _, err := web.Eval(ctx, "window.__anydoc.chunk("+strconv.Quote(enc[:n])+")"); err != nil {
			return src, err
		}
		enc = enc[n:]
	}
	raw, err := web.Eval(ctx, "window.__anydoc.convert()")
	if err != nil {
		return src, err
	}
	answer, ok := raw.(map[string]any)
	if !ok {
		return src, fmt.Errorf("documents: the reader answered %T", raw)
	}
	if format, _ := answer["format"].(string); format != "" {
		src.Format = format
	}
	if converted, _ := answer["ok"].(bool); !converted {
		code, _ := answer["code"].(string)
		message, _ := answer["message"].(string)
		src.Skip = skipReason(code, message)
		src.Pages = pageList(answer["pages"])
		return src, nil
	}
	markdown, _ := answer["markdown"].(string)
	src.Markdown = markdown
	src.Chars = len([]rune(markdown))
	if markdown == "" {
		src.Skip = "No text came out"
	}
	return src, nil
}

// started returns the page once anydoc runs in it, starting it on first use.
// The caller holds e.mu.
func (e *engine) started(ctx context.Context) (shell.WebView, error) {
	if e.ready && e.web != nil {
		return e.web, nil
	}
	if e.web == nil {
		web, err := e.newWeb()
		if err != nil {
			return nil, err
		}
		e.web = web
	}
	if err := e.writePage(); err != nil {
		return nil, err
	}
	if err := e.web.LoadFile(e.page); err != nil {
		e.web.Close()
		e.web = nil
		return nil, err
	}
	deadline := time.Now().Add(startWait)
	for {
		if v, err := e.web.Eval(ctx, "window.__anydocReady === true"); err == nil {
			if ready, _ := v.(bool); ready {
				e.ready = true
				return e.web, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, errors.New("documents: the reader did not start")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

// writePage keeps the page on disk: it carries the embedded wasm as base64,
// so it is written once per version.
func (e *engine) writePage() error {
	if _, err := os.Stat(e.page); err == nil {
		return nil
	}
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		return err
	}
	page := pageHTML()
	tmp := e.page + ".tmp"
	if err := os.WriteFile(tmp, page, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.page)
}

// close takes the page away; the engine can start another.
func (e *engine) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.web != nil {
		e.web.Close()
		e.web = nil
	}
	e.ready = false
}

// pageHTML is one page that starts anydoc and offers this file to it.
func pageHTML() []byte {
	var b strings.Builder
	b.Grow(len(anydocWASM)*4/3 + len(anydocGlue) + 4096)
	b.WriteString("<!doctype html>\n<meta charset=\"utf-8\">\n<title>reader</title>\n")
	b.WriteString("<script id=\"anydoc-wasm\" type=\"application/octet-stream\">")
	b.WriteString(base64.StdEncoding.EncodeToString(anydocWASM))
	b.WriteString("</script>\n<script type=\"module\">\n")
	b.WriteString(strings.ReplaceAll(anydocGlue, "</script>", "<\\/script>"))
	b.WriteString("\n")
	b.WriteString(bootScript)
	b.WriteString("\n</script>\n")
	return []byte(b.String())
}

// bootScript starts the module with the embedded bytes and publishes the
// two calls Go makes: chunk takes the document in pieces, convert reads it.
const bootScript = `
const el = document.getElementById('anydoc-wasm');
const bin = atob(el.textContent);
el.textContent = '';
const bytes = new Uint8Array(bin.length);
for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
initSync({ module: bytes });
window.__anydoc = {
	parts: [],
	chunk(text) { this.parts.push(text); return this.parts.length; },
	convert() {
		const all = atob(this.parts.join(''));
		this.parts = [];
		const u = new Uint8Array(all.length);
		for (let i = 0; i < all.length; i++) u[i] = all.charCodeAt(i);
		const format = formatFromBytes(u) || '';
		try {
			return { ok: true, markdown: toMarkdownBytes(u), format };
		} catch (e) {
			return { ok: false, code: e.code || '', message: String(e.message || e), pages: e.pages || [], format };
		}
	}
};
window.__anydocReady = true;
`

// skipReason says in words why a file did not convert.
func skipReason(code, message string) string {
	switch code {
	case "needsOcr":
		return "Scanned pages need OCR"
	case "encrypted":
		return "Password-protected"
	case "unsupported":
		return "Not a document the reader knows"
	case "malformed":
		return "Unreadable"
	case "resourceLimit":
		return "Too large to convert"
	}
	if message != "" {
		return message
	}
	return "Could not be read"
}

// pageList turns the pages of a needsOcr answer into page numbers.
func pageList(v any) []int {
	list, _ := v.([]any)
	if len(list) == 0 {
		return nil
	}
	pages := make([]int, 0, len(list))
	for _, p := range list {
		if n, ok := p.(float64); ok && n > 0 {
			pages = append(pages, int(n))
		}
	}
	return pages
}
