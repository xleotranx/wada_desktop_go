# Tasks: Codebase clarity and enforceable ownership

Design: [plan.md](plan.md). Implementation is in progress. Baseline evidence is recorded in plan.md.

## 1. Establish the baseline and automatic gate

- [x] Record `make check` and `go test -run '^$' -bench 'Benchmark(Transcript|Sidebar|Stream)$' -benchmem -count=3 .` results, including tool/environment constraints.
- [x] Enable push and pull-request triggers on the existing check workflow; keep manual dispatch.
- [x] Verify with actionlint and `make check`. A pre-existing failure is recorded and resolved before relying on the baseline.

Depends on: none. Files: `.github/workflows/check.yml`, baseline notes in
`tasks/plan.md`; Makefile only if a reproducible baseline issue requires it.

## 2. Make background completion controllable

- [x] Replace the `win == nil` synchronous execution branch with an explicit background executor/dispatcher dependency and a production Window.Update adapter.
- [x] Provide a manual test executor that releases work and posted callbacks independently; retain a small production-adapter integration test.
- [x] Demonstrate a late callback after state replacement and closure without sleeps.

Depends on: 1. Files: `server_folders.go`, new background helper/test files,
`main.go`, relevant folder tests. Verify: focused executor/folder tests and
`make check`. Update affected headless tests to explicitly drain work.

## 3. Make connection attempt results stale-safe

- [x] Introduce connection-generation and attempt identities. Capture immutable identity at request launch, not when callbacks execute.
- [x] Apply consistent acceptance to stream, bootstrap, catalog, projects, stored turns, usage and connection status. Use attempt cancellation for attempt-scoped requests.
- [x] Test A -> B -> A, explicit reconnect, old-attempt completion and app closure; no obsolete result changes the active view.

Depends on: 2. Files: `agent.go`, `agent_jobs.go`, `servers.go`, focused agent
tests. Verify: targeted connection tests and `make check`.

## Checkpoint A

- [x] Async tests control ordering rather than relying on inline completion.
- [x] Agent result acceptance covers both server identity and attempt freshness.
- [x] Gate passes before workspace request changes.

## 4. Give workspace reads revisions

- [x] Files directory reads have per-directory request ownership; file previews and searches have independent latest-request revisions. Changes reads have a revision independent of mode/path text.
- [x] Test file A -> B -> A, query x -> y -> x, working -> branch -> working and refresh replies arriving in reverse order.
- [x] Start reads through opening/selection/refresh actions. Repeated render frames issue zero additional external reads; preserve editor state across unrelated frames.

Depends on: 2, 3. Files: `files.go`, `changes.go`, `workspace.go`, their tests and
browser action wiring where needed. Verify: focused files/changes/workspace
tests, existing editor retention tests and `make check`.

## 5a. Bind folder browsing to its dialog and request

- [x] Folder discovery/listing results match their captured dialog instance, connection generation and request revision.
- [x] Closing/reopening a dialog and browsing A -> B -> A cannot receive an old result; obsolete work is canceled.
- [x] Remote paths remain in the remote server's namespace.

Depends on: 2, 3. Files: `server_folders.go`, `server_folders_test.go`, background
helper if required. Verify: focused folder tests and `make check`.

## 5b. Bind attachment and policy work to their owners

- [x] Attachment reads match the original dialog, draft/workspace and request revision.
- [x] Policy reads/writes retain ordering and ownership when server/workspace changes; existing refusal-to-overwrite behavior remains tested.
- [x] Test old results after switching, draft replacement and shutdown.

Depends on: 2, 3. Files: `main.go`, `policy.go`, attachment/policy tests.
Verify: focused attachment/policy tests and `make check`.

## 5c. Bind server administration work to configuration revisions

- [x] Probes and pairing results match their server/configuration or dialog operation revision; inactive-server probes may complete legitimately.
- [x] Editing/deleting a server invalidates old results; pairing and minted links cannot update a replacement operation.
- [x] Test reversed results and shutdown without displaying secrets in diagnostics.

Depends on: 2, 3. Files: `servers.go`, `settings.go`, `settings_test.go`.
Verify: focused probe/pairing tests and `make check`.

## 6. Make queue admission explicit

- [x] Submission returns an admission result. Required user commands cannot silently disappear; optional refresh work has explicit coalescing behavior.
- [x] Composer, permission and other optimistic state only advance after admission or recover visibly from rejection.
- [x] Test full queue, closed runner, repeated refresh and switch/cancel under load.

Depends on: 3. Files: `agent_jobs.go`, `main.go`, `agent_jobs_test.go`, relevant
composer/permission tests. Verify: focused admission/interaction tests and
`make check`.

## Checkpoint B

- [x] Every asynchronous operation has an identified owner and tested acceptance rule.
- [x] Rendering cannot issue repeated workspace requests.
- [x] Queue pressure is observable; no phantom prompt/approval remains.
- [x] Gate passes before package extraction.

## 7. Separate transcript data from rendering

- [x] Replace SVG fields and drawing closures in transcript blocks with symbolic tool kinds and plain detail payloads.
- [x] Resolve icons/details in chat rendering, preserving activity expansion, permissions, markdown, row identity and virtualization.
- [x] Stored and streamed turns render equivalent content with stable state across extra frames.

Depends on: 6. Files: `main.go`, `agent_apply.go`, `chat.go`, `chat_test.go`,
transcript/performance fixtures. Verify: transcript/view tests, relevant existing
performance guards and `make check`.

## 8. Extract the chat model

- [x] Move transcript storage/indexes, streaming, permission and turn transitions into `internal/chat`; fields that enforce invariants are private. Expose narrow operations and efficient read access.
- [x] Model tests cover wrong sessions, tool/permission lifecycle, interrupted turns, usage placement, reset and repeated completion. Follow-up requests are explicit transition outcomes.
- [x] Root retains list/editor/markdown/expansion state. No MyGo, JSON, I/O, or callback-to-app dependency enters chat.

Depends on: 7. Files: new `internal/chat` sources/tests, `agent_apply.go`,
`main.go`, `chat.go`, migrated transcript tests/callers. This is a mechanical
package cutover; update fixtures in the same compiling change.
Verify: `go test ./internal/chat`, full view tests, streaming benchmark and
`make check`.

## 9. Extract the OpenCode adapter

- [x] Move HTTP calls, wire DTOs, SSE framing and subprocess lifetime into `internal/opencode`. Keep UI result application in root until runner extraction.
- [x] Preserve request/authentication semantics, response limits, remote operations, process cleanup and context cancellation.
- [x] Move protocol tests alongside implementation; root uses the client API without accessing transport internals.

Depends on: 8. Files: new `internal/opencode` files/tests, transport/process
sections of `agent.go`, `agent_child_*.go`, callers of standaloneAgent and moved
fixtures. Split the extraction by HTTP endpoint family before final process/SSE
cutover if needed; do not add a legacy forwarding API.
Verify: `go test ./internal/opencode`, focused remote tool tests, `make check`.

## 10. Decode typed events at the boundary

- [x] Known wire events decode to validated typed payloads once; application/chat code no longer unmarshals raw SSE JSON.
- [x] Unknown event names are skipped. Malformed known payloads yield bounded diagnostics without partial application.
- [x] Event coverage tests exercise every supported event kind and required identity.

Depends on: 9. Files: protocol event decoder/tests, `agent_events.go`,
`agent_apply.go`, event fixtures. Verify: decoder tests, full stream/model tests,
existing allocation guards and `make check`.

## 11a. Introduce typed command entry points

- [x] Callers submit operation-specific arguments with meaningful identity and decision types; the wide job struct is not exposed across the boundary.
- [x] Private scheduling retains exhaustive kind dispatch and the admission contract from task 6.
- [x] Test invalid inputs at entry, queue coalescing and operation-specific payloads.

Depends on: 6, 10. Files: runner command definitions/tests, `agent_jobs.go`,
command callers in `main.go` and `sidebar.go`. Verify: command/admission tests,
relevant view tests and `make check`.

## 11b. Extract the runner and retry policy

- [x] Move connection attempts, execution, bootstrap and retry scheduling to `internal/runner`; inject a narrow client/factory, dispatcher and test clock. No root/MyGo access remains.
- [x] Control can cancel in-flight I/O and supersede queued work. Reads and unsent commands have explicit retry rules; externally uncertain mutations are reported rather than blindly replayed.
- [x] Fake-client tests cover delayed starts, reconnect, bounded queue pressure, prompt/stop, approval replies, uncertain outcomes, cancellation and shutdown cleanup.

Depends on: 11a. Files: new runner sources/tests, runner sections of `agent.go`,
`agent_events.go`, root startup/result adapter. Verify:
`go test -race ./internal/runner`, root integration tests and `make check`.

## Checkpoint C

- [x] Chat, protocol and runner tests execute independently of native UI.
- [x] There is one implementation of each command and event transition.
- [x] Transcript/sidebar/stream benchmarks retain existing performance properties.
- [x] Gate passes before root cleanup and persistence changes.

## 12a. Consolidate connection ownership

- [x] Registry owns server configuration, per-server catalog/session cache, pins, disabled models and health/facts with one authoritative representation.
- [x] Switch/reconnect operations own generation changes and the complete active-view reset; views use narrow queries/actions.
- [x] Server-switch, pins and model-filter tests preserve existing behavior without manipulating unrelated app fields.

Depends on: 11b. Files: `servers.go`, `main.go`, `pinned.go`, `settings.go`,
affected sidebar/tests. Verify: focused servers/pins/models tests and `make check`.

## 12b. Consolidate conversation and draft transitions

- [x] Explicit draft/session target state replaces scattered empty-string conventions; a draft identity guards session creation adoption.
- [x] Open, new draft, adopt, delete and reset each have one transition implementation; derive the title from the selected target/cache where possible.
- [x] Test rapid switching, new draft while session creation is pending, deletion and boot settlement.

Depends on: 12a. Files: root conversation coordinator, `main.go`, `sidebar.go`,
`composer.go`, relevant transition/view tests. Verify: focused conversation tests
and `make check`.

## 12c. Consolidate workspace and view state

- [x] Workspace owner coordinates Files/Changes/policy context; navigation and dialogs keep persistent widget state in named UI-owned fields.
- [x] Update pane callbacks to call transitions rather than duplicating resets. Avoid copying used builders, editor/list state or handles.
- [x] Render extra frames while switching panes, workspace and dialogs; no duplicate I/O, focus loss or retained stale tool state.

Depends on: 12b. Files: `workspace.go`, `browser.go`, `main.go`, pane/view tests.
Verify: workspace/view/focus tests and `make check`.

## 13. Simplify development metadata

- [x] Create the final schema directly and remove historical migration helpers/fixtures. Unsupported existing schemas fail with a reset instruction; corrupt/open failures never trigger automatic deletion.
- [x] Provide an explicit reset command scoped to app metadata. Tests use temporary paths and prove OpenCode data is untouched.
- [x] Preserve transactional snapshots, per-server keys, error reporting and latest-write acceptance; no current developer database is reset during implementation.

Depends on: 12a. Files: `metadata.go`, `metadata_test.go`, reset command handling,
`README.md`. Verify: metadata round-trip/reset/failure tests and `make check`.

## 14. Enforce boundaries and shorten prose

- [ ] Add narrow import restrictions for the extracted packages using supported lint configuration; demonstrate that forbidden imports fail. Preserve enum, lifetime and palette checks across the new layout.
- [ ] Rewrite AGENTS.md as a compact map and verification entry point; update README architecture, persistence and remote-folder descriptions. Link representative tests instead of repeating field behavior.
- [ ] Run the full gate, benchmark comparison, and conversation/settings captures. Record limitations and final evidence. Add a thin verification skill only if it improves check discovery.

Depends on: 12c, 13. Files: `.golangci.yml`, `palette_test.go`, `AGENTS.md`,
`README.md`, final notes in `tasks/plan.md`. Verify: intentional boundary violation
is rejected and removed; `make check`; baseline benchmark command;
`WADA_CAPTURE=/tmp/wada-architecture-chat.png go run .` and
`WADA_SETTINGS=servers WADA_CAPTURE=/tmp/wada-architecture-settings.png go run .`.

## Final checkpoint

- [ ] All completion criteria in plan.md have concrete evidence.
- [ ] All obsolete implementations and compatibility scaffolding are removed.
- [ ] Automatic CI runs the same passing gate.
- [ ] Remaining prose explains intent and framework constraints rather than
      substituting for checks or ownership.
