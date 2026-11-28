# Performance

The app is a native MyGo UI: a frame rebuilds the view functions, then layout,
paint and present run in the engine. Measure the production bundle separately
from UI-only benchmarks and distinguish Go heap, RSS and physical footprint.

## Benchmarks

`bench_test.go` draws a representative set of UI plugins on `ui.Tester`,
substituting fake backend, workspace, file and skill services (`modes`,
`folderpicker`, `starters`, `workers` and `terminal` are excluded). The window is
1400 × 900 with thirteen tabs in three folders. `wadad`/PiG and native GPU
presentation are excluded. Plugin failures panic so a failed feature cannot
silently reduce the workload.

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

Tester uses software rasterization. Its scrolling and text-reveal costs include
painting that the production GPU window performs differently. Allocation counts
and timings vary with the workload and Go/runtime versions; compare repeated runs
under the same conditions.

The chat UI keeps the eight conversations left most recently, including their
scroll positions. A background read rebuilds a cached transcript only when it
changed. This cache is separate from backend SDK resources.

## Backend retention

`wadad`'s embedded PiG runtime keeps at most eight idle SDK session owners in
least-recently-used order. Service calls, active turns and pending extension
dialogs protect their owner and can exceed that budget. Older idle owners release
their session, runtime, services and event subscription together. Reopening
restores the saved transcript, identity, model, thinking level, mode and
attachment metadata. Extension bindings belong to the newly opened SDK owner.
Metadata for all stored conversations remains indexed. The resource cap is
covered by regression tests for repeated reads, least-recently-used eviction,
restored messages and settings, active tool approvals and extension dialogs.

## The real window

Use temporary data for every experiment. The seeded workload has twelve folders,
56 chats and 24 open tabs; its largest conversation has 120 user and 120 assistant
messages plus 168 tool-result transcript entries.

```
dir=$(mktemp -d)
python3 -I scripts/seed.py "$dir"
WADA_DATA_DIR="$dir/data" MYGO_FRAME_STATS=all build/darwin-arm64/Wada.app/Contents/MacOS/Wada
```

`MYGO_FRAME_STATS=8` logs only frames above 8 ms; `all` logs every frame with
build, layout, paint and present splits. Use `ps -o %cpu,rss,time -p PID` for CPU
and RSS, and `vmmap --summary PID` for physical footprint. CPU of 100% represents
one CPU core. RSS includes resident shared mappings; Go heap excludes native and
GPU resources. OS sampling and memory inspection can stall frames, so collect
latency in a separate run or inspect memory after the measured activity.

## Replayed workloads

`scripts/measure.py` measures the real window under a steady synthetic load. It
seeds a temporary folder, builds the app with the `wadareplay` and
`mygo_noinspector` tags, which install `internal/plugins/replay` (the app never
links it), and reads the process's own resource accounting (`proc_pid_rusage`),
which neither samples nor pauses it: CPU time, idle wakeups, the current footprint
and the highest footprint of each phase. Frame times come from
`MYGO_FRAME_STATS=all`.

```
python3 -I scripts/measure.py [--heavy] [--keep] [--runs N] text|working|background
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
it grows: the costliest case. Events go through the same quiet post as backend
events. The replay excludes daemon IPC, provider streaming, PiG batching, tools,
images and Git diffs. Environment variables shape it:

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

`go tool mygo build` writes the production `.app` and DMG into
`build/darwin-arm64/`. MyGo applies `-s -w`, `-trimpath` and its native
inspector exclusion. Because `main.go` handles `--daemon` and `--terminal-worker`
subprocess dispatch, the desktop binary links the daemon/PiG runtime, providers,
terminal worker, and resources. Icon artwork lives at `assets/icon.png` (`icon`
in `mygo.json`).
