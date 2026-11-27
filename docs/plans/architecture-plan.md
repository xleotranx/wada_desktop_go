# Wada Coding-Agent Architecture & Execution Plan

> [!NOTE]
> **Document status:** Forward-looking engineering architecture and execution plan (not an as-built design note). In accordance with `AGENTS.md` Rule 8 (*"Docs describe the code as it is"*), this document lives under `docs/plans/` rather than `docs/design/`. As each phase lands, promote implemented specifications into `docs/design/` and remove completed sections from this plan.

---

## 1. Executive Summary & Locked Decisions

This plan restructures **Wada** (`wada_desktop_go`) from an in-process, macOS-first everyday-work desktop app ("Wada Cowork") into a **Paseo-inspired, local-first, client–server developer coding-agent environment** while preserving Wada's core strengths:

- **Native ~50 MiB Go desktop client** (`MyGo` + `Onevra` + `wada-kit`), rather than Electron.
- **Embedded PiG (`v0.8.0`) with Wada-managed API keys/OAuth** as the out-of-the-box primary experience and **Chief Agent** orchestrating external coding CLI harnesses (**Claude Code**, **Codex**, and **ACP**-compatible agents) in isolated **Git worktrees**.
- **Headless Go daemon (`wadad`)** so long-running coding tasks, workflows, terminal PTYs, and remote connections survive closing the desktop window or running on a remote machine/Docker host.

### Locked Decisions

| # | Decision | Architectural Implication |
|---|---|---|
| 1 | **100% coding-agent focus** | Delete all non-code plugins and contracts (`design`, `cowork`, `documents`); re-enable PiG's native coding tools (`bash`, `read`, `write`, `edit`, `grep`, `find`) and refocus `flows` and `starters` on software engineering. |
| 2 | **Keep `markup` plugin** | Retain `internal/plugins/markup` for annotating bug/UI screenshots on desktop; gate capture on host OS and add Windows capture/clipboard support alongside macOS `screencapture`. |
| 3 | **PiG as Chief Agent + external CLI harnesses** | Keep embedded PiG + Wada-managed credentials (`pig/agent/auth.json`) as the default primary runtime. Equip PiG with orchestration tools to spawn and supervise external CLI harnesses (ACP generic adapter, Claude Code, Codex) in isolated Git worktrees. |
| 4 | **Built-in interactive PTY terminals** | Build a detached, cross-platform Go terminal worker (`POSIX PTY` on macOS/Linux, `ConPTY` on Windows) that survives daemon restarts and streams binary VT frames over WebSocket to desktop, Web/PWA, and CLI clients. |
| 5 | **Native Desktop (macOS + Windows first) + Web/PWA + Go CLI** | Keep MyGo + Onevra for desktop (verifying macOS + Windows interactively from Phase 0; Linux later). Serve a responsive TypeScript/React Web/PWA companion for mobile and browser supervision, plus a `wada` Go CLI. |
| 6 | **Hosted relay first, then self-hosted + Tailscale/VPN/direct LAN** | Implement zero-knowledge `Noise_NK_25519_ChaChaPoly_SHA256` E2EE + URL-fragment QR pairing (`#offer=...`). Deploy hosted `wada-relay` first alongside direct LAN/Tailscale, then package `wada-relay` (single Go binary + Docker + TLS) for self-hosting. |
| 7 | **Single monorepo (`wada_desktop_go`)** | Keep desktop (`main.go`), daemon (`cmd/wadad`), CLI (`cmd/wada`), relay (`cmd/wada-relay`), shared Go packages (`internal/protocol`, `internal/client`, `internal/daemon`, `internal/relay`), and Web/PWA (`web/`) in this repository. |
| 8 | **Compile-time Go plugins first** | Keep `wada-kit/kernel` compile-time Go plugins across Phases 0–5. Defer a user-installable third-party plugin ecosystem and automation triggers to Phase 6+. |

---

## 2. Target Monorepo Layout & Import Boundaries

### Directory Structure

```text
wada_desktop_go/
├── main.go                          # Desktop app composition (MyGo + wada-kit shell + UI plugins)
├── measured.go / replay.go          # Desktop frame measurement / synthetic replay build tags
├── go.mod / Makefile / .golangci.yml
├── cmd/
│   ├── wadad/                       # Headless Go daemon binary (macOS, Windows, Linux, Docker)
│   ├── wadad-term/                  # Detached cross-platform PTY worker (or `wadad --term-worker`)
│   ├── wada/                        # Docker-style CLI (`wada run`, `wada ls`, `wada daemon`, `wada attach`)
│   └── wada-relay/                  # Stateless zero-knowledge Noise NK WebSocket relay server
├── internal/
│   ├── protocol/                    # Append-only WS JSON envelopes, binary codecs, capability flags, schemas
│   ├── client/                      # Reusable Go daemon client SDK (direct Unix/NamedPipe/TCP & Noise NK relay)
│   ├── relay/                       # Noise NK handshake/cipher framing + relay client/server primitives
│   ├── daemon/                      # Headless backend (ZERO imports of mygo or wada-kit/shell)
│   │   ├── server/                  # HTTP/WS server, Host/CORS guards, credential & password auth, relay link
│   │   ├── store/                   # Pure-Go SQLite + numbered schema migrations + atomic JSON stores
│   │   ├── engine/                  # AgentManager lifecycle state machine, timeline sequencer, creation journal
│   │   ├── providers/               # AgentProvider interface + PiG (Chief), ACP, Claude Code, Codex adapters
│   │   ├── worktree/                # Git worktree create/reconcile/merge/cleanup/recovery + repo trust gates
│   │   ├── terminal/                # Detached terminal worker supervisor, 0600 socket/pipe protocol, VT replay
│   │   ├── workflow/                # Server-owned Plan → Work → Review engine & reviewer child orchestration
│   │   ├── workspace/               # Project/workspace registry, directory sync cursors, file search/read
│   │   └── git/                     # Cross-platform Git status, diff, branch, and forge (gh) helpers
│   ├── contract/                    # Desktop plugin contracts (no implementation, no state)
│   │   ├── agent/                   # Backend service, capabilities, events (extended with sequence/worktree/terminal)
│   │   ├── attachments/
│   │   ├── chat/
│   │   ├── tabs/
│   │   ├── terminal/                # Desktop terminal pane contract (attach/resize/input/snapshot)
│   │   ├── workflow/
│   │   └── workspace/
│   ├── plugins/                     # Compile-time desktop Go plugins (UI + daemon client)
│   │   ├── daemon/                  # New: connects to local/remote wadad; provides agent.ServiceKey et al.
│   │   ├── pig/                     # Refactored into daemon/providers/pig (optional in-process fallback removed post-MVP)
│   │   ├── chat/                    # Conversation timeline, composer, unified approvals, subagent tree
│   │   ├── terminal/                # New: interactive terminal split/pane widget in MyGo + xterm/VT surface
│   │   ├── filechanges/             # Routed through daemon Git/worktree diff API
│   │   ├── starters/                # Refactored for Git repos & codebases only
│   │   ├── flows/                   # Refactored Plan → Work → Review coding prompts
│   │   ├── markup/                  # Kept (local desktop screenshot annotation; macOS + Windows)
│   │   └── attachments/, explore/, folderpicker/, mentions/, models/, modes/,
│   │       notifications/, palette/, replay/, skills/, tabs/, usage/, workflow/, workspace/
│   └── plugintest/                  # Fakes and harnesses for plugin & protocol tests
├── web/                             # Responsive TypeScript + React PWA (Vite/Preact or React 19)
│   ├── src/crypto/                  # Noise NK (@noble/curves + @noble/ciphers) + fragment offer parser
│   ├── src/protocol/                # Generated/verified TS wire types matching internal/protocol
│   ├── src/client/                  # WS client, 10s ping/pong liveness, paged timeline catch-up, IndexedDB replica
│   └── src/ui/                      # Host switcher, worktree list, Chief + worker stream, approvals, diffs, PTY view
└── docs/
    ├── design/                      # As-built architecture & contract docs (kept in sync per AGENTS.md)
    └── plans/
        └── architecture-plan.md     # This plan
```

### Enforced `.golangci.yml` `depguard` Rules

Extend `.golangci.yml` with strict import rules alongside existing `contract`, `plugins`, and `plugintest` rules:

1. **`protocol` (`internal/protocol/**/*.go`):** Allows only `$gostd`. Zero dependencies on daemon, client, relay, kernel, or UI.
2. **`relay` (`internal/relay/**/*.go`):** Allows `$gostd`, `golang.org/x/crypto`, `github.com/gorilla/websocket`, `wada-desktop-go/internal/protocol`.
3. **`daemon` (`internal/daemon/**/*.go`):** Allows `$gostd`, PiG SDK (`github.com/MichaelKinsy/PiG/...`), pure-Go SQLite driver, `github.com/gorilla/websocket`, `github.com/Microsoft/go-winio`, `golang.org/x/crypto`, `golang.org/x/sys`, `wada-desktop-go/internal/{protocol,relay}`. **Strictly forbids** `github.com/egoist/mygo`, `github.com/oneo-me/onevra`, `github.com/xleotranx/wada-kit/shell`, and `internal/plugins/*`.
4. **`client` (`internal/client/**/*.go`):** Allows `$gostd`, `github.com/gorilla/websocket`, `github.com/Microsoft/go-winio`, `wada-desktop-go/internal/{protocol,relay}`. **Strictly forbids** `mygo`, `onevra`, `wada-kit`, and `internal/daemon`.
5. **`plugins` (`internal/plugins/**/*.go`):** Retains existing isolation (plugins never import each other or `internal/daemon` directly). Only `internal/plugins/daemon` may import `wada-desktop-go/internal/client` and `wada-desktop-go/internal/protocol`.

---

## 3. Target System Architecture

```text
┌───────────────────────────┐   ┌───────────────────────────┐   ┌────────────────────┐
│  Wada Desktop (Go / MyGo) │   │  Wada Web / PWA (TS)      │   │  Wada CLI (Go)     │
│  macOS + Windows first    │   │  Mobile + Browser         │   │  wada run/ls/logs  │
│  • Native UI + Onevra     │   │  • Live Chief + workers   │   │  wada daemon/pair  │
│  • Multi-host tabs/spaces │   │  • Approvals & diffs      │   │  wada attach       │
│  • Interactive PTY pane   │   │  • Follow-up composer     │   │  wada worktree     │
│  • Local markup plugin    │   │  • Read-only PTY viewer   │   │                    │
└─────────────┬─────────────┘   └─────────────┬─────────────┘   └─────────┬──────────┘
              │ Unix sock / Named Pipe /      │ Direct / Tailscale OR     │ Local IPC / WS
              │ LAN / Tailscale / Relay       │ Hosted / Self-hosted Relay│
              └───────────────────────────────┼───────────────────────────┘
                                              ▼
                                 ┌──────────────────────────┐
                                 │  wada-relay (Go)         │
                                 │  Zero-knowledge router   │
                                 │  Noise NK E2EE end-to-end│
                                 │  Hosted first → Self-host│
                                 └────────────┬─────────────┘
                                              │ Outbound WS (no open inbound ports)
                                              ▼
┌────────────────────────────────────────────────────────────────────────────────────┐
│  wadad (Headless Go Daemon — macOS / Windows / Linux / Docker)                     │
│                                                                                    │
│  ┌──────────────────────────────────────────────────────────────────────────────┐  │
│  │ HTTP / WebSocket Server (internal/daemon/server)                             │  │
│  │ • Capability-gated append-only JSON RPC + binary frames (PTY + 256K files)   │  │
│  │ • Host/Origin DNS-rebinding allowlist, 0600 local-credential, bcrypt auth    │  │
│  │ • Noise NK E2EE termination + URL-fragment pairing offer generator           │  │
│  └──────────────────────────────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────┐  ┌────────────────────────────────────────────┐  │
│  │ Authoritative State & Sync   │  │ Orchestration & Git Isolation              │  │
│  │ • Pure-Go SQLite + migrations│  │ • Git Worktree Manager + repo trust gate   │  │
│  │ • Creation & delivery journal│  │ • Server-owned Plan → Work → Review engine │  │
│  │ • Monotonic seq/epoch events │  │ • Unified Tool Approval Broker             │  │
│  └──────────────────────────────┘  └────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────────────────────────────┐  │
│  │ Agent Runtime Layer (internal/daemon/providers)                              │  │
│  │                                                                              │  │
│  │  ┌────────────────────────────────────────────────────────────────────────┐  │  │
│  │  │ Chief Agent: Embedded PiG v0.8.0 (Wada-managed API keys / OAuth)       │  │  │
│  │  │ • Native tools: bash, read, write, edit, grep, find                    │  │  │
│  │  │ • Orchestration tools: list_providers, create_worktree, list_worktrees,│  │  │
│  │  │   spawn_agent, send_agent, wait_agent, inspect_agent_diff, stop_agent  │  │  │
│  │  └─────────────────────────────┬──────────────────────────────────────────┘  │  │
│  │                                │ dispatches parallel workers into worktrees   │  │
│  │                                ▼                                              │  │
│  │  ┌───────────────────┐  ┌───────────────────┐  ┌──────────────────────────┐  │  │
│  │  │ Generic ACP       │  │ Claude Code       │  │ Codex                    │  │  │
│  │  │ Adapter           │  │ Native Harness    │  │ AppServer / CLI Harness  │  │  │
│  │  │ (Copilot, Gemini, │  │ (user CLI auth)   │  │ (user CLI auth)          │  │  │
│  │  │  OpenCode, etc.)  │  │                   │  │                          │  │  │
│  │  └───────────────────┘  └───────────────────┘  └──────────────────────────┘  │  │
│  └──────────────────────────────────────────────────────────────────────────────┘  │
│                                      │ 0600 Unix socket / SID-ACL Named Pipe       │
│                                      ▼                                             │
│                        ┌───────────────────────────┐                               │
│                        │ wadad-term (Detached PTY) │                               │
│                        │ POSIX PTY / Win ConPTY    │                               │
│                        │ 10k-row headless VT cache │                               │
│                        └───────────────────────────┘                               │
└────────────────────────────────────────────────────────────────────────────────────┘
```

### Subsystem Specifications

#### 3.1 Chief Agent + Worker Harness Model (`internal/daemon/providers`)
- **Primary Experience (PiG Chief Agent):** Runs in-process inside `wadad` using Wada-managed credentials (`<DataDir>/pig/agent/auth.json`). Unlike the removed Cowork persona, `SkipBuiltinTools` is turned **off** (or explicitly selects PiG's native coding toolset: `bash`, `read`, `write`, `edit`, `grep`, `find`), while `ProjectTrusted` is driven by an explicit per-repository trust decision before loading repo-local `AGENTS.md`/`CLAUDE.md` or setup hooks.
- **Orchestration Tool Catalog (transport-neutral inside `wadad`, exposed natively to PiG and optionally via MCP/RPC to workers):**
  - `list_providers()` — installed external CLIs (`claude`, `codex`, ACP binaries), availability, auth/health diagnostics, and selectable models.
  - `create_worktree(projectId, branch, baseBranch)` / `list_worktrees(projectId)` — provisions isolated Git worktrees under a deterministic project worktree directory.
  - `spawn_agent(provider, model, worktreeId, prompt, policy)` — launches a child agent session bound to a worktree `cwd`.
  - `send_agent(agentId, prompt)` / `wait_agent(agentIds, timeout)` — steers or awaits worker completion notifications.
  - `inspect_agent_diff(agentId)` — returns structured diff summary and patch against the worktree's base ref.
  - `stop_agent(agentId)` — aborts a running worker cleanly.
- **Unified Approval & Visibility:** Worker sessions are real `ManagedAgent`s visible in a parent-child **Subagent Tree** in both Desktop and Web/PWA. When Claude Code, Codex, or an ACP worker requests tool approval, `wadad` normalizes it to `ApprovalRequested` (keeping the worker's real `SessionID` as owner) and routes it to subscribed clients (and optionally notifies the Chief Agent if delegated autonomously).

#### 3.2 Git Worktree & State Separation (`internal/daemon/worktree`, `internal/daemon/workspace`)
Adopt Paseo's boundary between **directory-backed** state and **workspace-owned** state:
- **Projects** map to a lexically normalized repository/folder root (`prj_<16hex>`) plus a `projectKey` (normalized Git remote or root path) for cross-host grouping.
- **Workspaces** belong to a project and point to either the main checkout or a dedicated Git worktree (`cwd` + `worktreeRoot` + `baseBranch`).
- **Directory-backed state** (keyed by `(hostId, cwd)`): `git status`, working diffs, branch/PR info, and file listings. Multiple agents/tabs looking at the same `cwd` share one view and never diverge.
- **Workspace-owned state** (keyed by opaque `workspaceId`): active agent tabs, unsent composer drafts, attachments, and terminal slots.
- **Change Tracking:** Replace PiG's global `e.mutation` mutex and eager RAM `Baselines` map (`internal/plugins/pig/engine.go`, `changes.go`) with:
  1. Per-workspace mutation serialization (so parallel worktrees never block each other).
  2. Lazy on-disk baseline storage (content-addressed files on disk, loaded only when computing a diff) plus native Git diffing for worktrees.

#### 3.3 Wire Protocol & Sync (`internal/protocol`, `internal/client`)
- **Transport:** Single WebSocket connection carrying JSON text envelopes (`hello`, `ping`/`pong`, `session` RPCs & streams) and binary frames.
- **Handshake & Capabilities:**
  - Client sends `hello { clientId, clientType: "desktop"|"web"|"cli", protocolVersion, appVersion, capabilities[] }`.
  - Server validates admission (local credential / bcrypt password), emits `server_info { serverId, hostname, version, features[] }`.
  - Schemas are **strictly append-only**; new fields/enums are gated per physical socket capability.
- **Sequencing & Catch-up:**
  - Every `AgentStreamEvent` carries `(epoch, seq)` allocated monotonically by `wadad`.
  - Directory entities (`projects`, `workspaces`, `agents`) each maintain monotonic change sequences + bounded tombstones so reconnecting clients pass cursors and receive minimal deltas (or full snapshot on generation mismatch).
  - Live WS events provide immediacy; `agent.timeline.fetch` (paged, default 200 items) is authoritative for gap recovery.
- **Idempotency & Non-Retrying Mutations:**
  - Preserve Wada's strict safety invariant (*never silently retry mutations*).
  - Client assigns a draft-scoped `idempotencyKey` for session/workspace creation and a stable `messageId` for prompt submission.
  - `wadad` persists a creation/delivery journal before dispatching to PiG/external providers. Reconnecting clients reconcile status from receipts; interrupted deliveries with no conclusive receipt return `outcome_unknown` rather than duplicating prompts.
- **Liveness & Backpressure:**
  - Application-level `ping`/`pong` every 10s (plus immediate 2×5s foreground resume cycle for mobile PWA browsers whose JS event loop froze while backgrounded).
  - Per-socket 8 MiB outbound high-water mark (4 MiB soft terminal threshold); slow/stalled sockets are closed cleanly without blocking `wadad` or sibling clients.
- **Binary Frame Layout:**
  - `0x01–0x04` Terminal stream: `[1B opcode (Output|Input|Resize|Snapshot)][1B slot][payload]`
  - `0x10–0x12` File transfer: `[1B opcode (Begin|Chunk|End)][transferId][256 KiB chunks]`

#### 3.4 Persistence & Migrations (`internal/daemon/store`)
- Eliminate `github.com/egoist/mygo/plugins/sqlite` from daemon/state stores (`internal/plugins/workspace/store.go`, `internal/plugins/workflow/store.go`, and desktop cache).
- Standardize `wadad` on a pure-Go SQLite driver (`modernc.org/sqlite`, preserving `CGO_ENABLED=0` compatibility required by `make test-nocgo`) with WAL mode, busy timeout, and an explicit numbered SQL migration runner (`schema_migrations` table / `PRAGMA user_version` upgrades instead of refusing versions `!= 1`).
- Keep PiG transcripts in `<DataDir>/pig/sessions/*.jsonl` and credentials in `<DataDir>/pig/agent/auth.json` (`0600`), while moving session/workspace/workflow/receipt indexes into `wadad.db`.

#### 3.5 Detached PTY Terminal Worker (`cmd/wadad-term` / `internal/daemon/terminal`)
- **Process isolation:** PTYs live outside `wadad` so daemon restarts, upgrades, or crashes never kill developer shells, dev servers, or test runners.
- **Cross-platform PTY:**
  - **macOS / Linux:** POSIX `pty` (`creack/pty` or `golang.org/x/sys/unix`) + process-group signaling.
  - **Windows:** Windows `ConPTY` (`CreatePseudoConsole` via `golang.org/x/sys/windows` or battle-tested Go wrapper) + Job Objects / `taskkill /T` for clean process tree teardown.
- **Control IPC:** `0600` Unix domain socket inside `0700` `$WADA_HOME/term/` on POSIX (respecting the 103-byte macOS `sun_path` limit with `$TMPDIR/wada-<uid>/` fallback); Windows Named Pipe with owner-SID ACL via `github.com/Microsoft/go-winio` (already in `go.mod`). First frame requires per-worker random secret `hello`.
- **Reconnect replay & resize:** Worker embeds a lightweight headless VT state buffer (10,000 scrollback lines + active screen, cursor style/visibility, mouse mode) and sends a binary `Snapshot` frame on client attach. PTY resize follows **last-interacting-client-wins** (passive views/reflows never resize the PTY).

#### 3.6 Security & Trust Model (`internal/relay`, `internal/daemon/server`)
- **Zero-Knowledge E2EE Relay:**
  - Use `Noise_NK_25519_ChaChaPoly_SHA256` exclusively (no legacy NaCl fallback). `wadad` persists a Curve25519 keypair in `$WADA_HOME/daemon-keypair.json` (`0600`).
  - Pairing URL carries daemon static public key in the URL fragment: `https://app.<domain>/#offer=...` (never transmitted in HTTP requests to the static web host or relay).
  - Every frame after handshake uses ChaCha20-Poly1305 with strictly increasing monotonic 64-bit counters; replayed/reordered/reflected frames immediately close the session.
- **Local & Network Trust Boundaries:**
  - Bind `127.0.0.1` by default + Unix socket / Named Pipe; generate a fresh `0600` `local-credential` per daemon run.
  - Enforce Vite-style `Host` header allowlist (`localhost`, `*.localhost`, literal IPs, configured hostnames) and strict `Origin` checks on every HTTP/WS upgrade to stop DNS rebinding.
  - Support optional `auth.password` (bcrypt) checked inside the encrypted channel / `hello` message before any session state is sent.
- **Credential & Repo Safety:**
  - `pig/agent/auth.json` is never readable over file-preview RPCs or diagnostic exports.
  - Untrusted repository guard: opening an unfamiliar repository prompts before running repo-defined setup commands, `AGENTS.md`/`CLAUDE.md` auto-instructions, or project MCP servers.

#### 3.7 Cross-Platform Desktop Strategy (macOS & Windows First)
- Verify MyGo + Onevra interactive behavior on Windows early (Phase 0): window chrome (`TitleBarHidden` vs. Windows frame), HiDPI scaling, keyboard shortcuts (`Ctrl` vs `Cmd` mapping in `wada-kit`), WebView availability (WebView2), clipboard image/file paste, and `go-winio` pipes.
- Replace macOS-specific copy and assumptions (e.g., `"git isn't installed on this Mac"` in `internal/plugins/filechanges/git.go`).
- Implement `markup` screen capture on Windows (region/clipboard capture or MyGo window capture) alongside macOS `screencapture` (`internal/plugins/markup/capture.go`).
- Build one shared cross-platform subprocess launcher in `internal/daemon/providers` handling Windows `.cmd`/`.exe` `PATHEXT` resolution, UTF-8 env setup, and process-tree termination without spawning PowerShell unnecessarily.

---

## 4. Detailed Phased Execution Plan

### Phase 0: Coding-Only Pivot, Pruning & Storage/Platform Debt Paydown
**Estimated Effort:** 1.5 – 2 weeks (1–2 engineers)

**Goal:** Remove all non-code features, unlock PiG's real coding capabilities, eliminate `mygo/plugins/sqlite` and eager memory bottlenecks, and establish a verified macOS + Windows desktop baseline before splitting processes.

#### Exact Files & Packages Touched
- **Delete:**
  - `internal/plugins/design/` (all files, ~3,045 LOC)
  - `internal/plugins/cowork/` (all files, ~657 LOC)
  - `internal/plugins/documents/` (all files + `anydoc_wasm_bg.wasm` + `anydoc_wasm.js`, ~539 LOC + 6.7 MiB)
  - `internal/contract/documents/`
  - `docs/design/cowork.md`, `docs/design/documents.md` (plus prune non-code sections from `README.md`, `AGENTS.md`, `docs/design/shell-and-plugins.md`)
- **Modify:**
  - `main.go` (remove `cowork.New`, `design.New`, `documents.New`)
  - `internal/plugins/pig/{engine.go,plugin.go,service.go,policy.go,workspace_tools.go,changes.go,settings.go}`
  - `internal/plugins/flows/{planworkreview.go,plugin.go,flows_test.go}`
  - `internal/plugins/starters/{plugin.go,view.go,starters_test.go}`
  - `internal/plugins/workspace/{store.go,plugin.go,workspace_test.go}`
  - `internal/plugins/workflow/{store.go,plugin.go,run.go,store_test.go}`
  - `internal/plugins/filechanges/{git.go,git_test.go}`
  - `internal/plugins/markup/{capture.go,plugin.go}`
  - `go.mod` / `go.sum` / `resources/licenses/` (`make licenses`)

#### Step-by-Step Tasks
1. **Prune non-code plugins & docs:** Remove `design`, `documents`, `cowork`, `internal/contract/documents`, and their registrations in `main.go` and tests (including `internal/plugins/pig/plugin_test.go` references to `cowork.New`). Update `README.md`, `AGENTS.md`, and `docs/design/shell-and-plugins.md` to reflect the pure coding-agent scope.
2. **Unlock PiG coding tools:**
   - Remove `workspace_tools.go` (`cowork` bridge) and set `SkipBuiltinTools: false` in `internal/plugins/pig/engine.go` so PiG exposes its native coding tools (`bash`, `read`, `write`, `edit`, `grep`, `find`).
   - Update `internal/plugins/pig/policy.go` and `service.go` modes (`Ask before changes` → permit reads/searches, ask on shell & edits; `Work independently` → permit shell & edits within work folder/worktree).
   - Add an explicit workspace trust flag before enabling `AGENTS.md`/`CLAUDE.md` loading (`ProjectTrusted`).
   - Replace PiG's `cowork` system prompt with a clean software-engineering Chief Agent system prompt.
3. **Refocus `flows` & `starters`:**
   - Rewrite `internal/plugins/flows/planworkreview.go` prompts for codebase planning, implementation + test/build verification, and independent code/diff review.
   - Refactor `internal/plugins/starters/` to remove Office/document extension counting and present Git/codebase starters only.
4. **Pay down storage & performance debt:**
   - Replace `github.com/egoist/mygo/plugins/sqlite` in `internal/plugins/workspace/store.go` and `internal/plugins/workflow/store.go` with pure-Go `modernc.org/sqlite` behind a new shared `internal/daemon/store` (or `internal/store`) migration runner. Keep `wada-kit`'s `settings.db` intact on desktop.
   - Move `record.Baselines` byte slices out of `internal/plugins/pig/engine.go` startup RAM into lazy on-disk files under `<DataDir>/pig/baselines/<session>/`; replace global `e.mutation` lock in `changes.go` with per-workspace mutexes.
5. **macOS + Windows interactive baseline:**
   - Fix macOS-specific strings (`internal/plugins/filechanges/git.go:31`) and Windows path edge cases (`filepath.ToSlash` / drive-letter normalization).
   - Add Windows capture fallback in `internal/plugins/markup/capture.go` (or clipboard-first capture flow when `screencapture` is absent).
   - Run and verify the app interactively on both macOS and Windows 11.

#### Acceptance Criteria & Verification
- `go build ./...` and `GOWORK=off make check` (`vet`, `lint`, `licenses-check`, `test -race`, `test-nocgo`) pass 100%.
- `make audit` and `make secrets` pass; app binary size drops by >6 MiB from removing `anydoc_wasm_bg.wasm`.
- PiG successfully runs `bash` (e.g., `go test`), `read`, `edit`, and `write` in both `ask` (with approval prompt) and `full` modes on macOS and Windows.
- `internal/plugins/workspace` and `internal/plugins/workflow` have zero imports of `github.com/egoist/mygo/plugins/sqlite`.

#### Risks & Mitigations
- *Risk:* Surfacing latent MyGo UI, WebView2, or shortcut bugs when testing Windows interactively for the first time.
  - *Mitigation:* Allocate explicit buffer in Phase 0 for Windows smoke testing (`go tool mygo dev .` on Windows) and upstream/kit fixes before daemon extraction begins.
- *Risk:* `modernc.org/sqlite` increases binary size or `licenses-check` entries.
  - *Mitigation:* Run `make licenses` and measure binary/footprint deltas (`docs/performance.md` benchmarks) immediately after switching; the removal of `anydoc_wasm_bg.wasm` (6.7 MiB) offsets it.

---

### Phase 1 (MVP): Headless Go Daemon (`wadad`) + Wire Protocol + Desktop Attach + Go CLI
**Estimated Effort:** 4 – 6 weeks (1–2 engineers)

**Goal:** Separate execution from the desktop UI process so coding conversations, workflows, and file state run inside `wadad` and survive closing/restarting the desktop app, controllable from both Desktop and CLI.

#### Exact Files & Packages Touched
- **New packages:**
  - `cmd/wadad/main.go` (daemon entrypoint + supervisor/worker PID lifecycle)
  - `cmd/wada/main.go` (Go CLI: `daemon start|stop|status|restart`, `ls`, `run`, `logs`, `attach`, `permit`)
  - `internal/protocol/` (`envelope.go`, `messages.go`, `binary.go`, `capabilities.go`, `errors.go`)
  - `internal/client/` (`client.go`, `transport_local.go`, `transport_ws.go`, `sync.go`, `receipts.go`)
  - `internal/daemon/server/` (`http.go`, `ws.go`, `auth.go`, `host_guard.go`, `lifecycle.go`)
  - `internal/daemon/engine/` (`manager.go`, `timeline.go`, `journal.go`)
  - `internal/daemon/providers/pig/` (extracted from `internal/plugins/pig/`)
  - `internal/daemon/{store,workflow,workspace,git}/`
  - `internal/plugins/daemon/` (desktop plugin providing `agent.ServiceKey` and optional keys over `internal/client`)
- **Modify:**
  - `.golangci.yml` (add `protocol`, `daemon`, `client` `depguard` rules)
  - `main.go` (swap `pig.New` for `daemon.New`; keep `internal/plugins/pig` only until Phase 1 cutover tests pass, then remove legacy in-process path)
  - `internal/contract/agent/agent.go` (add optional sequence/epoch metadata and `Files`/`Changes` remote guarantees)
  - `internal/plugins/{filechanges,starters,workspace,workflow}/` (route all FS/Git/workflow operations over daemon client APIs instead of local `os.*` / `exec.Command("git")`)

#### Step-by-Step Tasks
1. **Define `internal/protocol`:**
   - Implement JSON envelope (`hello`, `ping`/`pong`, `session` request/response/event) with `protocolVersion`, capability strings, and strict append-only validation.
   - Define binary framing codec for chunked file/attachment transfers (`256 KiB` chunks).
   - Add `idempotencyKey` (creation) and `messageId` (prompt delivery) + monotonic `(epoch, seq)` fields on all timeline events and directory snapshots.
2. **Build `internal/daemon` & `cmd/wadad`:**
   - Extract PiG engine, `workflow` engine, `workspace` registry, and `filechanges` Git service into `internal/daemon/*` with zero MyGo/UI imports.
   - Add `AgentManager` with durable SQLite creation/delivery journal, paged timeline history (`limit=200`), and per-subscription event filtering.
   - Implement HTTP/WS server with `Host` allowlist (`localhost`, `*.localhost`, literal IPs), CORS guard, per-run `0600` `local-credential`, and `wadad.pid` lock + supervisor (`desktopManaged` flag).
   - Listen on `127.0.0.1:<port>` plus local IPC (`0600` Unix socket on macOS/Linux; `go-winio` SID-ACL Named Pipe on Windows).
3. **Build `internal/client` & `internal/plugins/daemon`:**
   - Implement `client.Client` with reconnect backoff, capability negotiation, 10s `ping`/`pong`, and paged timeline catch-up.
   - Implement `internal/plugins/daemon` satisfying `agent.ServiceKey`, `ModelsKey`, `ModesKey`, `ChildrenKey`, `SnapshotsKey`, `FilesKey`, `SkillsKey`, `RewindKey`, `ChangesKey`, `InteractionsKey`.
   - Supervise local `wadad` from desktop (`desktopManaged: true`): auto-launch on startup, upgrade/restart stale desktop-managed daemons, attach cleanly without killing CLI-started daemons, and re-attach seamlessly on desktop restart.
   - Move `workflow` execution authority into `wadad`; desktop `internal/plugins/workflow` renders daemon-reported task cards and submits user actions (`resume`, `retry`, `stop`).
4. **Build `cmd/wada` CLI:**
   - Implement core commands (`wada daemon start/stop/status/logs`, `wada ls`, `wada run`, `wada attach <id>`, `wada permit`) sharing `internal/client` and reading `wadad.pid` + `local-credential`.

#### Acceptance Criteria & Verification
- Closing `wada_desktop_go` mid-turn or mid-`Plan → Work → Review` workflow does **not** interrupt PiG; reopening desktop re-attaches to `wadad` and catches up the full timeline and workflow card.
- `wada run "fix failing test"` from CLI creates a session visible in Desktop in real time, and approvals can be answered from either CLI or Desktop.
- `GOWORK=off make check` passes; `depguard` proves `internal/daemon` and `internal/client` have zero UI/MyGo imports.
- Deterministic protocol tests verify: reconnect mid-stream without duplicate/dropped events, `idempotencyKey` dedup across dropped ACKs, `Host` DNS-rebinding `403` rejection, and `outcome_unknown` reporting on interrupted sends.

#### Risks & Mitigations
- *Risk:* Subtle ordering races between live WS stream events and authoritative `Messages` / `timeline.fetch` on reconnect.
  - *Mitigation:* Use Paseo's rule: every timeline row has a daemon-assigned `(epoch, seq)`; clients deduplicate strictly by `(epoch, seq)` and treat paged fetch up to the latest cursor as authoritative.
- *Risk:* Windows Named Pipe vs. loopback port conflicts or lingering zombie `wadad` processes.
  - *Mitigation:* Test daemon lifecycle (`start`, crash-recovery, `desktopManaged` upgrade, `stop`) in automated E2E tests on both macOS and Windows CI runners.

---

### Phase 2: Chief Agent Orchestration + Git Worktrees + External CLI Harnesses
**Estimated Effort:** 5 – 7 weeks (1–2 engineers)

**Goal:** Enable embedded PiG to act as the **Chief Agent** that decomposes engineering work, provisions isolated **Git worktrees**, and spawns/supervises external coding CLI harnesses (**ACP**, **Claude Code**, **Codex**).

#### Exact Files & Packages Touched
- **New packages:**
  - `internal/daemon/worktree/` (`manager.go`, `reconcile.go`, `trust.go`, `worktree_test.go`)
  - `internal/daemon/providers/provider.go` (unified `Provider` interface, capability descriptors, normalized tool-call & mode mapping)
  - `internal/daemon/providers/acp/` (generic ACP stdio/JSON-RPC adapter + registry for Copilot, Gemini CLI, OpenCode, etc.)
  - `internal/daemon/providers/claude/` (Claude Code CLI / stream-json harness adapter)
  - `internal/daemon/providers/codex/` (Codex CLI / app-server harness adapter)
  - `internal/daemon/providers/tools/` (Chief Agent orchestration tools: `create_worktree`, `spawn_agent`, `send_agent`, `wait_agent`, `inspect_agent_diff`, `stop_agent`, `list_providers`)
- **Modify:**
  - `internal/protocol/` (add `Project`, `Workspace` worktree fields, `ProviderInfo`, parent/child `ParentAgentID`, and worktree RPCs)
  - `internal/daemon/engine/` (support multi-provider sessions, lifecycle lane serialization per agent for model/mode switches, parent-child finish notifications)
  - `internal/contract/{agent,chat,tabs,workspace}/` (surface worktree badges, provider selection, and subagent tree)
  - `internal/plugins/{chat,tabs,filechanges,models,modes,workspace}/` (display Chief + child worker tree, live worker streams, unified approval cards for external CLI tools, and per-worktree diffs)

#### Step-by-Step Tasks
1. **Build Git Worktree Manager (`internal/daemon/worktree`):**
   - Implement `Project` (`prj_<16hex>`) and `Workspace` (`main checkout` vs. `new worktree` vs. `existing worktree`) creation, reconciliation (non-recursive root `.git` watcher + slow fallback), archive/worktree removal, and worktree recovery.
   - Implement `.worktreeinclude` copy support for gitignored local env/config files and explicit **repo setup trust gate** before running any project scripts when checking out untrusted code.
   - Enforce directory-backed (`(hostId, cwd)`) vs. workspace-owned (`workspaceId`) state keys.
2. **Define `Provider` interface & cross-platform process supervisor:**
   - Build a single process launcher supporting POSIX process groups and Windows `.cmd`/`.exe` resolution + Job Objects.
   - Implement provider adapters in order:
     1. **Generic ACP adapter** (unlocks dozens of ACP-compliant CLIs immediately).
     2. **Claude Code adapter** (headless streaming JSON / permission hook bridge, inheriting user's local `claude` auth).
     3. **Codex adapter** (app-server / CLI JSON protocol, inheriting user's local `codex` auth).
   - Normalize all external tool calls onto `agent.ToolKind` (`ToolShell`, `ToolRead`, `ToolSearch`, `ToolEdit`, `ToolFetch`, `ToolOther`) and bridge permission prompts into `wadad`'s approval broker.
3. **Equip PiG Chief Agent with orchestration tools:**
   - Register Chief Agent tools in PiG's toolset (`list_providers`, `create_worktree`, `list_worktrees`, `spawn_agent`, `send_agent`, `wait_agent`, `inspect_agent_diff`, `stop_agent`).
   - Update `Plan → Work → Review` workflow option so the `Work` phase can delegate parallel sub-tasks to worker harnesses across worktrees and run an independent reviewer over combined diffs.
4. **Update Desktop UI (`chat`, `tabs`, `filechanges`, `workspace`):**
   - Show a compact **Workers / Subagents** track under the Chief Agent conversation with live status pills (`running`, `waiting`, `idle`, `error`), click-through to inspect any worker's full transcript, and inline approval cards in the parent Chief chat.
   - Allow users to open a direct conversation with any provider (PiG, Claude Code, Codex, ACP) or let PiG manage them.

#### Acceptance Criteria & Verification
- PiG Chief Agent successfully receives a multi-part coding task, creates 2 isolated Git worktrees, spawns a Claude Code worker in worktree A and a Codex/ACP worker in worktree B, waits for completion, inspects both diffs, and summarizes/merges without file conflicts.
- Tool permission requests from external CLIs render natively in Desktop and CLI, block until answered, and survive reconnects.
- Integration tests cover mock ACP/Claude/Codex subprocesses, Windows `.cmd` invocation/cancellation, worktree cleanup, and cross-worktree diff isolation.

#### Risks & Mitigations
- *Risk:* Upstream CLI protocol/flag churn across Claude Code, Codex, and ACP tools, especially on Windows.
  - *Mitigation:* Isolate each provider behind strict adapter tests with recorded golden stdio transcripts; prioritize ACP standardization where upstream tools support it.
- *Risk:* Orphaned subprocesses when `wadad` stops or a worker hangs.
  - *Mitigation:* Always bind spawned agent trees to OS process groups (POSIX) / Windows Job Objects (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`).

---

### Phase 3: Built-in Interactive PTY Terminal Subsystem
**Estimated Effort:** 4 – 5 weeks (1–2 engineers)

**Goal:** Give developers integrated interactive terminals inside Wada (alongside agents and worktrees) backed by a detached cross-platform worker that survives daemon restarts.

#### Exact Files & Packages Touched
- **New packages:**
  - `cmd/wadad-term/main.go` (detached terminal worker process)
  - `internal/daemon/terminal/` (`supervisor.go`, `pty_unix.go`, `pty_windows.go`, `vt_buffer.go`, `reaper.go`)
  - `internal/contract/terminal/` (desktop contract for terminal tabs/split panes)
  - `internal/plugins/terminal/` (MyGo interactive terminal view: keyboard input, selection/copy/paste, resize observer, font rendering)
- **Modify:**
  - `internal/protocol/` (wire binary opcodes `0x01–0x04` and terminal list/create/kill RPCs)
  - `internal/client/` (terminal attach, binary stream reader/writer, resize debounce)
  - `cmd/wada/` (`wada terminal ls|create|attach|capture|kill`)
  - `internal/plugins/chat/` (embed terminal split/drawer alongside conversation or work panel)

#### Step-by-Step Tasks
1. **Build detached terminal worker (`cmd/wadad-term`):**
   - Implement POSIX PTY (`pty_unix.go`) and Windows ConPTY (`pty_windows.go`) behind one Go `PTY` interface.
   - Implement control IPC over `0600` Unix socket (with `$TMPDIR/wada-<uid>/` path-length fallback on macOS/Linux) and SID-ACL Windows Named Pipe, authenticated with per-worker secret `hello` frame and tracked by `$WADA_HOME/terminal-worker/<pid>.json` + `closing-<id>.json` reaper guards.
   - Embed a pure-Go headless VT parser/screen buffer retaining 10,000 scrollback rows + cursor/mouse state (`SGR` mouse, alternate screen) to emit atomic `Snapshot` replay frames on reconnect.
2. **Integrate with `wadad` & `internal/protocol`:**
   - Supervise `wadad-term` from `wadad`: auto-reconnect on daemon restart (`keepTerminals: true`), clean shutdown when requested, and 4 MiB soft / 8 MiB hard per-socket backpressure.
   - Enforce **last-interacting-client-wins** PTY sizing.
3. **Build Desktop & CLI terminal surfaces:**
   - Create `internal/plugins/terminal` in MyGo (monospaced text layout, scrollback, keyboard + mouse wheel forwarding, clipboard copy/paste, split pane inside workspace).
   - Add `wada terminal` commands to `cmd/wada`.

#### Acceptance Criteria & Verification
- Restarting `wadad` (`wada daemon restart`) leaves running interactive shells (e.g., `vim`, `tail -f`, dev servers) alive and replays their exact screen state into Desktop on reconnect.
- Works interactively on both macOS (zsh/bash) and Windows (cmd/PowerShell/Git Bash via ConPTY).
- Stress test: rapid `yes` / large stdout flood triggers backpressure cleanly without OOMing `wadad` or stalling agent chat streams.

#### Risks & Mitigations
- *Risk:* MyGo custom terminal widget fidelity (IME input, ANSI truecolor, mouse selection, resize jitter) vs. battle-tested web `xterm.js`.
  - *Mitigation:* Option A: render a native MyGo canvas/grid for crisp performance; Option B (fallback if IME/VT edge cases stall): embed `xterm.js` inside a single sandboxed local `shell.WebView` inside `internal/plugins/terminal` (since `wada-kit/shell` already provides `WebView` on macOS & Windows) while keeping all PTY ownership in `wadad-term`. Decide via a 3-day spike at the start of Phase 3.

---

### Phase 4: Remote Access (Hosted `wada-relay` + Direct/Tailscale) + Web/PWA Client + Multi-Host
**Estimated Effort:** 5 – 7 weeks (1–2 engineers)

**Goal:** Enable "start at your desk, supervise and steer from your phone or another laptop" via zero-knowledge E2EE relay, direct LAN/Tailscale, and a responsive Web/PWA client.

#### Exact Files & Packages Touched
- **New packages:**
  - `internal/relay/` (`noise.go` implementing `Noise_NK_25519_ChaChaPoly_SHA256`, `framing.go`, `router.go`, `offer.go`)
  - `cmd/wada-relay/main.go` (stateless Go relay server for hosted deployment)
  - `web/` (TypeScript + React 19 PWA: Vite build, Noise NK crypto via `@noble/curves` + `@noble/ciphers`, WS sync client, mobile-first UI)
- **Modify:**
  - `internal/daemon/server/` (outbound relay client, `daemon-keypair.json`, QR URL-fragment offer generator, bcrypt password auth, embedded/static Web UI serving for LAN/Tailscale)
  - `internal/client/` (add Noise NK relay transport + saved host connection manager)
  - `internal/plugins/{daemon,workspace,tabs,palette}/` (key conversations, folders, and tabs by `(hostID, origin, sessionID)` for simultaneous multi-host connections)
  - `cmd/wada/` (`wada daemon pair`, `wada daemon set-password`, `wada host add/ls/remove`)

#### Step-by-Step Tasks
1. **Implement Noise NK E2EE & `wada-relay`:**
   - Implement `Noise_NK_25519_ChaChaPoly_SHA256` with strictly monotonic record counters in `internal/relay` (Go) and `web/src/crypto/` (TS), cross-tested against identical test vectors.
   - Build `cmd/wada-relay`: lightweight stateless WebSocket pair router (daemon registers outbound by public-key hash; client connects and exchanges `e2ee_hello`/`e2ee_ready`). Deploy hosted instance (e.g., `relay.wada.dev`).
   - Implement QR pairing (`https://app.<domain>/#offer=...`) and optional bcrypt daemon password inside the encrypted `hello`.
2. **Build Responsive Web/PWA Client (`web/`):**
   - Build mobile-first + desktop-responsive UI:
     - **Hosts & Workspaces:** Pair via QR/link or connect via direct LAN/Tailscale URL; switch hosts and worktrees.
     - **Chief + Worker Timelines:** Streaming Markdown, collapsible tool calls, subagent tree, unified approval/interaction cards, follow-up composer.
     - **Diffs & Read-only Terminal View:** Inspect per-worktree unified diffs and live terminal snapshot/output from phone (optional tap-to-send input).
     - **Mobile Resilience:** 10s `ping`/`pong` + immediate foreground-resume 2×5s check + IndexedDB replica cache + paged catch-up; PWA manifest + service worker + optional Web Push notifications.
   - Embed `web/dist` (or serve optionally) from `wadad` for zero-config Tailscale/LAN access alongside static hosting for Relay mode.
3. **Add Multi-Host Support to Desktop:**
   - Allow `wada_desktop_go` to save and connect to multiple `wadad` hosts (e.g., local laptop + home Mac Mini + Linux GPU box) simultaneously via `internal/plugins/daemon` and host switcher in `tabs`/`palette`.

#### Acceptance Criteria & Verification
- Cross-language Go ↔ TypeScript Noise NK test suite passes (including tamper, wrong-key, and replay-rejection tests).
- From a phone (iOS Safari / Android Chrome PWA) over cellular data via hosted `wada-relay`, scan desktop QR code, view live PiG + Claude Code worker streams, approve a file edit/shell command, and inspect the resulting diff.
- Backgrounding mobile browser for 60s and returning immediately detects dead sockets and catches up missed timeline events within <2s.

#### Risks & Mitigations
- *Risk:* Mobile Safari PWA limitations (WebSocket suspension, iOS Web Push requiring Home Screen install).
  - *Mitigation:* Document "Add to Home Screen" for iOS notifications and rely on Paseo's proven foreground two-window liveness check + authoritative timeline catch-up so missed events are never lost.
- *Risk:* Go ↔ TS binary frame or Noise nonce counter desync.
  - *Mitigation:* Share golden test fixtures generated by Go and verified in Vitest (`web/`) on every CI run.

---

### Phase 5: Self-Hosted Relay Packaging, Forge/PR Integration & Distribution
**Estimated Effort:** 3 – 4 weeks (1–2 engineers)

**Goal:** Package `wada-relay` for user self-hosting, add GitHub/GitLab PR workflows in worktrees, and ship signed, auto-updating macOS and Windows desktop releases.

#### Exact Files & Packages Touched
- `docker/` (`Dockerfile.wadad`, `Dockerfile.relay`, `compose.yml` for `wadad` + `wada-relay` + TLS reverse proxy)
- `internal/daemon/git/forge.go` (`gh` CLI integration for worktree PR status, checks, create/view PR; strict trusted-host guard)
- `web/src/ui/` + `internal/plugins/filechanges/` (PR status badge & diff actions)
- `.github/workflows/` (release pipelines for macOS signed/notarized `.dmg`, Windows signed installer, Linux `wadad` / `wada` / `wada-relay` binaries, and `ghcr.io` images)
- `docs/` (self-hosting relay, Tailscale/VPN, Docker daemon, and firewall guides)

#### Step-by-Step Tasks
1. **Self-hosted relay & Docker:** Publish multi-arch `wada-relay` and `wadad` binaries + Docker images (`non-root` user, configurable TLS flags `WADA_RELAY_USE_TLS`, `daemon.relay.url` setting in Desktop & CLI). Document Tailscale, SSH tunnel, and direct LAN setups.
2. **Forge / PR integration:** Read authenticated `gh` status per worktree (`checkoutPrStatus`), surface PR link/CI check summary in Desktop & PWA, never probing unauthenticated hosts.
3. **Production Desktop Packaging:** Wire macOS code signing & notarization, Windows Authenticode signing, and MyGo delta auto-updater (`mygo` already includes Sparkle-style updater primitives) for Desktop + managed `wadad`/`wadad-term` version sync.

#### Acceptance Criteria & Verification
- Clean end-to-end self-hosted deployment: `docker compose up` starts `wada-relay`, a local `wadad` connects via `WADA_RELAY_URL`, and `web/` pairs and operates end-to-end.
- Signed macOS DMG and Windows installer install cleanly, launch `wadad`, and auto-update without stranding orphaned `wadad-term` workers.

---

### Phase 6 (Future / Post-Validation): Third-Party Plugin Runtime & Automation Triggers
- **Scope:** Only after `internal/protocol`, `Provider`, and Chief Agent orchestration stabilize in real usage:
  - Out-of-process third-party plugin SDK (JSON-RPC over stdio/socket or WASM plugins running in `wadad` with declarative UI schema contributions rendered by Desktop + PWA).
  - Scheduled cron agents (`wada schedule`) and event-driven webhook/GitHub/Slack/Linear triggers ("Wada Hub-lite").

---

## 5. Cross-Phase Testing, Quality Gates & Hardening

1. **Repository Gate (`GOWORK=off make check`):** Must remain green at the end of every phase:
   - `go tool mygo vet .` (MyGo UI lifetime safety)
   - `golangci-lint` (`exhaustive`, `depguard` enforcing daemon/UI separation, `staticcheck`, `unused`, `ineffassign`)
   - `scripts/licenses.py --check` (third-party notice hygiene)
   - `go test -race ./...` and `CGO_ENABLED=0 go test ./...`
2. **Security & Supply-Chain Gate:** `make audit` (`govulncheck`) + `make secrets` (`gitleaks`) on every release; automated tests for DNS rebinding (`Host` allowlist), path containment, untrusted-repo setup gating, and Noise NK replay/tamper rejection.
3. **Performance & Footprint Budgets (`docs/performance.md`):**
   - Keep Desktop UI settled physical footprint near the ~50 MiB baseline by keeping heavy work (agent runtimes, Git diffs, SQLite, VT buffers) inside `wadad` and `wadad-term`.
   - Maintain `bench_test.go` (`EmptyPageIdle`, `TranscriptIdle`, `StreamDelta`, `TranscriptScroll`, `SwitchTab`) so UI responsiveness never regresses.
4. **Cross-Platform Verification Matrix:**
   - **macOS (arm64/amd64):** Full automated race tests + real-window `scripts/measure.py` + manual smoke check.
   - **Windows (amd64):** Automated CI build + unit/daemon/ConPTY/Named-Pipe tests + interactive manual checklist at every phase boundary.
   - **Linux / Docker (Phase 1+):** Headless `wadad`, `wadad-term`, `wada`, and `wada-relay` CI test suite.
