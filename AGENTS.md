# AGENTS.md — wada_desktop_go

A native MyGo desktop AI chat app. Everything is a plugin on a small kernel; all UI
is Go, with Onevra components and theme. PiG is the embedded coding-agent backend.
This is development software with no users: replace internal APIs directly and
remove obsolete paths. Use PiG's pinned public Go SDK; keep its types inside the
backend plugin.

## Source map

- `github.com/xleotranx/wada-kit`: an external, independently released Go module,
  pinned in `go.mod`. Its repository is https://github.com/xleotranx/wada-kit;
  source is in the Go module cache (`go list -m -f '{{.Dir}}'
  github.com/xleotranx/wada-kit`) or a separate checkout. It owns `kernel`,
  `contract/shell`, `contract/settings`, `shell`, `native`, built-in settings and
  inspector plugins, `kernel/kerneltest` and `kittest`. It never imports Wada.
  Do not edit cached dependencies or add a permanent local `replace`. See
  [kit dependency maintenance](docs/design/kit-dependency.md).
- `main.go`: composition only — Wada's `shell.App` and the plugins this build installs.
- `internal/contract/*`: Wada's declarations shared between plugins — types, interfaces,
  keys, topics, pipelines, points. No implementation, no state.
  `agent` (backend seam), `chat` (open conversation, outgoing pipeline, composer
  slots, work panel sections and hiding the panel, drawers, selection actions, file drops, completions), `tabs` (the open tabs, the looks that mark a kind of conversation's tabs, and the
  buttons at the strip's end),
  `workspace` (folders and the conversations the app started),
  `workflow` (conversational workflow definitions and phase instructions),
  `attachments` (images waiting in the composer, their actions and replacement).
- `internal/plugins/*`: every feature. `pig` (embedded backend), `chat` (conversation
  page), `models` (the composer's model and effort), `modes` (the composer's mode, or a workflow for the next message), `usage` (cost and context in the work panel or the composer), `filechanges`
  (session or local uncommitted Git changes and diffs below Context in the work panel), `attachments` (files sent with a message), `mentions`
  (`@file` and `@folder` in the composer), `skills` (`/skill` in the composer), `markup` (screen capture, and numbered boxes,
  arrows, pins, lines and highlights with notes, and hidden areas, on a pending screenshot), `design` (design mode for
  conversations: a canvas beside a design whose artboards are the HTML files the agent writes, with comments, notes, tweaks,
  presenting and a brief that tells the agent how to write for it), `explore` (read-only side chats in a drawer, started from selected transcript text, whose answers can join the main draft),
  `workspace` (folders and the app's conversation record in
  `workspace.db`, needing no chat), `folderpicker` (Open Folder… and the new chat's folder
  in the composer), `tabs` (open chats and pages as tabs across the title
  strip, one folder's space at a time, with the space switcher and the tab menus), `workflow` (the engine
  that runs tasks in the main conversation with independent reviewer children), `flows` (the app's flows,
  such as plan → code → review), `palette` (All chats, ⌘K), `notifications`
  (desktop notices for background conversations), `replay` (synthetic agent events
  for measurements; only builds tagged `wadareplay` install it, through `replay.go`).
- `internal/plugintest`: fake agent (scriptable with `OnSend`, `Emit`, `Respond`),
  chat controller, workspace, files and skills for plugin tests; the host fake is `kittest.Host`.

Design notes: [kernel API](https://github.com/xleotranx/wada-kit/blob/v0.1.1/docs/kernel-api.md), [kit shell](https://github.com/xleotranx/wada-kit/blob/v0.1.1/docs/shell.md),
[agent contract](docs/design/agent-contract.md),
[shell and built-in plugins](docs/design/shell-and-plugins.md). Keep them in step with the code.

Before nontrivial UI work, read [.agents/skills/mygo-maintenance/SKILL.md](.agents/skills/mygo-maintenance/SKILL.md).
Check APIs against the pinned module with `go list -m -f '{{.Dir}}' github.com/egoist/mygo`,
particularly `ui/doc.go`.

## Rules

1. **Add features as plugins.** A plugin implements `kernel.Plugin`, declares what it
   `Provides` and `Requires` in its manifest, and registers everything in `Setup`
   through its `*kernel.Context`. Its package's `New` is the factory `main.go`
   installs; the kernel makes a new plugin for each activation, so `Setup` starts
   from zero values and never resets fields. It never imports another plugin; shared names go
   in a contract package. Built-in plugins get no private access to the shell.
2. **Register, don't wire.** Use `Contribute` for commands, slots and sections,
   `On`/`Emit` for events, `Transform`/`Run` for pipelines, `Provide`/`Require`/`Use`
   for services. Everything a plugin registers is undone when it's disposed.
3. **Threading.** Plugin state belongs to the UI thread. Background work goes through
   `kernel.Go` (replaceable, stale results dropped) or `Scope.Spawn`, never a `go`
   statement (a guard test enforces it); workers return
   values or `Post` closures and never touch plugin state. Tie navigation-dependent
   work to a child `Scope` and dispose it when navigation changes.
4. **Rendering performs no I/O and no kernel mutation.** Do changes in `OnClick` and
   other handlers. The shell renders contributions through `kernel.Invoke`, so a
   panic fails only that plugin.
5. **Backends never retry mutations.** A failed create/send/reply/rename/delete has an
   unknown outcome; report it and let the user decide.
6. **Every color comes from `native.PaletteFor`.** A guard test enforces it.
7. **Keep MyGo identity stable**: keys before stateful widgets, `ItemKey` on lists,
   persistent `ListState`/handles in plugin state.
8. **Docs describe the code as it is.** No history, plans or examples of APIs that
   don't exist. Update the design note in the same change as the code; delete a
   doc rather than leave it stale.

Depguard enforces the import boundaries; `exhaustive` covers enum switches.

## Commands and native checks

- `go tool mygo dev .`: live reload; q + Enter or Ctrl+C quits, r rebuilds.
- `go run .`: run the app.
- `GOWORK=off make check`: MyGo lifetime vet, pinned lint, license inventory, race
  tests and no-cgo tests for Wada; also CI. The kit runs its own gate.
- `make licenses`: refresh third-party notices after dependency or Go changes.
- `make audit` and `make secrets`: vulnerability and committed-history secret scans.
  See [source publication](docs/source-publication.md) before changing visibility.
- `go run ./examples/notes` in a separate kit checkout: its reference app.
- `go test ./...`; focused: `go test -run TestFirstMessage ./internal/plugins/chat`.
- `go test -run NONE -bench . -benchtime 30x .`: draw and time whole-app
  frames on `ui.Tester` (see docs/performance.md for the readings and the
  real-window recipe).
- `go tool mygo build`: production app/DMG in gitignored `build/`.
- `make demo`: the app on throwaway data from `scripts/seed.py` (many folders, chats,
  long conversations and open tabs), loaded by embedded PiG from a temporary folder.
  `seed.py --heavy` adds change baselines, images, large tool outputs and a 200 MB project.
- `python3 -I scripts/measure.py [--heavy] text|working|background`: CPU, frames and
  footprint of the real window under a replayed workload (docs/performance.md).

`WADA_CAPTURE=/path/window.png go run .` captures the window and exits
(`WADA_CAPTURE_WAIT=12s` waits longer, e.g. for the model catalog);
`WADA_APPEARANCE=system|light|dark` fixes the palette without saving it;
`WADA_STRICT=1` re-panics plugin failures. Always use a temporary `WADA_DATA_DIR`
for captures and experiments; never reset developer data during verification.

Settings live in `settings.db` (schema 1) under `$WADA_DATA_DIR`, or else
`~/Library/Application Support/wada-desktop-go/` for a `mygo build` app and
`wada-desktop-go-dev/` beside it for `go run` and `mygo dev`; the folders and the record of the
conversations the app started live beside it in `workspace.db` (schema 1). Workflow tasks, queued inputs and review reports live in
`workflow.db` (schema 1). A
database of another schema is refused, not migrated; delete it explicitly to start
over. Conversations' messages belong to PiG's JSONL transcripts in `pig/sessions/`;
Wada's session metadata is in `pig/metadata/`, and provider credentials and
configuration in `pig/agent/`.

Window frame restoration uses `StateKey: "main"`. F12 / Alt+Cmd+I opens the
inspector; Shift+Cmd+C picks an element.
