# Performance

The app is a native MyGo UI: a frame rebuilds the view functions, then layout,
paint and present run in the engine. Measure the production bundle separately
from UI-only benchmarks and distinguish Go heap, RSS and physical footprint.

## Benchmarks

`bench_test.go` draws the production feature plugins on `ui.Tester`, substituting
fake backend, workspace, file and skill services. The window is 1400 × 900 with
thirteen tabs in three folders. PiG and native GPU presentation are excluded.
Plugin failures panic so a failed feature cannot silently reduce the workload.

```
go test -run NONE -bench . -benchtime 128x -count 5 .
```

| Benchmark | Measures |
|---|---|
| `EmptyPageIdle` | One frame of the new-chat screen beside the tabs |
| `TranscriptIdle` | One frame of a settled 120-message conversation |
| `StreamDelta` | A text update plus one refresh of simulated time (1/60 s), including the frame the paced reveal asks for |
| `TranscriptScroll` | One frame per wheel notch over the conversation |
| `SwitchTab` | Opening another cached 120-message conversation and completing its background refresh |

The stream targets the visible conversation, checks that streamed text appears,
and uses matching ordinals for start, delta and end events. Its updates are about
52 bytes of words drawn at random with a fixed seed, so lines do not repeat as a
repeated sentence's would, which would let text layouts be reused as real answers
never do. Every 64 updates it starts a fresh block. At least 128 iterations exercise multiple blocks. Its
operation can render several software frames; do not call it a native frame time.
Absolute process-wide heap readings are excluded from these per-operation results.

Measurements on 27 October 2026: Apple M4, 16 GiB RAM, macOS 27.0.1, Go 1.27.3,
128 iterations per benchmark, median of five runs:

| Benchmark | Time per operation | Bytes per operation | Allocations per operation |
|---|---:|---:|---:|
| EmptyPageIdle | 191 µs | 5,992 | 81 |
| TranscriptIdle | 276 µs | 7,129 | 103 |
| StreamDelta | 1.54 ms | 96,372 | 381 |
| TranscriptScroll | 1.89 ms | 21,715 | 313 |
| SwitchTab | 462 µs | 238,860 | 564 |

Tester uses software rasterization. Its scrolling and text-reveal costs include
painting that the production GPU window performs differently. Allocation counts
and timings vary with the workload and Go/runtime versions; compare repeated runs
under the same conditions.

The chat UI keeps the eight conversations left most recently, including their
scroll positions. A background read rebuilds a cached transcript only when it
changed. This cache is separate from backend SDK resources.

## Backend retention

PiG keeps at most eight idle SDK session owners in least-recently-used order.
Service calls, active turns and pending extension dialogs protect their owner and
can exceed that budget. Older idle owners release their session, runtime,
services and event subscription together. Reopening restores the saved transcript,
identity, model, thinking level, mode and attachment metadata. Extension bindings
belong to the newly opened SDK owner.

A diagnostic using the real PiG plugin and the 56-chat seed, with GC before each
heap reading, measured:

| Chats read | Live SDK owners | Retained Go heap | Goroutines |
|---|---:|---:|---:|
| 0 | 0 | 1.16 MiB | 3 |
| 1 | 1 | 1.45 MiB | 4 |
| 8 | 8 | 1.98 MiB | 11 |
| 24 | 8 | 2.27 MiB | 11 |
| 56 | 8 | 2.51 MiB | 11 |

This is backend Go heap, excluding the native UI and graphics allocations.
Metadata for all stored conversations remains indexed. The resource cap is
covered by regression tests for repeated reads, least-recently-used eviction,
restored messages and settings, active tool approvals and extension dialogs.
Three passes over the 56-chat fixture retained eight SDK owners and eleven
goroutines throughout; heap after each pass was 2.52, 2.57 and 2.59 MiB.

## The real window

Use temporary data for every experiment. The seeded workload has twelve folders,
56 chats and 24 open tabs; its largest conversation has 120 user and 120 assistant
messages plus 168 tool-result transcript entries.

```
dir=$(mktemp -d)
python3 -I scripts/seed.py "$dir"
WADA_DATA_DIR="$dir/data" MYGO_FRAME_STATS=all   build/darwin-arm64/wada_desktop_go.app/Contents/MacOS/wada_desktop_go
```

`MYGO_FRAME_STATS=8` logs only frames above 8 ms; `all` logs every frame with
build, layout, paint and present splits. Use `ps -o %cpu,rss,time -p PID` for CPU
and RSS, and `vmmap --summary PID` for physical footprint. CPU of 100% represents
one CPU core. RSS includes resident shared mappings; Go heap excludes native and
GPU resources. OS sampling and memory inspection can stall frames, so collect
latency in a separate run or inspect memory after the measured activity.

The M4 production bundle settled at 45.0 MiB physical footprint on an empty page,
49.4 MiB with the seeded short chat selected, and 55.4 MiB with the largest chat
selected. Idle CPU was near zero; only two to four startup frames were drawn over
48 seconds. Startup physical-footprint peaks ranged from 109 to 120 MiB.

Of a settled 49 MiB with a seeded chat open, `vmmap --summary` attributes about
16 MiB to native malloc zones, 12 MiB to untagged memory (the Go heap and
runtime), 10 MiB to dirty framework and executable data, 6 MiB to IOSurface and
GPU memory, and the rest to Core Animation, page tables and smaller regions.
Package initialization allocates 0.7 MB in 1.5 ms (`GODEBUG=inittrace=1`).

## Replayed workloads

`scripts/measure.py` measures the real window under a steady synthetic load. It
seeds a temporary folder, builds the app with the `wadareplay` and
`mygo_noinspector` tags, which install `internal/plugins/replay` (the app never
links it), and reads the process's own resource accounting (`proc_pid_rusage`),
which neither samples nor pauses it: CPU time, idle wakeups, the current footprint
and the highest footprint of each phase. Frame times come from
`MYGO_FRAME_STATS=all`.

```
python3 -I scripts/measure.py [--heavy] [--runs N] text|working|background
```

The replay waits 6 s, replays for 30 s, ends the turn and quits 10 s later; the
phases are startup, replay and settled. Its modes:

| Mode | Workload |
|---|---|
| `text` | text deltas of about 52 bytes at 60 a second into the open conversation, a fresh block every 64 |
| `working` | a turn whose tool runs throughout: no text, the "Working…" indicator |
| `background` | the `text` stream into another open tab's conversation |

The text is words drawn at random with a fixed seed. Without paragraph breaks a
block is one paragraph that grows to 3.3 KB, which the window lays out again as
it grows: the costliest case. Events go through the same quiet post as PiG's
event pump. The replay excludes provider streaming, PiG batching, tools, images
and Git diffs. Environment variables shape it:

| Variable | Effect |
|---|---|
| `WADA_REPLAY_PARAGRAPH=n` | ends a paragraph every n updates, as answers do |
| `WADA_REPLAY_HZ=n` | batches a second (default and most 60), the text at the same pace |
| `WADA_REPLAY_PROFILE=path` | writes a CPU profile of the replay phase |
| `WADA_REPLAY_DELAY`, `_FOR`, `_SETTLE` | the phases' lengths |

The script notes when the window was not in front, which slows the paced reveal.

`--heavy` uses `scripts/seed.py --heavy`, which adds what use accumulates: change
baselines of 12 × 64 KB files per chat, image attachments (a 5120 × 2880
screenshot in the longest chat, 1600 × 1000 images in every fourth), 100 KB tool
outputs, and a 200 MB project of 5,000 files in the first folder.

Measurements on 27 October 2026, the M4 above, a 60 Hz display, the window in front; one run each, two for `text`, which agreed within 0.1 points.
CPU is a percentage of one core:

| Mode | Phase | CPU | Frames/s (built) | p50 / p95 frame | Median layout | Peak / end footprint |
|---|---|---:|---:|---:|---:|---:|
| `text` | replay | 21.4% | 28.9 (28.9) | 5.5 / 7.2 ms | 3.5 ms | 132.2 / 132.2 MiB |
| `text` | settled | 1.2% | 1.5 | 5.7 / 6.9 ms | 3.8 ms | 132.2 / 64.2 MiB |
| `text`, paragraphs of 8 updates | replay | 16.1% | 28.7 (28.7) | 3.2 / 4.1 ms | 1.1 ms | 131.3 / 131.3 MiB |
| `working` | replay | 1.6% | 2.9 (1.0) | 1.4 / 2.8 ms | 0.7 ms | 116.6 / 116.6 MiB |
| `background` | replay | 4.1% | 3.8 (3.8) | 2.0 / 2.6 ms | 0.3 ms | 115.8 / 115.8 MiB |
| `text`, heavy seed | replay | 21.8% | 28.8 (28.8) | 5.4 / 7.2 ms | 3.5 ms | 211.4 / 211.4 MiB |

Startup peaks at 113–118 MiB within the first second and settles at about
51 MiB before the replay (107 MiB with the heavy seed, whose metadata PiG holds
in memory). The peaks during activity are mostly the window's drawables, about
20 MiB each at this size, which MyGo keeps three of while something moves.

- `text` builds the 30 frames a second of the paced reveal. Each lays out the
  growing paragraph again, which is most of its cost (CoreText shaping in
  `layout`), so shorter paragraphs cost less.
- `working` repaints the "Working…" shimmer for its first 2 s, then builds a frame
  a second for the elapsed time. Frames once a second keep MyGo from releasing the
  extra drawables and the in-memory frame it frees after 2 s without frames, so
  the footprint stays high until the turn ends.
- `background` draws no frame for the events themselves: the conversation is
  kept up to date unseen, and the kernel's 250 ms fallback for quiet posts accounts
  for the frames.

## Bundle size

`go tool mygo build .` writes the production `.app` and DMG into
`build/darwin-arm64/`. MyGo already applies `-s -w`, `-trimpath` and its native
inspector exclusion. The embedded PiG SDK, providers and resources ship in the
executable. Icon artwork lives at `assets/icon.png` (`icon` in `mygo.json`).

The arm64 production build is approximately 50.2 MiB installed and 18.0 MiB as a
DMG. The main executable is 48.5 MiB; the SQLite library is 1.48 MiB. Sizes are
sums of file bytes, rather than filesystem allocation sizes.
