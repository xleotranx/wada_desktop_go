// Package documents reads the files of a folder as Markdown. It converts
// Word, PowerPoint, Excel, OpenDocument, RTF, EPUB, CSV and PDF with
// anydoc's WebAssembly build inside a web page of its own, so the app
// carries its reader and calls no service.
package documents

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/documents"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/contract/shell"
	"github.com/xleotranx/wada-kit/kernel"
)

// Plugin is the documents plugin: the reader service, the Sources section of
// the work panel and the command that reads a conversation's folder.
type Plugin struct {
	ctx    *kernel.Context
	host   shell.Host
	chat   chat.Controller
	engine *engine
	work   kernel.Task
	last   documents.Result
}

// New makes the plugin; the kernel makes one for each activation.
func New() kernel.Plugin { return &Plugin{} }

func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{
		ID: "documents", Title: "Documents",
		Description: "Reads Word, PowerPoint, Excel, PDF and the other documents in a folder as Markdown, in the app.",
		Provides:    []kernel.Named{documents.ReaderKey},
		Requires:    []kernel.Named{shell.HostKey, chat.ControllerKey},
	}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.ctx = c
	p.host = kernel.Require(c, shell.HostKey)
	p.chat = kernel.Require(c, chat.ControllerKey)
	p.engine = newEngine(filepath.Join(p.host.DataDir(), "documents"), p.host.NewWebView)
	c.OnDispose(p.engine.close)
	if err := kernel.Provide[documents.Reader](c, documents.ReaderKey, p.engine); err != nil {
		return err
	}
	if err := kernel.Contribute(c, kernel.Commands, "documents.ingest", 60, kernel.Command{
		Title: "Read Documents in Folder", Menu: "Agent", Run: p.ingest,
	}); err != nil {
		return err
	}
	if err := kernel.Contribute(c, chat.Work, "documents.sources", 40, chat.WorkSection{
		Title: "Sources", Shown: p.shown, Render: p.sources,
	}); err != nil {
		return err
	}
	// WADA_DOCUMENTS_PROBE reads one file at startup and writes its Markdown
	// beside the engine, so the reader can be checked without a window.
	if path := os.Getenv("WADA_DOCUMENTS_PROBE"); path != "" {
		p.probe(path)
	}
	return nil
}

// ingest reads the open conversation's folder in the background, then shows
// what came back. It runs on the UI thread.
func (p *Plugin) ingest() {
	folder := p.chat.Current().Directory
	if folder == "" {
		p.host.Toast(ui.Toast{Title: "Open a conversation in a folder first"})
		return
	}
	kernel.Go(p.ctx, &p.work, func(ctx context.Context) (documents.Result, error) {
		return p.engine.Ingest(ctx, folder)
	}, func(res documents.Result, err error) {
		if err != nil {
			log.Printf("documents: %v", err)
			p.host.Toast(ui.Toast{Title: "Could not read the folder"})
			return
		}
		p.last = res
		kernel.Emit(p.ctx, documents.Ingested, res)
		if res.Read == 0 {
			p.host.Toast(ui.Toast{Title: "No documents could be read"})
		}
	})
}

// probe reads a file or a folder at startup, for checking the engine
// headlessly.
func (p *Plugin) probe(path string) {
	task := &kernel.Task{}
	kernel.Go(p.ctx, task, func(ctx context.Context) (probeResult, error) {
		start := time.Now()
		res := probeResult{}
		info, err := os.Stat(path)
		switch {
		case err != nil:
			return res, err
		case info.IsDir():
			folder, err := p.engine.Ingest(ctx, path)
			res.folder = &folder
			res.took = time.Since(start)
			return res, err
		default:
			src, err := p.engine.Read(ctx, path)
			res.src = src
			res.took = time.Since(start)
			return res, err
		}
	}, func(res probeResult, err error) {
		if err != nil {
			log.Printf("documents probe: %s: %v", path, err)
			return
		}
		dir := filepath.Join(p.host.DataDir(), "documents")
		if folder := res.folder; folder != nil {
			log.Printf("documents probe: %s %d files, %d read, %d chars in %s",
				path, len(folder.Sources), folder.Read, folder.Chars, res.took.Round(time.Millisecond))
			for _, src := range folder.Sources {
				log.Printf("documents probe:   %-14s %-6s %6d chars pages=%v skip=%q",
					filepath.Base(src.Path), src.Format, src.Chars, src.Pages, src.Skip)
			}
			return
		}
		src := res.src
		log.Printf("documents probe: %s format=%q chars=%d pages=%v skip=%q in %s",
			filepath.Base(path), src.Format, src.Chars, src.Pages, src.Skip, res.took.Round(time.Millisecond))
		_ = os.WriteFile(filepath.Join(dir, "probe.md"), []byte(src.Markdown), 0o600)
	})
}

// probeResult is what the startup probe reports.
type probeResult struct {
	src    documents.Source
	folder *documents.Result
	took   time.Duration
}

// shown reports that there are sources to list.
func (p *Plugin) shown() bool { return len(p.last.Sources) > 0 }
