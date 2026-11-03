# Documents

`internal/contract/documents` declares reading a folder's files as Markdown.
`internal/plugins/documents` implements it with [anydoc](https://github.com/firecrawl/anydoc)'s
WebAssembly build, embedded in the app: no service is called and nothing is
installed beside it.

## What it reads

Word, PowerPoint, Excel, OpenDocument, RTF, EPUB, CSV and PDF, chosen by
extension from `extensions`; the format itself comes from the bytes, so a
mislabeled file still converts. anydoc returns GitHub-flavored Markdown with
headings, tables (merged cells included), lists, footnotes, speaker notes and
LaTeX. Excel cells come back as their values, not formulas.

## The engine

`engine` runs one web page for the app's lifetime. At first use it writes
`<DataDir>/documents/anydoc-0.2.4.html` — the wasm-bindgen glue with the
6.4 MiB module inline as base64 — and loads it in a `shell.WebView` that is
never shown. The page starts the module with `initSync` and publishes
`window.__anydoc`: `chunk` takes the document as base64 pieces (256 KiB each,
so no single script is huge) and `convert` returns
`{ok, markdown, format}` or `{ok: false, code, message, pages}`.

`convert` holds `e.mu` for the whole exchange: the page's JavaScript is
single-threaded, so conversions run one at a time. `started` waits up to 20
seconds for `window.__anydocReady` before failing.

The page is its own `mygo` web view, so the converted text never passes
through the app's own UI and the page is not part of any layout.

## Cache and skips

`cached` keys on the content's SHA-256 in `<DataDir>/documents/cache`, so an
edited file is never served stale and a repeat read is a file read. A result
is cached whether or not it converted, so a scanned PDF is not retried on
every run.

A file that cannot be converted is a `Source` with `Skip` set, not an error;
an error means the engine did not run. `needsOcr` becomes "Scanned pages need
OCR" and fills `Pages` with the pages that are image-only. Encrypted,
unsupported, malformed and over-limit files get their own words.

anydoc does no OCR. Reading a scanned PDF as text needs either a local OCR
path or the model reading rendered page images; neither is here yet.

## Where it shows

`documents.sources` is a `chat.Work` section listing the last folder read: one
row per file, its format and length, or why it was skipped. `documents.ingest`
reads the open conversation's folder in the background through `kernel.Go` and
emits `documents.Ingested`. The reader service is `documents.ReaderKey`, for
the job engine to ingest a folder before planning.

## Checking it

`WADA_DOCUMENTS_PROBE` reads one file, or ingests one folder, at startup and
logs what came back; a file's Markdown is also written to
`<DataDir>/documents/probe.md`. Measured on this machine (arm64): a 1.0k-char
`docx` in 21 ms, a 53k-char 12-page PDF in 455 ms from cold, and a nine-file
mixed folder in 477 ms cold and 2 ms from cache.
