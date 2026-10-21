# AGENTS.md — wada_desktop_go

A native MyGo desktop AI chat app. Everything is a plugin on a small kernel; all UI
is Go, with Onevra components and theme. OpenCode is the current backend; Pi is
expected as a second one. This is development software with no users: replace
internal APIs directly and remove obsolete paths. Don't change OpenCode's external
protocol to simplify a refactor.

## Source map

- `main.go`: composition only — the list of plugins this build installs.
- `internal/kernel`: plugin lifecycle, scoped/reversible registration, services
  (`Key`), events (`Topic`), pipelines, extension points (`Point`), commands, tasks
  and settings. Standard library only. `kerneltest` runs it in tests.
- `internal/contract/*`: declarations shared between plugins — types, interfaces,
  keys, topics, pipelines, points. No implementation, no state.
  `agent` (backend seam), `chat` (open conversation, outgoing pipeline, composer
  slot), `shell` (window slots, host service), `settings` (settings sections).
- `internal/shell`: the window host — kernel, SQLite settings store, frame/layout,
  menu bar and shortcuts from commands. Knows no feature.
- `internal/plugins/*`: every feature. `opencode` (backend), `chat` (conversation
  page), `models` (composer pickers), `sessions` (sidebar list), `settings`
  (settings page).
- `internal/plugintest`: fake agent, host and chat controller for plugin tests.
- `internal/native`: the semantic palette and small shared controls.

Design notes: [kernel API](docs/design/kernel-api.md),
[agent contract](docs/design/agent-contract.md),
[shell and chat](docs/design/shell-and-chat.md). Keep them in step with the code.

Before nontrivial UI work, read [.agents/skills/mygo-maintenance/SKILL.md](.agents/skills/mygo-maintenance/SKILL.md).
Check APIs against the pinned module with `go list -m -f '{{.Dir}}' github.com/egoist/mygo`,
particularly `ui/doc.go`.

## Rules

1. **Add features as plugins.** A plugin implements `kernel.Plugin`, declares what it
   `Provides` and `Requires` in its manifest, and registers everything in `Setup`
   through its `*kernel.Context`. It never imports another plugin; shared names go
   in a contract package. Built-in plugins get no private access to the shell.
2. **Register, don't wire.** Use `Contribute` for commands, slots and sections,
   `On`/`Emit` for events, `Transform`/`Run` for pipelines, `Provide`/`Require`/`Use`
   for services. Everything a plugin registers is undone when it's disposed.
3. **Threading.** Plugin state belongs to the UI thread. Background work goes through
   `kernel.Go` (replaceable, stale results dropped) or `Scope.Spawn`; workers return
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

Depguard enforces the import boundaries; `exhaustive` covers enum switches.

## Commands and native checks

- `go tool mygo dev .`: live reload; q + Enter or Ctrl+C quits, r rebuilds.
- `go run .`: run the app.
- `make check`: MyGo lifetime vet, pinned lint, race tests, no-cgo tests; also CI.
- `go test ./...`; focused: `go test -run TestFirstMessage ./internal/plugins/chat`.
- `go tool mygo build`: production app/DMG in gitignored `build/`.

`WADA_CAPTURE=/path/window.png go run .` captures the window and exits
(`WADA_CAPTURE_WAIT=12s` waits longer, e.g. for the model catalog);
`WADA_APPEARANCE=system|light|dark` fixes the palette without saving it;
`WADA_STRICT=1` re-panics plugin failures. Always use a temporary `WADA_DATA_DIR`
for captures and experiments; never reset developer data during verification.

Settings live in `settings.db` (schema 1) under `$WADA_DATA_DIR` or
`~/Library/Application Support/wada-desktop-go/`. A database of another schema is
refused, not migrated; delete it explicitly to start over. Conversations belong to
the backend.

Window frame restoration uses `StateKey: "main"`. F12 / Alt+Cmd+I opens the
inspector; Shift+Cmd+C picks an element.
