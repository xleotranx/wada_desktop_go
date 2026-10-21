# AGENTS.md — wada_desktop_go

A native MyGo desktop AI chat app. All UI is Go; Onevra supplies components and
theme. OpenCode is the current backend. This is development software: replace
internal APIs directly and remove obsolete paths. Do not change the external
server protocol to simplify a refactor.

## Source map

- `main.go`: composition only. Select `agent.Backend` and compiled feature factories;
  `--chat-only` installs neither coding nor terminal.
- `internal/chat`: pure transcript, stream, tool, turn, approval and usage state.
  Read rows by value; mutable indexes/builders never leave the model.
- `internal/agent`: app-owned backend types and decoded events. No native UI or
  concrete backend dependency. Optional capabilities are separate interfaces.
- `internal/runner`: typed admission, command execution, connection attempts,
  cancellation/retries and bounded event batches. No concrete backend/native UI.
- `internal/opencode`: HTTP/SSE, wire conversion, credentials, managed process,
  pairing and permission-file interpretation. No desktop or feature dependency.
- `internal/desktop/main.go`, `state.go`, `conversation.go`, `workspace.go`:
  native bootstrap, UI owners, complete conversation/context transitions.
- `internal/desktop/runner_sink.go`, `agent_apply.go`, `agent_jobs.go`:
  apply normalized results/events and admit commands before optimistic state.
- `internal/desktop/{sidebar,chat,composer,settings,browser}.go`: native views;
  `browser.go` is the generic tool-panel host, with no page-preview feature.
- `internal/desktop/features.go`: module registration, host callbacks, cached
  optional backend services and namespaced persistence. No concrete module import.
- `internal/desktop/metadata.go`: transactional SQLite app settings and snapshots.
  Conversation storage stays with the backend. Store paths include backend ID.
- `internal/desktop/{background,dialog_lifetime,servers,server_folders,search}.go`:
  app request/configuration owners. Folder navigation is an optional conversation
  context selector; coding tool implementation lives in its module.
- Other desktop files own chat organization, drafts, attention, notifications,
  queue display, menus, shortcuts and appearance. Tests sit beside these subjects.
- `internal/feature`: host/contribution interfaces and module request ownership.
- `internal/features/coding`: Files, Changes/review, quick-open/recent files,
  mentions, branch decoration and policy UI/state. Owns its workers/lifetime.
- `internal/features/terminal`: shells, tabs, dialogs, layout and process cleanup.
- `internal/workspace`: optional file, folder, VCS and policy contracts.
- `internal/native`: semantic palette and small shared controls.

Before nontrivial UI work, read [.agents/skills/mygo-maintenance/SKILL.md](.agents/skills/mygo-maintenance/SKILL.md).
Check APIs against the pinned module with
`go list -m -f '{{.Dir}}' github.com/egoist/mygo`, particularly `ui/doc.go`.
See [the architecture decision](docs/decisions/0001-modular-chat.md) for extension
and lifecycle contracts. No new dependency is needed to add a native module.

## Ownership

1. Application and widget state belong to the UI thread. Workers capture immutable
   requests and publish through the configured dispatcher. Cancel obsolete work;
   validate generation, attempt and request/navigation revision, including A-B-A.
2. Desktop work uses `requestState`/`runRequest`; inactive-server work uses its
   configuration owner and app lifetime. Modules use `feature.Request`/`feature.Run`
   and a child module lifetime. Rendering performs no external I/O.
3. Install modules at composition, start them explicitly after dispatcher setup,
   and cancel/dispose in `Close`. IDs are unique. Modules request host actions;
   they cannot access desktop private state. Persist their own namespaced settings.
4. Admit runner commands before optimistic UI changes. Busy/closed/invalid remain
   visible. Sent mutations with uncertain outcomes are not replayed automatically.
5. Navigate through `openConversation`, `startDraft`/`resetDraft`, `useServer`,
   and `workspaceChanged`. Keep one model filtering path through `offerModels`.
6. Keep MyGo list/editor/markdown/handle identity stable. Files/Changes use persistent
   read-only editor/diff buffers; parse patches on selection changes with raw fallbacks.
7. Every color comes from `native.PaletteFor`. Import guards, exhaustive switches,
   MyGo lifetime vet, palette guards and behavioral tests enforce these boundaries.

`internal/desktop/background_test.go` installs immediate test effects explicitly.
Ordering tests use `manualBackground`; do not introduce window-nil production
execution fallbacks. Examples: `async_regression_test.go`, `conversation_test.go`,
`modules_test.go`, module lifetime tests and runner attempt tests.

## Commands and native checks

- `go tool mygo dev .`: pinned live reload; q + Enter or Ctrl+C quits, r rebuilds.
- `go run .`: default modules; `go run . --chat-only`: independent chat composition.
- `make check`: MyGo lifetime vet, pinned lint, race tests, no-cgo tests; also CI.
- `go test ./...`; focused: `go test -run TestChooseChat ./internal/desktop`.
  There is no `go tool mygo test` command.
- `go tool mygo build`: production app/DMG in gitignored `build/`.
- `go test -run '^$' -bench 'Benchmark(Transcript|Sidebar|Stream)$' -benchmem -count=3 ./internal/desktop`.

`WADA_CAPTURE=/path/window.png go run .` captures content and exits. Settings can
open first with `WADA_SETTINGS=servers|models`; fix palette with
`WADA_APPEARANCE=system|light|dark`. The terminal module supports `WADA_TERMINAL=N`
and `WADA_TERMINAL_MAX=1` during captures. Use a temporary `WADA_DATA_DIR` for all
captures/experiments. Never reset developer data during verification.

Metadata schema is 5, created directly. `go run . --reset-metadata` explicitly
discards only the selected backend's app database/SQLite sidecars. No historical
migration or compatibility layer exists; old unscoped stores remain untouched.

Window frame restoration uses `StateKey: "main"`. Capture excludes traffic lights;
`trafficLightButton`, `trafficLightY` and `headerStrip` in desktop/main.go align the
native title strip. F12 / Alt+Cmd+I opens the inspector; Shift+Cmd+C picks an element.
