# Performance

How the app's performance is measured and what it currently costs. The app is
a native MyGo UI: a frame rebuilds the view functions, then layout, paint and
present run in the engine.

## Benchmarks

`bench_test.go` at the root draws the whole app — shell chrome, a tab strip of
twelve conversations in three folders plus the one on show, the page and
composer — on `ui.Tester` with the real plugins and the fake backend and
workspace record from `plugintest`, at the window's default size:

```
go test -run NONE -bench . -benchtime 30x .
```

| Benchmark | Measures |
|---|---|
| `EmptyPageIdle` | One frame of the new-chat screen beside the tabs: the floor every invalidation pays |
| `TranscriptIdle` | One frame of a settled 120-row conversation |
| `StreamDelta` | One frame per streamed text delta, the hot path of a live turn |
| `TranscriptScroll` | One frame per wheel notch over the conversation |
| `SwitchTab` | Showing another open conversation of 120 messages, kept in memory: the frame that shows it, and the read again in the background |

Current numbers (Tester excludes GPU paint and present, which
`MYGO_FRAME_STATS` measures below). Frame times are from an M4, 30 iterations
each; bytes and allocations per frame are counted by the Go runtime, the same
on any machine:

| Benchmark | Frame | Bytes | Allocs |
|---|---|---:|---:|
| EmptyPageIdle | 128 µs | 4,798 | 82 |
| TranscriptIdle | 232 µs | 5,346 | 92 |
| StreamDelta | 207 µs | 5,646 | 97 |
| TranscriptScroll | 1.7 ms | 16,392 | 283 |
| SwitchTab | 361 µs | 260,648 | 560 |

Most of the time of `TranscriptScroll` and `SwitchTab` is the Tester's
software rasterizer repainting the window, which the GPU does in the real
window; building and laying out the view is about 0.1 ms of a switch. The
chat keeps the eight conversations left most recently, so switching among
them draws one frame with no loading screen and keeps the scroll place;
the background read that follows rebuilds the transcript only when the
conversation changed. A conversation that is not kept is read first, as
before: Pi parses a 200-turn, 164 KB session file in 1.5 ms (1.7 MB
allocated), growing with the file, and OpenCode fetches it over HTTP.
The kept conversations hold about 3 MB of heap for eight of 120 messages;
past that, 3,000 switches across twelve tabs leave it within 0.7 MB.

Most of the per-frame allocation floor is MyGo's element machinery, a pinned
dependency. A frame builds nothing of the app's own that has not changed: the
shell builds its table of shortcuts again only when the commands change, the
chat folds a conversation's turns again only when its rows' order, kinds or
running turn change (streamed words reuse the folding), `Setting.Get` decodes
a value once until a setting changes, the tab strip draws from a buffer it
keeps and from state the tabs plugin holds, without I/O, and the OpenCode
decoder names event kinds from one table rather than one built per event.

## The real window

Set `MYGO_FRAME_STATS=8` before launching to log every frame over 8 ms with
its build, layout, paint and present split:

```
WADA_DATA_DIR=/tmp/wada MYGO_FRAME_STATS=8 open ...
```

`mygo build` sets production flags and embeds the icon; the app is otherwise
the same binary. Sample the process with `ps -o %cpu,rss -p PID` while the
app works, and `vmmap --summary PID` for the true cost of memory: `ps`
counts the framework's mapped code, `Physical footprint` is the RAM the
window actually holds.

Current shape on an M4, with `make demo`'s data (twelve folders, 56 chats,
24 open tabs): the app idles at 0.0 % CPU (no frames are drawn when nothing
changes), a turn streaming from OpenCode renders frames under the 8 ms
threshold throughout, and the first boot frame is the only one over it
(~24 ms, half of it the GPU's first present); the first transcript frame
takes ~10 ms. While a turn runs, the "Working…" shimmer repaints at 30
frames a second without building the view. The idle footprint is ~44 MB and
peak ~113 MB while the first frame and the font caches warm.

## Bundle size

`go tool mygo build .` writes the .app and the DMG into `build/`. The binary
is ~12.9 MB, nearly all of it the pinned MyGo runtime, its GPU engine and
the SQLite helper dylib (1.5 MB); the app's own packages are ~315 KB of
code. Icon artwork lives at `assets/icon.png` (`icon` in mygo.json); MyGo
turns it into `AppIcon.icns` and ships it on every platform except macOS,
where Finder uses the .icns instead.
