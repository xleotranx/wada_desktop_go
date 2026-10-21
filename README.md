# wada_desktop_go

A native macOS AI chat app built with Go, MyGo and Onevra. OpenCode is the current
agent backend. Coding tools and the terminal are optional compiled Go modules.
There is no webview.

## Run

Requires macOS, the Go version in `go.mod`, and the OpenCode v2 CLI on `PATH` for
managed local connections. MyGo and its CLI are pinned by this repository.

```sh
go tool mygo dev .       # live reload; q quits, r rebuilds
go run .                 # chat, coding tools and terminal
go run . --chat-only     # chat without coding or terminal modules
go tool mygo build       # production app/DMG in build/
```

The app manages a local backend process or connects to a configured server.
Startup does not rewrite global OpenCode configuration. New conversations use
the backend's default model unless the user selects another model.

## Features

Core chat includes searchable/paged conversations, streaming markdown, drafts,
model selection, expandable tool activity, approvals, stop, copy/export, pins,
archive, unread marks, notifications, appearance and keyboard settings.

The coding module supplies Files, file search and quick-open, recent files,
`@file` suggestions, workspace branch display, Changes with diff review, and
workspace permission controls. Optional folder navigation groups chats by their
execution context. The terminal module owns its shells, tabs, naming and panel
layout. Disabling these modules removes their commands, shortcuts, menus, slash
commands, keyboard reference entries and composer controls. The retired page-preview
stub is removed.

## Architecture

| Package | Responsibility |
| --- | --- |
| `main.go` | Select backend and install feature factories |
| `internal/chat` | Pure transcript, turn, streaming and approval transitions |
| `internal/agent` | Backend-neutral messages, events, catalog and client contract |
| `internal/runner` | Admission, cancellation, reconnect attempts and bounded event batches |
| `internal/opencode` | OpenCode wire formats, HTTP/SSE, process, pairing and permission configuration |
| `internal/desktop` | Native chat shell, conversation/context navigation and application preferences |
| `internal/feature` | Native module contributions, host actions and request ownership |
| `internal/features/coding` | File/diff/mention/policy state, operations and native views |
| `internal/features/terminal` | Shell lifetime, tabs and native panel |
| `internal/workspace` | Optional filesystem, VCS and policy capability contracts |
| `internal/native` | Shared palette and small native controls |

The shell and runner do not import OpenCode or concrete feature modules. Import
rules enforce this. The backend normalizes stored messages and events before
publishing them. Backend generation, connection attempt, navigation and request
revisions reject obsolete results; uncertain mutations are not replayed.

See [the architecture decision](docs/decisions/0001-modular-chat.md) for the
extension and lifecycle contracts, and [AGENTS.md](AGENTS.md) for the source map.

## Extend

A feature factory takes `feature.Host` and returns `feature.Module`. Implement
only the contributions it needs: actions, panels, dialogs, composer UI, a bottom
panel, persistence or lifecycle hooks. Register the factory in `main.go`.
`internal/desktop/modules_test.go` demonstrates an independent notes module that
adds a panel, keyboard action, menu and slash command without changing the shell.

Modules own their widget state and resources. Use `feature.Run` for replaceable
background requests, publish through `Host.Post`, and cancel the module lifetime
in `Close`. `Start` runs explicitly after the native dispatcher is installed.
Action and panel declarations are evaluated once at registration. Module, action,
panel and shortcut IDs must be unique. These are trusted Go
modules installed at startup; runtime loading and unload are not implemented.

A backend implements `agent.Backend` and `agent.Client`. `Backend.Client` constructs
a lightweight service handle without I/O; `Start` establishes the active connection.
Workspace discovery, personas, pairing, filesystem and policy capabilities are
optional. Add a backend adapter and select it in `main.go`; Pi is not implemented.

## Verify

```sh
make check
# Includes MyGo lifetime vet, lint, race tests and a no-cgo test pass.
go test ./...
go test -run TestChooseChat ./internal/desktop
go test -run '^$' -bench 'Benchmark(Transcript|Sidebar|Stream)$' -benchmem -count=3 ./internal/desktop
WADA_LIVE_AGENT=1 go test -run 'TestLiveLocation|TestLiveSessionOps' ./internal/desktop
```

Tests use temporary stores, fake backends and controllable work/publication
ordering. Native interactions use real `ui.NewTester` views. CI runs `make check`.

```sh
WADA_DATA_DIR=/tmp/wada-preview WADA_CAPTURE=/tmp/wada-chat.png go run . --chat-only
WADA_DATA_DIR=/tmp/wada-preview WADA_SETTINGS=servers WADA_CAPTURE=/tmp/wada-settings.png go run .
WADA_DATA_DIR=/tmp/wada-preview WADA_TERMINAL=2 WADA_TERMINAL_MAX=1 WADA_CAPTURE=/tmp/wada-terminal.png go run .
```

`WADA_APPEARANCE=system|light|dark` fixes the capture palette. Captures exclude
native traffic lights. F12 opens the inspector; Shift+Cmd+C picks an element.
Headless benchmarks measure layout/CPU painting, not native GPU or idle CPU.

## Development data

The backend owns conversation contents. Application metadata is stored per backend
at `~/Library/Application Support/wada-desktop-go/<backend-id>/metadata.db`, or
`$WADA_DATA_DIR/<backend-id>/metadata.db`. Module settings use separate namespaces;
disabled modules retain their saved settings. Passwords currently reside in SQLite.

There are no historical format migrations. The previous unscoped development
store is left untouched and is not imported. Unsupported/corrupt stores are never
silently deleted. To explicitly discard the selected backend's app metadata:

```sh
go run . --reset-metadata
```

Reset removes only that app database and its SQLite sidecars, never backend
conversations or workspace files. Verification uses temporary `WADA_DATA_DIR`.
Attachments currently quote text files; image parts and dictation are not implemented.
