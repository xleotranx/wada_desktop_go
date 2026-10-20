# AGENTS.md — wada_desktop_go

A native MyGo desktop chat client backed by OpenCode. All UI is Go; no webview.
MyGo and its CLI are pinned in `go.mod`; Onevra (`github.com/oneo-me/onevra/source`)
is the component and theme layer built on MyGo. This is development software: replace
internal APIs directly and remove obsolete paths rather than keeping compatibility
layers. Do not change the external server protocol to simplify a refactor.

## Source map

- `main.go`: bootstrap, native window, theme installation, application queries/actions.
- `commands.go`: the Cmd+K command palette over actions, servers, projects and chats.
- `state.go`: eight named UI-thread state owners: runtime, connections,
  conversation, workspace, preferences, search, layout and terminal.
- `conversation.go`, `workspace.go`: complete navigation and workspace transitions.
- `sidebar.go`, `chat.go`, `composer.go`, `browser.go`, `settings.go`,
  `terminal.go`, `tabs.go`, `activity.go`: native views.
- `runner_sink.go`: the runner's typed results applied on the UI thread;
  `agent_jobs.go`: typed command admission; `agent_apply.go`: decoded events and
  stored messages adapted to chat data.
- `internal/chat`: pure transcript model, streams, tool rows, approvals and usage.
  Read rows by value. Mutable indexes and stream builders never leave the model.
- `internal/opencode`: HTTP/SSE client, wire decoding, connection credentials and
  managed process lifetime. No MyGo or application-state dependency.
- `internal/runner`: command queue, connection attempts, independent switch/stop
  control, retries and bounded event batches. No native UI dependency.
- `servers.go`, `server_folders.go`, `files.go`, `changes.go`, `policy.go`,
  `search.go`, `background.go`, `attach.go`, `dialog_lifetime.go`: operations
  and lifetime ownership.
- `metadata.go`: transactional SQLite settings and asynchronous snapshots.
- `unread.go`: background chat and approval marks, the bell and their navigation.
- `notifications.go`: the desktop banners for background turns and approvals,
  the preference gates and the click that opens their chat.
- `bell.go`: the notification center under the bell, its grouped rows and
  mark-all-read.
- `queue.go`: prompts waiting behind the runner's one active turn.
- `drafts.go`: each chat's unsent composer words and their metadata.
- `archive.go`: chats put away from the pinned and project lists, their
  filter, counts and persistence.
- `chattext.go`: the saved conversation text size and its shortcuts.
- `quickopen.go`: the centered file picker and its search.
- `slash.go`: local composer commands, suggestions and their help sheet.
- `mentions.go`: the composer’s @-file menu, its workspace search and insertion.
- `chatfilter.go`: the sidebar filters for unread, attention and drafts.
- `project_links.go`, `project_picker.go`: local projects that group several
  folders and their chats, and the new-chat picker that offers each folder.
- `commandscope.go`: the command palette’s command/chat scopes and prefixes.
- `recentfiles.go`: the quick picker’s recently opened files per workspace.
- `keymap.go`: remappable window shortcuts, their recorder and persistence.
- `appearance.go`: the saved System/Light/Dark palette and MyGo theme source.
- `palette.go`, `icons.go`: the app's semantic colors read from the Onevra theme,
  and Lucide icon accessors.

Tests sit beside their subjects. UI interaction tests use real `ui.NewTester`
views; `newTestApp` explicitly installs immediate effects. Ordering tests replace
that executor with `manualBackground`, which separates work from publication.
See `async_regression_test.go`, `conversation_test.go`, `policy_ownership_test.go`,
`internal/chat/state_test.go`, and `internal/runner/runner_test.go` for patterns.

## Commands

- `go tool mygo dev .`: live reload; `q` + Enter or Ctrl+C quits, `r` rebuilds.
- `go run .`: plain native instance.
- `make check`: MyGo lifetime vet, pinned golangci-lint, race tests, no-cgo tests.
  CI runs this same gate on pushes and pull requests.
- `go test ./...`: all tests. Focus a test with `go test -run TestChooseChat .`.
  There is no `go tool mygo test` command.
- `go tool mygo build`: production app/DMG in gitignored `build/`.
- `go test -run '^$' -bench 'Benchmark(Transcript|Sidebar|Stream)$' -benchmem -count=3 .`:
  warm headless layout/painting and streaming allocation comparisons.

Use the pinned `go tool mygo`; no Bun/npm or globally installed MyGo.

## Ownership and verification

1. Application and widget state belong to the UI thread. Workers capture immutable
   requests and publish through the configured dispatcher. Cancel obsolete work
   and validate the result's generation, attempt, and request revision; matching
   server IDs, query text or paths alone does not establish freshness.
2. Background work uses `requestState` and `runRequest`; inactive-server registry
   work uses its own configuration/operation owner and application lifetime.
   Rendering reads state and does not initiate external I/O.
3. Admit typed runner commands before changing optimistic UI state. Busy, closed,
   and invalid submissions remain visible. Sent mutations with uncertain outcomes
   are not replayed automatically.
4. Navigate through `openConversation`, `startDraft`/`resetDraft`, `useServer`, and
   `workspaceChanged`. Server scopes own session/model caches and pins. Model
   choices go through `offerModels`; avoid a second filtering implementation.
5. Keep MyGo list, editor, markdown and handle state stable across frames. Use
   MyGo markdown for transcripts and read-only editor/diff buffers for Files and
   Changes. Parse patches on selection changes, with readable raw fallbacks.
6. Every color comes from `paletteFor`. Import restrictions, enum exhaustiveness,
   MyGo lifetime vet, palette guards and behavioral tests enforce the mechanical
   rules. Add a regression for a new failure mode instead of another prose rule.

Before nontrivial UI work, read [.agents/skills/mygo-maintenance/SKILL.md](.agents/skills/mygo-maintenance/SKILL.md).
Check APIs against the pinned module (`go list -m -f '{{.Dir}}' github.com/egoist/mygo`),
particularly `ui/doc.go`. Preserve stable keys, bound-value timing and deferred
input actions. Comments are compact prose explaining intent or constraints.

## Native checks and development data

`WADA_CAPTURE=/path/window.png go run .` captures the content and exits.
`WADA_SETTINGS=servers` or `models` opens settings before capture,
`WADA_APPEARANCE=(system|light|dark)` fixes the palette,
`WADA_TERMINAL=N` opens the bottom panel with N shells, and
`WADA_TERMINAL_MAX=1` maximizes it. Use
`WADA_DATA_DIR=/temporary/directory` to isolate settings for captures and experiments.
Tests use temporary stores. Never reset the developer's data as part of verification.

Metadata has one current schema (5), with no historical migrations. An unsupported
store reports `go run . --reset-metadata`; that explicit command discards only this
app's settings database and SQLite sidecars, never OpenCode conversations.

The window restores its frame through `StateKey: "main"`. Capture output excludes
native traffic lights. `trafficLightButton`, `trafficLightY`, and `headerStrip`
in `main.go` keep the custom rows aligned with the native title-bar strip. Use
F12 / Alt+Cmd+I for the inspector and Shift+Cmd+C to pick an element when exact
committed boxes or focus identity need inspection.
