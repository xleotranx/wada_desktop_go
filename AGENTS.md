# AGENTS.md — wada_desktop_go

Wada: a native MyGo desktop workspace for coding-agent development beside the chat
that started it, with the agent's work reviewed in the conversation. Everything is a
plugin on a small kernel; all UI is Go, with Onevra components and theme. PiG is
the embedded agent backend. This is development software with no users: replace
internal APIs directly and remove obsolete paths. Use PiG's pinned public Go SDK;
keep its types inside `internal/daemon/pig`.

## Source map

- `github.com/xleotranx/wada-kit`: an external, independently released Go module,
  pinned in `go.mod`. Its repository is https://github.com/xleotranx/wada-kit;
  source is in the Go module cache (`go list -m -f '{{.Dir}}'
  github.com/xleotranx/wada-kit`) or a separate checkout. It owns `kernel`,
  `contract/shell`, `contract/settings`, `shell`, `native`, built-in settings and
  inspector plugins, `kernel/kerneltest` and `kittest`. It never imports Wada.
  Do not edit cached dependencies or add a permanent local `replace`. See
  [kit dependency maintenance](docs/design/kit-dependency.md).
- `main.go`: composition only — Wada's `shell.App`, `--daemon` / `--terminal-worker` subprocess dispatch, and the plugins this build installs.
- `cmd/wadad`, `cmd/wadad-term`, `cmd/wada-relay`, and `cmd/wada`: standalone headless daemon binary (and its `--stdio-bridge` for `ssh://` hosts), detached PTY worker binary, zero-knowledge Noise NK relay server, and CLI client (`ls`, `run` (with `--workspace` or `--new-worktree`, and `--profile`), `logs`, `abort`, `delete`, `project`, `workspace` (including `config|trust|setup|start|stop` for `wada.json`, and `commit|pr` with model-written text), `providers`, `profiles`, `schedule`, `workers`, `pr`, `terminal`, `daemon status|pair|set-password|stop`, `host`).
- `internal/protocol`: append-only WebSocket JSON wire envelopes, dotted RPC schemas, normalized events, and binary codecs.
- `internal/relay`: `Noise_NK_25519_ChaChaPoly_SHA256` cipher/handshake, `daemon-keypair.json`, `#offer=` pairing codec, outbound relay connector, and stateless WebSocket relay router.
- `web/`: responsive React 19 + TypeScript PWA client with Noise NK E2EE relay transport (cross-tested against Go golden vectors), direct LAN/Tailscale WebSocket client, sessions grouped by project and workspace, a Workspace tab (create and archive worktrees, trust `wada.json`, run setup, start and stop services), a Schedules tab, agent profiles on new chats, steer/queue/interrupt during a turn, dictation where the browser has speech recognition, chat/approvals/diffs/terminal views, a build-generated service worker (`web/sw/sw.js`, emitted by `vite.config.ts`) with an update banner, and static serving from `wadad --web-dir`.
- `internal/client`: Go WebSocket client SDK (`Dial`, `DialLocal`, `DialRelay`, `DialOffer`, `DialHost`, `DialSSH` with `ServeStdioBridge` for `wadad --stdio-bridge`, `ConnectOrStartLocal`), saved `hosts.json` registry, binary terminal streams, and platform detached spawn helpers.
- `internal/daemon/*`: headless `wadad` server (`server`), `wadad.db` receipt/timeline/project store (`store`), the projects and workspaces registry (`workspaces`), shared CLI runner (`runner`), Git worktree & Forge PR manager (`worktree`), external CLI harness adapters (`harness`: ACP, Claude Code, Codex), detached PTY/ConPTY terminal worker & supervisor (`terminal`), multi-provider orchestrator (`orchestrator`), the loopback MCP server that gives external workers the Chief tools (`mcp`), agent profiles (`profiles`), schedules and heartbeats (`schedules`), and embedded PiG runtime (`pig`).
- `docker/`: self-hosted `wada-relay` + Caddy TLS and `wadad` Dockerfiles & Compose stack (see [self-hosting](docs/self-hosting.md)).
- `internal/contract/*`: Wada's declarations shared between plugins — types, interfaces,
  keys, topics, pipelines, points. No implementation, no state.
  `agent` (backend seam), `chat` (open conversation, outgoing pipeline, composer
  slots and strip, work panel sections and hiding the panel, drawers, selection actions, file drops, completions, starters, requirements), `tabs` (the open tabs, the pane kinds other plugins contribute, and the
  buttons at the strip's end),
  `workspace` (the backend's projects and workspaces, and the conversations in them),
  `attachments` (images waiting in the composer, their actions and replacement),
  `terminal` (detached PTY terminal management contract and stream lifecycle),
  `browser` (the in-app browser's tab kind).
- `internal/plugins/*`: every feature. `daemon` (connects to local or remote `wadad` over WebSocket RPC via `hosts.json`, auto-spawns local `wadad`, provides `agent.*` and `terminal.ServiceKey`, and hosts Settings → Daemon pairing/password/host switcher and first-run/settings provider auth), `chat` (conversation
  page, which asks for a project before the composer), `starters` (the new chat's folder
  starters), `models` (the composer's model and effort), `modes` (the composer's mode), `usage` (cost and context in the work panel or the composer), `filechanges`
  (session, uncommitted and since-the-base Git changes and diffs below Context in the work panel, and a workspace's Changes tab), `attachments` (files sent with a message), `mentions`
  (`@file` and `@folder` in the composer), `skills` (`/skill` in the composer), `markup` (screen capture, and numbered boxes,
  arrows, pins, lines and highlights with notes, and hidden areas, on a pending screenshot), `explore` (read-only side chats in a drawer, started from selected transcript text, whose answers can join the main draft),
  `workspace` (the app's copy of the daemon's projects, workspaces and their
  conversations, needing no chat), `workers` (harness and agent profile picker, live supervised workers in the work panel and a strip above the composer, and Worker Inspector drawer), `worktrees` (New Workspace… in a Git worktree from a branch or pull request, Archive and Restore Workspace…, the work panel's Workspace section with branch, base and PR, Commit… and Create Pull Request… with model-written drafts, and the workspace's trusted `wada.json` setup and services), `terminal` (interactive PTY terminals as tabs in each workspace, ⌘J / ⇧⌘J), `folderpicker` (Add Project… and the new chat's workspace
  in the composer), `tabs` (open chats, terminals, changes and pages as tabs across the title
  strip, one workspace's space at a time, with the space switcher and the tab menus), `palette` (All chats, ⌘K), `schedules` (a conversation's heartbeat in the work panel, and the Schedules tab and dialog), `browser` (web pages as tabs, such as a workspace's service; ⇧⌘L), `voice` (dictation into the composer; ⇧⌘M), `notifications`
  (desktop notices for background conversations), `replay` (synthetic agent events
  for measurements; only builds tagged `wadareplay` install it, through `replay.go`).
- `internal/plugintest`: fake agent (scriptable with `OnSend`, `Emit`, `Respond`),
  chat controller, workspace, files and skills for plugin tests; the host fake is `kittest.Host`.

Design notes: [kernel API](https://github.com/xleotranx/wada-kit/blob/v0.3.1/docs/kernel-api.md), [kit shell](https://github.com/xleotranx/wada-kit/blob/v0.3.1/docs/shell.md),
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
- `make web-test` and `make web-build`: Vitest, TypeScript typecheck, and Vite production build for `web/`.
- `make package`: checks license notices and runs `go tool mygo build`.
- `go run ./examples/notes` in a separate kit checkout: its reference app.
- `go test ./...`; focused: `go test -run TestFirstMessage ./internal/plugins/chat`.
- `go test -run NONE -bench . -benchtime 30x .`: draw and time whole-app
  frames on `ui.Tester` (see docs/performance.md for the readings and the
  real-window recipe).
- `go tool mygo build`: production app/DMG in gitignored `build/`.
- `make demo`: the app on throwaway data from `scripts/seed.py` (many folders, chats,
  long conversations and open tabs), loaded by `wadad`'s embedded PiG runtime from a temporary folder.
  `seed.py --heavy` adds change baselines, images, large tool outputs and a 200 MB project.
- `python3 -I scripts/measure.py [--heavy] text|working|background`: CPU, frames and
  footprint of the real window under a replayed workload (docs/performance.md).

`WADA_CAPTURE=/path/window.png go run .` captures the window and exits
(`WADA_CAPTURE_WAIT=12s` waits longer, e.g. for the model catalog);
`WADA_APPEARANCE=system|light|dark` fixes the palette without saving it;
`WADA_STRICT=1` re-panics plugin failures. Always use a temporary `WADA_DATA_DIR`
for captures and experiments; never reset developer data during verification.

Settings live in `settings.db` (schema 1, via `wada-kit`) under `$WADA_DATA_DIR`, or else
`~/Library/Application Support/wada-desktop-go/` for a `mygo build` app and
`wada-desktop-go-dev/` beside it for `go run` and `mygo dev`. Daemon server identity,
epoch counters, idempotent creation/delivery receipts, sequenced timeline events,
the projects and their workspaces, and schedules live in `wadad.db` (pure-Go SQLite with numbered
schema migrations; schema 6). Future unsupported
schemas are refused. Runtime, pairing, and isolation state under the data directory
also includes `wadad.pid`, `wadad.lock`, `local-credential`, `wadad.log`,
`daemon-keypair.json`, `remote-token`, `password.hash`, `hosts.json`, `worktrees/`,
and `terminal-worker/`. Conversations' messages belong to PiG's JSONL transcripts in
`pig/sessions/`; Wada's session metadata is in `pig/metadata/`, lazy session diff
baselines in `pig/baselines/`, and provider credentials and configuration in `pig/agent/`.
The user's agent profiles are in `agents.json`.

Window frame restoration uses `StateKey: "main"`. F12 / Alt+Cmd+I opens the
inspector; Shift+Cmd+C picks an element.
