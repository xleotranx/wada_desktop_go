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

Current numbers on an M4 (30 iterations each; Tester excludes GPU paint and
present, which `MYGO_FRAME_STATS` measures below):

| Benchmark | Frame | Bytes | Allocs |
|---|---|---:|---:|
| EmptyPageIdle | 144 µs | 9,419 | 134 |
| TranscriptIdle | 398 µs | 16,766 | 217 |
| StreamDelta | 437 µs | 4.0 MB heap | — |
| TranscriptScroll | 1.8 ms | 52,276 | 669 |

Most of the per-frame allocation floor is MyGo's element machinery, a pinned
dependency. The app parses command shortcuts once rather than every frame;
the tab strip draws from state the tabs plugin keeps, without I/O.

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

Current shape on an M4: the app idles at 0.0 % CPU (no frames are drawn
when nothing changes), a turn streaming from OpenCode renders frames under
the 8 ms threshold throughout, and the first boot frame is the only one over
it (~15–19 ms). The idle footprint is ~45 MB and peak ~110 MB while the
first frame and the font caches warm.

## Bundle size

`go tool mygo build .` writes the .app and the DMG into `build/`. The binary
is ~12.9 MB, nearly all of it the pinned MyGo runtime, its GPU engine and
the SQLite helper dylib (1.5 MB); the app's own packages are ~315 KB of
code. Icon artwork lives at `assets/icon.png` (`icon` in mygo.json); MyGo
turns it into `AppIcon.icns` and ships it on every platform except macOS,
where Finder uses the .icns instead.
