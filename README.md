# wada_desktop_go

A native macOS chat client for the ChatGPT desktop look, driven by [OpenCode](https://github.com/sst/opencode) as its agent engine. All UI is Go through [MyGo](https://github.com/egoist/mygo) — no webview, no Electron, one package.

The design-copy era is over: every visible control answers. Conversations, turns, approvals, search, model picking, folders, files and the shell policy are real, backed by an `opencode serve` subprocess the app owns.

## Requirements

- macOS (the UI uses native windows and hidden title bars).
- A Go toolchain; MyGo is pinned in `go.mod` and runs as `go tool mygo`.
- The `opencode` CLI v2 on `PATH`. The app spawns it itself (`opencode serve`) on its own port with a random password; your global `~/.config/opencode` config is untouched.

## Run

```
go tool mygo dev .      # live reload; q quits, r forces a rebuild
go run .                # plain instance
go tool mygo build      # .app / .dmg into build/
```

First run restores yesterday's conversations from OpenCode's own store — nothing is kept in the app.

## Test

```
go test ./...                                   # the gate, single: go test -run TestChooseChat ./...
go tool mygo vet .                              # go vet + native-UI build lifetimes
WADA_LIVE_AGENT=1 go test -run 'TestLiveAgent|TestLiveModel|TestLivePermission|TestLiveLocation|TestLiveSessionOps' .   # real server, ~a minute
```

UI verification without clicking: `WADA_CAPTURE=/path/out.png go run .` writes a PNG of the window and quits; dev builds carry an element inspector on `F12` (`Shift+Cmd+C` picks an element, Computed shows exact boxes).

## What is real

- **Sessions** — list, restore, create, rename, delete, pagination, per-chat working directories. Search is server-side (`GET /session?search=…`) with a stale-result guard.
- **Turns** — stream, stop, approval cards for shell and edit asks, activity rows for tools as they run.
- **Models** — the picker reads the server's catalog, free first, with per-model reasoning-effort chips; the switch folds back over `session.model.selected`. Chats run `opencode-go/longcat-2.5-preview-free` unless `WADA_MODEL=provider/model` says otherwise.
- **Folders** — every chat names its directory; the composer chip opens a typed-path dialog.
- **Files** — the composer's `+` quotes a workspace file into the draft as a fenced block (path through `~` or relative to the chat's folder, 256 KiB cap, de-duplicated).
- **Shell policy** — the pill reads the chat's `opencode.json` for the bash wildcard and the menu writes it back; the running server picks the file up, so the next turn runs under what the pill shows. An unreadable config leans to "Asks first" and refuses to be overwritten.
- **Markdown** — settled answers render: headings, fenced code, lists, bold, italic, inline code, underlined links. In-flight previews stay plain until the turn ends.
- **Resilience** — a dead server ends one attempt, not the session: the pipe respawns with a stretching backoff, half-said answers settle as cut short, and queued jobs replay against the fresh server.

## Honest boundaries

- Links render underlined but do not open yet; there is no file picker (typed paths instead); no dictate and no archive — the first has no surface worth faking, the second no server endpoint.
- The connection's port and password live only in the app's process.

## Layout

`main.go` (state, window, boot) → `sidebar.go` (rows, kebabs, bell, search) → `chat.go` (transcript blocks, title bar) → `composer.go` (draft, model picker, dialogs, pill) → `agent.go` (the engine: HTTP, SSE, runner) → `markdown.go` → `palette.go` / `icons.go`.

The window hides its macOS title bar and draws its own rows; the traffic-light strip geometry is derived so the lights share the row's center — change one side of that pairing in `main.go` and recompute the other.

Deep MyGo rules (element lifetime, focus identity, threading) live in `.agents/skills/mygo-maintenance/SKILL.md` and in the module's `ui/doc.go`.
