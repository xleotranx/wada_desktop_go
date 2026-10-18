# AGENTS.md — wada_desktop_go

A single-package MyGo desktop app: all native UI in Go (`ui`), no webview — a design mock of the ChatGPT desktop client. MyGo is pinned in `go.mod`; docs: https://github.com/egoist/mygo/blob/main/docs/README.md

Layout: one package. `main.go` — app state, window options and `view()`; `sidebar.go`, `chat.go`, `composer.go`, `browser.go` — the panes; `settings.go` — the settings page (the server manager and the model switches) and `servers.go` — server connections, their health and the connection probe; `agent.go` — the `opencode serve` client (HTTP, SSE, job loop) with `agent_events.go` — bounded event batches and `agent_apply.go` — events and stored turns folded into the transcript; `paths.go` — workspace path helpers; `search.go`, `policy.go` (the workspace config's permission read/write), `metadata.go` (the app's SQLite store: sidebar and settings), `pinned.go`, `project_picker.go` — sidebar support; `server_folders.go` — the folder browser a remote server's chats are picked in; `palette.go` — every color; `icons.go` — SVGs.

Tests sit beside their subjects: `main_test.go` and `chat_test.go` (the view and the transcript), `agent_test.go` (the pipe), `settings_test.go` (the settings page and the servers), `server_folders_test.go` (the server folder browse), `projects_test.go`, `metadata_test.go`, `pinned_test.go`, and `performance_test.go` (frame cost).

## Commands

This is a `mygo init -template native` project: the CLI is pinned as a Go tool in `go.mod`, so run it as `go tool mygo <cmd>` at that pinned version — no Bun/npm, no installed `mygo`. Of the CLI's commands only `dev`, `vet`, `build` and `doctor` matter here; `generate`, `i18n` and `keygen` serve web frontends and update channels this app doesn't use (no bindings, no catalogs).

- `go tool mygo dev .` — run the app with live reload: any `.go` edit rebuilds and relaunches, and a build that fails to compile keeps the running one; `q` + Enter or Ctrl+C quits, `r` + Enter forces a rebuild. Dev builds live in gitignored `.mygo/dev/` and appear as the `wada_desktop_go Dev` process; killing the `go tool mygo` process stops the app as well.
- `go run .` — plain instance, no live reload.
- `make check` — the gate: `make vet` (`go tool mygo vet .`: Go vet **and** native-UI build lifetimes), `make lint` (golangci-lint, pinned in the `Makefile` and run through `go run`; its small probationary set lives in `.golangci.yml`), `make test` (`go test -race ./...`) and `make test-nocgo` (`CGO_ENABLED=0 go test ./...`). CI (`.github/workflows/check.yml`) runs the same via `make check` but is parked: manual dispatch only until it is turned on.
- `go test ./...` — plain tests; single test: `go test -run TestChooseChat ./...`. There is no `test` subcommand under `go tool mygo` — use `go test`.
- `go tool mygo build` — production `.app`/`.dmg` into `build/` (gitignored; the `wada-desktop-go` binary at the root is just a quick `go build -o` artifact).

## Verifying UI changes

- `WADA_CAPTURE=/path/out.png go run .` — writes a PNG of the window content and quits. It shows only what the view draws: macOS traffic lights are native chrome and never appear. Iterate layout with this instead of clicking around. `WADA_SETTINGS=servers` (or `models`, `1`) opens the settings page first and waits a little longer for the catalog.
- `StateKey: "main"` restores the window's position/size from the last run — don't assume it is centered when screenshotting live; ask System Events / `screencapture -R x,y,w,h` for the real frame.
- View tests run headless against the real view: `ui.NewTester(a.view, 1400, 920)` (see `main_test.go`). Prefer adding behaviors there over manual repro.
- Dev builds include the native-UI inspector: `F12` or `Alt+Cmd+I` opens the element tree, `Shift+Cmd+C` picks an element in the window. Its Computed tab gives exact element boxes (size and position), which beats pixel-measuring screen captures for alignment work.

## Custom title bar (macOS gotcha)

- The window is `TitleBarHidden` with `Vibrancy: VibrancyTitlebar`; the view draws its own top rows on the window's material. On macOS, `c.TitleBar()` measures the strip the traffic lights live in: `Height = traffic-light button height + 2 × TrafficLightPosition.Y`, controls are vertically centered in that strip, and `Left` is the width they occupy (the full-width top bar's left padding).
- `main.go` keeps the pairing in one constant trio: `trafficLightButton` (14), `trafficLightY` (18) and `headerStrip` (= button + 2×Y, 50 — what `c.TitleBar()` reports on macOS). `TrafficLightPosition.Y` reads `trafficLightY`, and the top bar (browser history, the sidebar toggle, the conversation's title and the right tools toggle), the sidebar's header row and the tools toolbar floor their height at `headerStrip`, so the dots share the rows' center line. Change `trafficLightY` and everything follows; nothing to recompute by hand.
- `topBar` anchors the title where the conversation starts — past the rail and sidebar — and the tools toggle expands or folds the right pane; both edges re-anchor when the sidebar folds away. The title is the open conversation's name, which the sidebar also lists, so one chat's name can stand more than once in a frame: tests that find rows by label count occurrences (`countTexts` in `projects_test.go`) rather than assume one. While the settings page is up it reads "Settings".
- The material shows wherever the view paints no background: the top bar, the rail and the strip around the content card. The root washes it with the palette's `chromeWash` (a white veil in the light appearance, a black one in the dark), so the chrome reads like the app's own surfaces over any wallpaper; `c.Vibrancy()` gates both that wash and the rail's fallback background, so the window stays opaque off macOS.

## Conventions

- Route all colors through `paletteFor` (light/dark palettes in `palette.go`); `palette_test.go` fails any `ui.Hex` or `ui.RGBA` outside `palette.go`, so the rule enforces itself.
- `block.kind` (the `kind*` constants) and `agentJob.kind` (the `job*` constants) are closed sets; a new block kind joins `blockRow`'s switch and the table in `chat_test.go` together.
- The transcript's markdown is MyGo's `ui/markdown`: one `markdown.Options` on the app that `prepareMarkdown` retunes to the palette each frame, `markdown.Stream` while a block lands and `markdown.View` once it settles. Don't hand-roll markdown parsing.
- Icons are single-color SVGs recolored by the text color; `ui.Icon(...).FontSize(n)` is the icon size. Multicolor art goes through `ui.Image`, not `ui.Icon`.
- The server connections, the per-server pins and the models the user turned off live in `metadata.db` (schema v4: `servers` — with the pairing a token keeps — `disabled_models`, `session_pins` keyed by server). Every list of models a user chooses from goes through `offerModels` (`filteredModels` for the composer's picker), so the settings' switches and the picker cannot drift apart. Each server's chats, catalog and the folders it serves sit in `main.go`'s `serverScope` under `a.scopes`, keyed by server id and read through `a.scope()`; a switch keeps the scopes it leaves and opens the next server with a fresh one, its dial refreshing the list. The picker and the Projects section read that scope for a remote server and the user's added folders (`projectDirectories`, `localActive`) for the one the app runs itself: a remote draft's "Browse the server…" and the sidebar's Add folder walk the server's own disk over `/fs/list`, one location per browse so a look around files no project row, while the local server keeps the system's folder panel. The sidebar header's server pill (`sidebar.go`) lists every connection in one menu with the active one disabled; picking one calls `useServer`, the same road Settings' Use action takes. The pipe dials one server at a time and every runner write checks `a.activeServer` first (the apply path is bound per attempt by the pipe's `applyForServer`), so a switched-away server stays quiet.
- The transcript in `chat.go` — including fake shell output like `go tool mygo test ./...` — is demo copy, not toolchain documentation.
- Comments are compact prose sentences; match that voice.

## Where the deep MyGo rules live

- Repo-local skill `.agents/skills/mygo-maintenance/SKILL.md` — element lifetime, focus/handle identity, threading and migration pitfalls; its closing section maps which of its rules `make check` already enforces. Read it before nontrivial `ui` work.
- Module source: `go list -m -f '{{.Dir}}' github.com/egoist/mygo` (read `ui/doc.go`); `docs/ui/markdown.md` covers the transcript's markdown package.
