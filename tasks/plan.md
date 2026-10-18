# Plan: Make the codebase easier to reason about

Status: implementation authorized; proceeding in verified increments.

## Objective and scope

Make normal changes understandable from one subsystem, its boundary contracts,
and its tests. Move correctness from remembered conventions into types,
ownership, explicit transitions, and executable checks.

This is a development application with no users. Internal APIs, test fixtures,
and the application's metadata format may break. Replace old implementations
directly; do not retain compatibility aliases, dual paths, or legacy migrations.
This does not permit changing the OpenCode server's API or MyGo's lifetime rules.
Changing the metadata format does not require deleting the developer's database.

Keep existing capabilities and the native UI's appearance. Preserve streaming
allocation behavior, virtualization, bounded event delivery, editor identity,
remote filesystem semantics, and per-server isolation. No new framework,
dependency, feature system, or universal event bus is needed.

## Findings that motivate the work

These are observations from source inspection, not results of a full audit or
tests run during planning.

- `app` in `main.go` mixes navigation, transcript data, connection routing,
  persistence bookkeeping, dialogs, and widget state. Package-wide access makes
  ownership a convention rather than a boundary.
- `agent.go` combines subprocess lifetime, HTTP/SSE protocol, server bootstrap,
  retry scheduling, command execution, and UI mutation.
- Jobs carry a connection generation, but many results only compare server IDs.
  Matching an ID after A -> B -> A does not establish that an attempt is current.
- Files and Changes callbacks often compare a path, query, mode, or state pointer.
  Those checks do not distinguish repeated requests within the same state.
  Folder browsing can resolve into whichever dialog is currently open.
- `queueJob` has a silent default branch when the channel is full. Callers cannot
  distinguish accepted work from work that never entered the runner.
- `inBackground` executes inline when there is no window. Those tests do not
  exercise the same completion ordering as production.
- Transcript blocks carry SVGs and drawing closures alongside conversation data.
- Metadata creation passes through historical migration helpers even for a new
  database. Those paths are unnecessary under the agreed development policy.
- Existing palette guards, exhaustive enum lint, MyGo lifetime vet, race tests,
  view tests, and performance tests are valuable foundations. CI is manual only.

## Target boundaries

Keep the root package as the native application composition and view layer.
Extract three focused internal packages when their data contracts are ready:

| Location | Owns | Must not own |
| --- | --- | --- |
| `internal/chat` | Transcript data, streaming indexes, permissions, turn transitions and stable row identity | MyGo values, JSON decoding, network/disk calls, widget state |
| `internal/opencode` | HTTP/SSE client, wire decoding, subprocess start/stop and protocol errors | Application state, views, scheduling UI actions |
| `internal/runner` | Connection attempts, command admission, ordered execution, cancellation and result publication | `*app`, `*mygo.Window`, UI widgets or direct model mutation |
| Root package | UI-thread application coordination, connection registry/cache, workspace state, preferences and adapters | Wire parsing in views or duplicated transcript rules |

Dependency direction: root -> runner -> opencode; root and runner may use chat
contracts; chat and opencode do not depend on runner or root. Chat depends on the
standard library only. Runner and opencode do not import MyGo. SQLite and native
window adaptation remain in root; extracting them offers little initial value.

The UI thread owns live application and chat state. Workers receive immutable
request snapshots and publish typed results. Publication goes through an injected
dispatcher; the production adapter calls `Window.Update`. The test adapter queues
callbacks so tests can deliberately change their order.

Root state should gain cohesive owners: connection registry/cache, active
conversation and draft, workspace tools, preferences, and view/dialog state.
Use named fields rather than anonymous embedding. Grouping root fields improves
navigation but is not an enforced access boundary; the internal packages provide
the stronger boundaries. Extract another package only if a concrete invariant
still cannot be protected locally.

## Contracts and invariants

### 1. Identity is explicit

Use distinct Go types for identities that are easily confused: session, message,
permission, connection generation, connection attempt, and request revision.
Choose their owning packages with the first extraction; do not introduce a
generic `types` package or wrap every string.

Every connection switch and explicit reconnect creates a new connection
generation. Every automatic connection attempt has its own attempt identity.
Every replaceable lookup has a request revision. Matching server IDs, query text,
paths, or modes alone never proves freshness.

An active-view result must match the current generation, attempt when relevant,
and owning request revision before it changes state. Registry operations such as
probing an inactive server instead match that server's configuration revision
and operation revision. Do not discard valid inactive-server health results
merely because another server is active.

Cancellation limits wasted work. Acceptance checks protect correctness even if
the worker ignores cancellation or its callback was already posted. Shutdown
invalidates results and cancels work. A generation switch cancels the previous
attempt and invalidates its queued commands and results.

### 2. Related state changes have one owner

Named operations cover switching/reconnecting, opening a stored conversation,
starting a draft, adopting a created session, resetting a transcript, changing
workspace, and ending a turn. Each operation performs its complete reset or
transition. UI callbacks do not reproduce portions of that sequence.

Use a small conversation target kind for draft versus stored session rather than
requiring unrelated empty fields to imply the mode. Draft creation has an
identity, so a late session creation cannot adopt a newer draft. Server scopes
own their pins and catalog; avoid a second mutable copy of the active pins.

### 3. Commands are admitted explicitly

Expose typed runner operations for prompt, open, stop, permission answer, rename,
delete, model change, pagination, refresh and usage lookup. Callers cannot build
the current wide `agentJob` representation. Keep any private queue representation
private to runner and cover its enum switch with exhaustive lint.

Submission reports accepted, busy/full, or closed. An optimistic UI transition
must happen only after admission, or have an explicit rollback on rejection.
Refresh requests may coalesce. Prompts, approvals and destructive operations
must never disappear silently. Switch and cancellation control must remain
responsive under ordinary queue pressure and during cancellable I/O.

Separate unsent work from work whose external outcome is unknown. A read may be
retried; a prompt or mutation that timed out after being sent must not be blindly
replayed. Without server-supported idempotency or reconciliation, report the
uncertain outcome and let the user inspect/retry. This is an intentional behavior
correction, not a promise of exactly-once execution.

### 4. Protocol decoding happens once

Decode wire events at the OpenCode boundary into typed payloads. Validate the
identities required by that event. Ignore unknown event names for forward server
extensibility; malformed known events produce a bounded diagnostic and never
partially mutate state. Avoid adding duplicate validation inside already trusted
internal operations.

Translate transport payloads into chat inputs at the adapter boundary. The chat
model stores tool data and symbolic presentation kinds. Rendering selects icons,
formats details, and retains markdown/editor/widget state in root.

Only claim exhaustiveness for representations the checks actually cover. Go
does not automatically enforce exhaustive interface type switches; prefer the
existing enum-based mechanism or add an explicit coverage check if a union is
introduced.

### 5. Rendering does not initiate external work

Opening Files/Changes, changing a workspace, refreshing, searching, and selecting
a file initiate requests through actions/coordinators. Rebuilding a view renders
the current loading/ready/error state without starting another external read.
Pure render caches, markdown preparation, editor patch preparation, and widget
state updates remain allowed where required by MyGo.

### 6. Development persistence has one schema

Create the final schema directly. Remove v1-v4 migration ladders and legacy-only
tests; retain transactional saves, scoped keys, snapshot isolation and latest
write-result acceptance. Preserve the SQLite adapter and asynchronous writer.

An unsupported existing schema returns a clear reset instruction. Provide an
explicit development reset command that affects only this app's metadata file;
never silently reset on corruption or open failure. Do not touch OpenCode's
conversation store. Existing developer data is not deleted during this refactor.

## Execution order

Detailed acceptance criteria and commands are in `tasks/todo.md`.

1. Establish the verification baseline and enable automatic CI.
2. Introduce deterministic background dispatch and completion tests.
3. Unify connection/attempt acceptance in the agent path.
4. Add request revisions to Files/Changes and move their I/O out of rendering.
5. Apply operation ownership to browsing, attachment, policy, pairing and probes.
6. Give command submission explicit admission behavior.
7. Remove presentation objects from transcript data.
8. Extract the chat model and its transition tests.
9. Extract the OpenCode client and process adapter.
10. Move event decoding into the protocol boundary.
11. Expose typed commands and extract the runner without a MyGo dependency.
12. Consolidate root transitions and state ownership by subsystem.
13. Replace metadata migrations with the direct development schema.
14. Add boundary guards, simplify the project guide and finish verification.

Each step must leave a compiling, usable application. Package cutovers can touch
more callers than a normal small task; perform related mechanical call-site
updates in the same compiling change. Do not preserve obsolete APIs merely to
meet an artificial file-count limit. Split root-state consolidation into separate
connection, conversation and workspace changes rather than one mass rename.

## Verification strategy

Before edits, record `make check` and representative benchmarks. Existing tests
are behavior evidence, not a requirement to preserve old internal APIs. Move
tests with their subjects and replace fixture manipulation with narrow builders
where package privacy requires it.

Use the manual dispatcher and fake clients to test late results, repeated inputs,
queue pressure, retries and shutdown without sleeps. Explicit sequences include
server A -> B -> A, reconnecting A, attempt 1 finishing after attempt 2, file
A -> B -> A, query x -> y -> x, mode working -> branch -> working, closing and
reopening a browser, and a deleted or edited server completing its old probe.

Chat model tests cover wrong-session rejection, draft adoption, tool and approval
lifecycle, completion/interruption, stored transcript restore, row identity, and
usage placement. Repeated completion must not repeatedly trigger follow-up work;
do not assume duplicate text deltas are detectable without protocol sequence IDs.

Transport tests use `httptest` for routes, payloads, limits, malformed events,
authentication, cancellation and SSE shutdown. Runner tests use a fake client,
controllable clock and explicit barriers to verify admission, scheduling, retry
classification, prompt interruption and connection control.

Keep real MyGo view tests for interaction, focus, extra rebuilds, scroll following,
and editor retention. Preserve existing performance guards; compare the transcript,
sidebar and streaming benchmarks against the recorded baseline. No speculative
coverage percentage or wall-clock timing threshold is required.

The final gate is `make check`. Run capture checks for conversation and settings
after the model/UI cutovers. Live-agent tests are an additional integration check
when the required CLI is present; deterministic tests remain sufficient for CI.

## Automated enforcement and minimal prose

- Enable push and pull-request triggers for the existing macOS check workflow.
  Validate workflow edits with actionlint. Branch-protection requirements are
  hosting settings and are separate from workflow execution.
- Keep MyGo lifetime vet, exhaustive lint, race/no-cgo passes and palette guards.
- Add import-boundary checks through the existing linter where supported, with
  explicit production/test policy. Verify violations fail the gate. Do not build
  a general architecture-analysis framework.
- Extend the palette guard if new production directories can contain UI code.
- Keep rules that require judgment in the MyGo skill. Update AGENTS.md to a short
  package map, gate commands, ownership/thread rules, and links to representative
  tests; remove its long narration of internal fields.
- Correct README claims that no app data is stored and that all folders use local
  dialogs. Document the current architecture and metadata reset behavior.
- A verification skill, if useful after the commands settle, should only route a
  change to existing checks and captures. It must not duplicate their logic or
  become another source of architectural rules.

## Completion criteria

- Chat logic can be tested without MyGo, a window, JSON or a server.
- Transport and runner cannot access application state or UI framework types.
- Every asynchronous operation has an owner, cancellation lifetime and acceptance
  rule; out-of-order tests demonstrate those rules.
- Command rejection is visible and does not leave a phantom accepted action.
- Ordinary views cannot accidentally issue repeated external work on rebuild.
- Root state transitions have one implementation each; per-server state has one
  authoritative representation.
- There is one metadata schema and no compatibility path left behind.
- Existing functionality, UI identity and performance guards pass; automated CI
  runs the same gate; the guide explains only what code and checks cannot.

## Risks and decisions to revisit with evidence

The largest risks are transcript allocation regressions, copying mutable widget
state, and changing reconnect semantics while moving the runner. Keep those moves
separate and use existing benchmarks and controllable fake clients as evidence.
Do not copy `strings.Builder` values after use. Keep MyGo editor/list/handle state
in stable UI-owned storage.

Package names are proposed, but their dependency direction and responsibilities
are the design contract. If an extraction needs a broad interface or cyclic
imports, revisit the boundary before adding adapters. No additional package is
justified merely because a source file is long.

## Baseline evidence

2026-10-18, macOS arm64, Apple M4, Go 1.27.2. `make check` passed
(MyGo vet, lint: 0 issues, race: 10.391s, no-cgo: 3.357s). Actionlint
v1.7.7 passed the enabled workflow. Three benchmark samples: transcript/1000
182.6–187.5 us, 1440 B/op; sidebar/2000 111.8–114.2 us, 8064 B/op;
stream/4096 1.338–1.349 ms, approximately 844 KB/op.

## Implementation evidence: asynchronous boundaries

Manual worker/publication tests reproduced six stale-result bugs before the
fixes. They now pass, together with cancellation, shutdown, registry lifetime,
render-without-I/O and admission pressure tests. `make check` passes after the
background, request ownership, policy action and transcript-presentation changes
(race 10.543s, no-cgo 3.459s, lint 0 issues). Interaction fixtures explicitly
install an immediate executor; ordering regressions install a manual executor.
Connection-result guarding is in place; runner extraction will complete
control cancellation and its dedicated deterministic attempt tests.
