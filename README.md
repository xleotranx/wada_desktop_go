# wada_desktop_go

A native macOS chat client. All UI is Go: [MyGo](https://github.com/egoist/mygo)
owns the window and layout, and [Onevra](https://github.com/oneo-me/onevra) supplies
the warm light/dark theme, Lucide icons, controls, dialogs and command palette.
[OpenCode](https://github.com/sst/opencode) provides the agent engine over HTTP and
SSE. There is no webview or Electron.

## Run

Requires macOS, the Go version in `go.mod`, and the OpenCode v2 CLI on `PATH` for
managed local connections. MyGo and its CLI are pinned by the project.

```sh
go tool mygo dev .      # live reload; q quits, r forces a rebuild
go run .                # plain instance
go tool mygo build      # production app and DMG in build/
```

The app can start its own `opencode serve` process or connect to a server configured
in Settings. A managed server gets a generated password when none is configured.
Global OpenCode configuration is not rewritten by startup.

## Capabilities

- Conversations: list, search, paginate, create, restore, rename and delete.
- Turns: streaming markdown, copy and quote actions on each message, stop, tool
  activity, shell/edit approvals and usage.
- Models: server catalogs, per-model enable switches, draft picks and reasoning
  variants. Settings and the composer share one filtering implementation.
- Servers: managed/local and remote connections, probes, pairing links, scoped
  history, pins, catalogs and workspace folders. One connection is active at a time.
- Workspaces: added local projects or server-reported remote projects, a remote
  folder browser, file search/preview, quoting files by path, picker or drop,
  and working-tree/branch diffs.
- Shell policy: read and update the selected workspace's OpenCode configuration,
  locally or through the remote API. Unreadable configuration is not overwritten.
- Navigation: the rail and sidebar, plus a Cmd+K command palette over actions,
  servers, projects and conversations. Opened chats stay in a tab strip that
  comes back with its last active tab on the next launch; Ctrl+Tab and
  Ctrl+Shift+Tab cycle it, and Cmd+Shift+T reopens the last closed tab. Cmd+F
  finds text in the open transcript and Cmd+Option+F searches chats. Toasts
  confirm copies and exports.
- Layout: drag the sidebar, tools or terminal divider, nudge it with the arrow
  keys, or double-click it to restore the default. Cmd+B folds the sidebar,
  Cmd+Shift+J the tools pane, Cmd+Shift+F enters a conversation-only full view
  and restores the panes on exit. The chosen sizes carry across restarts.
- Terminal: a bottom shell starts in the chat's local folder, keeps running
  while folded away, and restarts on demand. Cmd+J or Ctrl+` toggles it.
- Activity: a live list of the turn's tool calls and approvals, newest first,
  with the transcript's folded detail. Cmd+Option+U toggles the view.
- Appearance: System follows the desktop, Light and Dark fix Onevra's warm
  palette across the chrome and every surface. The choice is saved.

The transcript uses MyGo markdown with Onevra's message and activity surfaces;
the composer is Onevra's chat composer. Files and Changes use persistent
read-only MyGo editor buffers. Large transcript, sidebar and catalog views are virtualized;
stream events use bounded batches with UI backpressure.

## Architecture

| Location | Responsibility |
| --- | --- |
| Root package | Native views, UI-thread coordination, settings and workspace tools |
| `internal/chat` | Pure transcript transitions, streaming, approvals and usage |
| `internal/opencode` | Protocol decoding, HTTP/SSE and managed process lifetime |
| `internal/runner` | Typed commands, connection attempts, admission and cancellation |

`state.go` groups native state by owner. `conversation.go` and `workspace.go`
coordinate complete transitions. `runner_sink.go` applies typed worker results
through the UI dispatcher; `background.go` owns replaceable workspace requests.
The chat model exposes row values and snapshots without exposing its mutable
stream/index storage.

Generation, attempt and operation revisions reject obsolete replies, including
A -> B -> A navigation. Server switching cancels in-flight I/O independently of
the ordinary queue; stop can interrupt a blocked prompt. Queue rejection remains
visible and preserves the unsent draft. Requests already sent with uncertain
outcomes are not blindly replayed after reconnecting.

Import guards keep the chat model free of UI/protocol dependencies and keep the
transport/runner independent of native UI. See [AGENTS.md](AGENTS.md) for the
source map and the few framework constraints that still require judgment.

## Verification

```sh
make check       # MyGo lifetime vet, lint, race tests, no-cgo tests
go test ./...    # focused: go test -run TestChooseChat .
go test -run '^$' -bench 'Benchmark(Transcript|Sidebar|Stream)$' -benchmem -count=3 .
```

CI runs the same gate on pushes and pull requests. Deterministic tests use fake
clients and controlled worker/publication ordering; view tests exercise real
MyGo views. Optional integration checks against the installed CLI:

```sh
WADA_LIVE_AGENT=1 go test -run 'TestLiveLocation|TestLiveSessionOps' .
```

Capture the native window without clicking:

```sh
WADA_DATA_DIR=/tmp/wada-preview WADA_CAPTURE=/tmp/wada-chat.png go run .
WADA_DATA_DIR=/tmp/wada-preview WADA_SETTINGS=servers WADA_CAPTURE=/tmp/wada-settings.png go run .
WADA_DATA_DIR=/tmp/wada-preview WADA_TERMINAL=1 WADA_CAPTURE=/tmp/wada-terminal.png go run .
WADA_DATA_DIR=/tmp/wada-preview WADA_APPEARANCE=dark WADA_SETTINGS=appearance WADA_CAPTURE=/tmp/wada-dark.png go run .
```

Capture output excludes macOS traffic lights. Dev builds include the F12 inspector;
Shift+Cmd+C picks an element. Headless benchmarks measure layout and CPU painting,
not GPU presentation or idle CPU. Use `MYGO_FRAME_STATS=all go run .` for native
frame statistics and count the managed OpenCode process separately for resource use.

## Development data

OpenCode owns conversation contents. This app stores connections, passwords,
per-server chat pins, disabled models and added local projects in `metadata.db`
under the OS configuration directory (`~/Library/Application Support/wada-desktop-go`
on macOS). Dev and packaged builds share this path unless `WADA_DATA_DIR` overrides it.
Passwords currently reside in SQLite, not the macOS Keychain.

There are no users or supported historical settings formats. The current schema
is created directly; older or corrupt stores are never silently migrated or deleted.
To explicitly discard this app's development settings, close running instances and run:

```sh
go run . --reset-metadata
```

This removes only the app metadata database and its SQLite sidecars. It does not
remove OpenCode conversations or workspace files. With `WADA_DATA_DIR` set, reset
is scoped to that directory.

Links render but do not open yet. Attachments quote text files by typed path,
native picker or drag-and-drop; image parts, dictation and conversation archival
are not implemented.
