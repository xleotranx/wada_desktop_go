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
  variants. Settings and the composer share one filtering implementation. The
  composer's picker opens on the chat's model, takes a filter as it opens, and
  walks the catalog with the arrows and Enter as well as the mouse.
- Servers: managed/local and remote connections, probes, pairing links, scoped
  history, pins, catalogs and workspace folders. One connection is active at a time.
- Workspaces: added local projects or server-reported remote projects, a remote
  folder browser, file search/preview, quoting files by path, picker or drop,
  and working-tree/branch diffs. Local projects can group several folders:
  Add folder… in a project’s menu nests their chats under one row, each linked
  chat tagged with its folder, and the new-chat project picker lists those
  folders under the project, starting the chat where the chosen folder stands.
  The picker opens on the draft’s folder; typing filters, the arrows choose and
  Enter keeps the chat there. The draft page itself offers the first projects
  as chips, so a new chat can start in one without opening the picker. A file preview can copy its path or hand the
  file to the desktop editor or folder. Cmd+O adds a folder, Cmd+P opens the quick
  file picker where arrows choose and Enter opens; its empty query shows recently
  opened files first, then the workspace root, with folders under each name.
  Cmd+Shift+E folds the Files pane.
- Shell policy: read and update the selected workspace's OpenCode configuration,
  locally or through the remote API. Unreadable configuration is not overwritten.
- Navigation: the rail and sidebar, plus a Cmd+K command palette over actions,
  servers, projects and conversations. Its scope rows or `>` / `@` prefixes
  narrow the palette to commands or chats; the actions last run gather in a
  Recent group, five at most, newest first, saved with the settings. Opened chats stay in a tab strip that
  comes back with its last active tab on the next launch; Ctrl+Tab and
  Ctrl+Shift+Tab cycle it, and Cmd+Shift+T reopens the last closed tab. A
  right-click on a tab closes it, the other tabs or the whole strip; all three
  are in the command palette too, and closed tabs stay in the reopen history.
  Dragging a tab onto another moves it to that slot, and the order is saved.
  Cmd+F finds text in the open transcript and Cmd+Option+F searches chats; its
  results take the arrows and Enter, and a lookup still running waits rather
  than opening a stale row. Toasts confirm copies and exports.
- Layout: drag the sidebar, tools or terminal divider, nudge it with the arrow
  keys, or double-click it to restore the default. Cmd+B folds the sidebar,
  Cmd+Shift+J the tools pane, Cmd+Shift+F enters a conversation-only full view
  and restores the panes on exit. The chosen sizes carry across restarts.
- Terminal: a bottom panel of up to six shells, each starting in the chat's
  local folder. Tabs keep running while folded away, each closes from its own
  cross or a right-click (that tab, the others, or all of them), and Cmd+J or
  Ctrl+` toggles the panel. Its header can maximize or restore it.
- Activity: a live list of the turn's tool calls and approvals, newest first,
  with the transcript's folded detail. Cmd+Option+U toggles the view.
- Tools pane: Files, Changes, Activity and page previews stay open together in
  a tab strip, each with its own close action, and a right-click on a tab
  closes it, the others or the strip. Changes draws the selected
  file's patch with its name and counts above it, opens that file in Files
  from the header, and switches between a unified and a split diff; the
  choice is saved. The header's actions menu also hands the file to this
  machine's editor or file manager, or copies its path; those local actions
  stay out of a remote workspace. In Files, Show in tree unfolds the read
  file's folders (clearing a search) and the cross closes the viewer. The
  search's matches take the arrows and Enter: a completed search opens the
  chosen match, while a changed query searches again. A file row dragged into
  the composer writes its path as an @mention; files dropped
  from the desktop are quoted like the Attach picker's. Hovering
  a line and clicking its gutter + opens a review
  note under that line; Add to message (or Enter in the note) writes it into
  the composer as "In <file> at line N: …", so a review reaches the agent
  without leaving the diff. The bar under the patch counts the notes and
  adds them all at once in file and line order.
- Attention: chats that finish or ask for an approval while another chat is on
  show wear unread dots in the sidebar, tab strip and bell. The bell opens a
  notification center listing approvals waiting in this chat, chats needing
  attention and unread chats, each opening on click; Mark all read clears what
  has been seen but keeps the chats still blocked on an approval. Cmd+Shift+U
  marks the open chat unread or read; Cmd+Option+A visits the next marked chat.
  Unread marks are saved across launches.
- Notifications: the General settings tab turns desktop banners for finished
  turns and approvals on or off. A chat off screen, or the open chat while the
  window is in the background, raises one; clicking it opens that chat.
  Banners need a packaged build and the system's permission.
- Queue: a message sent while the agent is working waits for the current turn
  to finish, and the composer names how many messages are queued. Clicking that
  pill lists the waiting messages in order; each runs as the turn before it
  ends.
- Drafts: every chat keeps its unsent composer words when switching tabs, and
  the new-chat draft does too. They survive restarts and clear once sent. A
  pencil marks chats and tabs holding words, and New chat wears a Draft badge.
- Sidebar filters: the title menu and command palette narrow the sidebar to
  unread chats, chats needing attention, chats with drafts or archived chats;
  the list's cross clears the filter.
- Pinned chats: a conversation's menu pins it to the top section; dragging one
  pinned row onto another reorders that section, and the order is saved. Local
  project rows drag the same way inside their pinned group; a cross-group drop
  keeps the line and explains why.
- Archive: a chat's menu puts it away from the pinned and project lists without
  deleting it. Show archived chats (title menu or palette) lists them, and each
  row's menu restores it. Put-away chats drop their pin and leave the unread
  counts, bell and banners until they are restored.
- Slash commands: type `/files`, `/new`, `/terminal`, `/help` and the rest for
  local actions. Typing `/` lists matching commands above the composer; ↑↓
  choose and Enter runs the choice, even from a prefix. Words after a command
  make it an ordinary prompt.
- File mentions: an @-word at the end of the composer opens a menu of the
  chat's files — recent ones first, then a search as the word grows. ↑↓ choose
  and Enter writes the path into the draft; Escape keeps the menu away until
  the word ends.
- Appearance: System follows the desktop, Light and Dark fix Onevra's warm
  palette across the chrome and every surface. Conversation text can be Small,
  Default, Large or Largest, with Cmd+= / Cmd+- / Cmd+0 shortcuts. Both choices
  are saved.
- Menu bar: the native menu carries Settings, New Chat, Open Folder, Close and
  Reopen Chat Tab, Export Transcript, an Edit menu, Chat actions (find, search,
  model, unread), View toggles for the panes, terminal, full view and pane
  sizes, plus Help for the keyboard reference and slash commands. The app's own
  items leave accelerators to the remappable shortcut map.
- Chat menu: the title's kebab renames the chat, copies the transcript to the
  clipboard or exports it as markdown, archives it, or deletes it. The copy
  writes what the export would, and the command palette carries it too.
- Shortcuts: the main window commands — tab cycling, folder opening, conversation
  text steps and more — can be rebound from Settings → Keyboard or the command
  palette. The recorder rejects keys another command or a fixed action already
  uses, and overrides are saved.

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
WADA_DATA_DIR=/tmp/wada-preview WADA_TERMINAL=2 WADA_TERMINAL_MAX=1 WADA_CAPTURE=/tmp/wada-terminal.png go run .
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

Links render but do not open yet. Attachments quote text files by the native
picker or files dropped from the desktop; image parts, dictation and
conversation archival are not implemented.
