// Package documents declares reading a folder's files as Markdown. The
// documents plugin converts Word, PowerPoint, Excel, OpenDocument, RTF,
// EPUB, CSV and PDF in the app through anydoc's WebAssembly build, so no
// service is called and nothing is installed beside the app.
package documents

import (
	"context"

	"github.com/xleotranx/wada-kit/kernel"
)

// Source is one file read as Markdown.
type Source struct {
	Path     string // absolute and clean
	Format   string // what the content says it is, such as "docx"
	Bytes    int    // the file's size
	Chars    int    // the Markdown's length in runes
	Markdown string
	// Pages lists PDF pages that are scanned or image-only: the file was
	// not converted, and Skip says so. A future reader may read them as
	// images.
	Pages []int
	// Skip is why the file was not read, in words for the user; empty when
	// it was.
	Skip string
}

// Converted reports that the source has Markdown.
func (s Source) Converted() bool { return s.Skip == "" && s.Markdown != "" }

// Result is one folder read.
type Result struct {
	Folder  string
	Sources []Source
	Read    int // how many sources have Markdown
	Chars   int // their Markdown's total length
}

// Reader reads documents as Markdown. Its methods block and may be called
// from a worker; they never touch plugin state.
type Reader interface {
	// Read converts one file. A file that cannot be read comes back with
	// Skip set rather than an error; an error means the engine itself failed.
	Read(ctx context.Context, path string) (Source, error)
	// Ingest reads every supported file directly in folder, in name order,
	// skipping hidden folders and .wada.
	Ingest(ctx context.Context, folder string) (Result, error)
}

var (
	ReaderKey = kernel.NewKey[Reader]("documents.reader")
	// Ingested is emitted on the UI thread when a folder read finishes.
	Ingested = kernel.NewTopic[Result]("documents.ingested")
)
