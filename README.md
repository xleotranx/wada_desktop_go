# wada_desktop_go

A native macOS chat client for the ChatGPT desktop look, driven by [OpenCode](https://github.com/sst/opencode) as its agent engine. All UI is Go through [MyGo](https://github.com/egoist/mygo) — no webview, no Electron, one package.

The design-copy era is over: every visible control answers. Conversations, turns, approvals, search, model picking, folders, files and the shell policy are real, backed by an `opencode serve` subprocess the app owns — or by any opencode server the Settings page is pointed at.

## Requirements

- macOS (the UI uses native windows and hidden title bars).
- A Go toolchain; MyGo is pinned in `go.mod` and runs as `go tool mygo`.
- The `opencode` CLI v2 on `PATH`. The app spawns it itself (`opencode serve`) on its own port with a key of its own; your global `~/.config/opencode` config is untouched. Settings can instead point the app at a server you run yourself, with the key it was started with.

## Run

```
go tool mygo dev .      # live reload; q quits, r forces a rebuild
go run .                # plain instance
go tool mygo build      # .app / .dmg into build/
```

First run restores yesterday's conversations from OpenCode's own store — nothing is kept in the app.

## Test

```
make check                                      # the gate: mygo vet, golangci-lint, tests under -race and once without cgo
go test ./...                                   # plain tests, single: go test -run TestChooseChat ./...
WADA_LIVE_AGENT=1 go test -run 'TestLiveAgent|TestLiveModel|TestLivePermission|TestLiveLocation|TestLiveSessionOps' .   # real server, ~a minute
```

UI verification without clicking: `WADA_CAPTURE=/path/out.png go run .` writes a PNG of the window and quits; dev builds carry an element inspector on `F12` (`Shift+Cmd+C` picks an element, Computed shows exact boxes).

## Performance

```
go test -run '^$' -bench 'Benchmark(Transcript|Sidebar|Stream)$' -benchmem -count=3 .
MYGO_FRAME_STATS=all go run .
```

The benchmarks force warm headless frames at 1400×920, including layout and CPU painting. They do not measure native GPU presentation or idle CPU. Frame statistics split native work into build, layout, paint and presentation. Count the owned `opencode serve` process separately when measuring total CPU and RAM.

On an Apple M4 with Go 1.27.2, the October 2026 fixes reduced a 1,000-message frame from 11.3 ms to 0.175 ms and retained UI heap from 81.5 MiB to 2.64 MiB. A 2,000-row sidebar fell from 3.81 ms / 481 KB allocated per frame to 0.100 ms / 7 KB. Assembling a 128 KiB answer in 32-byte deltas fell from 283 MB of cumulative allocation to 0.84 MB. These are synthetic comparisons, not production latency percentiles.

Transcripts and chat search results use keyed virtual lists; streaming uses amortized storage and bounded event batches. Searches debounce and cancel on a separate worker. The permission label is cached: a worker checks for config changes once a second and reads JSON only after metadata changes. Regression tests cover visibility, scroll following, row identity, cancellation and memory retention. The stream allocation guard allows JSON decoder pool misses under the race detector and still rejects quadratic copying.

Use `go tool mygo build` for size comparisons: MyGo already strips symbols and debug data, trims paths, and omits the inspector. A plain `go build` artifact includes development overhead. The release DMG compresses the app; executable size, bundle size and installer size are different measurements.

## What is real

- **Sessions** — list, restore, create, rename, delete, pagination, per-chat working directories. Search is server-side (`GET /session?search=…`) with a stale-result guard.
- **Turns** — stream, stop, approval cards for shell and edit asks, activity rows for tools as they run.
- **Models** — the picker reads the active server's catalog, free first, with per-model reasoning-effort chips; the switch folds back over `session.model.selected`. Settings lists the whole catalog grouped by provider with a switch per model and Enable all / Disable all, and the composer's picker offers only what stays on. New chats run `opencode-go/longcat-2.5-preview-free` while it is enabled, else the first model still on, unless `WADA_MODEL=provider/model` says otherwise.
- **Servers** — the rail's gear opens the settings page: the local server the app runs itself (its key settable, or empty for one generated each start) beside any servers added by address and key. Use, Edit and Delete manage them, the active row reconnects from its menu, and the others are probed so their rows say how they last answered. Switching servers clears the view for that server's chats, its own pins, catalog and model choices.
- **Folders** — every chat names its directory; Add folder, Open folder… and New project all open the system's folder chooser.
- **Sidebar metadata** — MyGo SQLite saves the app's state in `metadata.db` under the OS user configuration directory's `wada-desktop-go` folder (`~/Library/Application Support/wada-desktop-go/metadata.db` on macOS): project folders, project pins, chat pins per server, server connections and disabled models. This path is shared by dev and packaged builds. The sidebar shows pinned chats and projects, and each project lists the chats that work in its folder: open by default, folded away with a click, five of them before a Show more row, while conversation results appear only during search. Projects start empty and only explicitly added folders appear; the version 2 migration clears the legacy imported project list once, preserving chat pins, and version 3 adds the settings tables without touching either. The Projects `+` opens the folder chooser, and a project's hover `+` or menu starts a blank draft in that folder. New drafts have a searchable project chooser above the composer, including New project and no-project actions. The first message creates the server chat in the chosen directory, and its row lands under that folder. Each project's menu pins or removes it without deleting files or conversations. Missing folders remain marked unavailable. Chat pins retain their order and load beyond the first history page. OpenCode continues to own all conversation contents.
- **Files** — the composer's `+` quotes a workspace file into the draft as a fenced block (path through `~` or relative to the chat's folder, 256 KiB cap, de-duplicated).
- **Shell policy** — the pill reads the chat's `opencode.json` for the bash wildcard and the menu writes it back; the running server picks the file up, so the next turn runs under what the pill shows. An unreadable config leans to "Asks first" and refuses to be overwritten.
- **Markdown** — streaming and settled answers render through MyGo's markdown package: headings, fenced code, lists, bold, italic, inline code and links.
- **Resilience** — a dead server ends one attempt, not the session: the pipe dials again with a stretching backoff, half-said answers settle as cut short, and queued jobs replay against the fresh server. Switching servers in Settings jumps that wait.

## Honest boundaries

- Links render underlined but do not open yet, and quoting a file still takes a typed path rather than a picker; no dictate and no archive — the first has no surface worth faking, the second no server endpoint.
- Server keys are stored as plain text in the local `metadata.db`, not the Keychain. A local server without a key set still gets a fresh generated one for each start, which only the app knows.
- One server is active at a time: a conversation belongs to the server it was created on, and switching servers swaps the whole view rather than mixing both.

## Layout

`main.go` (state, window, boot) → `sidebar.go` (rows, kebabs, bell, search), `chat.go` (transcript blocks, approvals), `composer.go` (draft, model picker, dialogs, pill), `browser.go` (right tools pane), `settings.go` (the settings page, the server dialogs and the model switches) → `agent.go` (HTTP and runner), `agent_events.go` (bounded SSE updates), `agent_apply.go` (the stream folded into the transcript), `servers.go` (connections, health, probe), `paths.go` (workspace paths), `search.go` (cancelable lookups), `policy.go` (workspace config cache and permission read/write), `metadata.go` (the app's SQLite store), `pinned.go`, `project_picker.go` → `palette.go` / `icons.go`.

The window hides its macOS title bar and draws its own rows; the traffic-light strip is one constant trio in `main.go` (`trafficLightButton`, `trafficLightY`, `headerStrip`) shared by `TrafficLightPosition` and the header rows, so the lights and the rows stay on one center line.

Deep MyGo rules (element lifetime, focus identity, threading) live in `.agents/skills/mygo-maintenance/SKILL.md` and in the module's `ui/doc.go`.
