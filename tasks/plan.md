# Modular native chat architecture

The user approved the architecture discussed in this chat: a stable chat core,
backend adapters, and optional compiled Go feature modules. Coding remains a
supported module. This is development software; there are no compatibility layers
or external protocol changes.

## Boundaries

- `internal/chat`: UI-free transcript state.
- `internal/agent`: backend-neutral application contracts and decoded events.
- `internal/runner`: cancellation, bounded scheduling, event batching; no concrete backend.
- `internal/opencode`: protocol, process and credential implementation of the contract.
- `internal/desktop`: native chat shell, composition and UI coordination.
- `internal/features/coding`: optional workspace/file/diff feature with owned state.
- `internal/features/terminal`: optional shell feature with owned state and lifetime.
- Narrow feature contracts let modules contribute panels and actions without access
  to desktop private state. Application composition supplies adapters and modules.

## Acceptance

1. A chat-only composition sends, streams, stops, restores and preserves drafts
   without constructing coding tools or terminals.
2. The default composition retains coding and terminal functionality.
3. Runner and core UI consume app-owned agent data, not raw OpenCode events.
4. Backend-specific workspace operations stay outside the core agent interface.
5. Feature state and worker/resource lifetime belong to their modules; stale results
   cannot update a changed context or closed module.
6. Remove the non-rendering page-preview surface and obsolete tab persistence keys.
7. Import guards, regression tests and `make check` enforce the boundaries.

## Execution and verification

First preserve the baseline, then establish the agent contract, extract one feature
at a time, wire optional composition, and update documentation. Retain existing
regressions for cancellation/admission/uncertain mutations and native widget identity.
Use focused package tests between slices, full `make check` at completion, and native
captures with temporary `WADA_DATA_DIR` for default and chat-only compositions.
No developer metadata is reset. No backend is added speculatively.

## Verification evidence — 2026-10-21

- Baseline `go test ./...`: passed (original root package 5.245s).
- Full modular `make check`: MyGo lifetime vet, lint (0 issues), race suite and
  no-cgo suite passed on the final code, including the moved palette guard and
  module-aware keyboard settings (desktop race 40.297s; no-cgo 4.985s).
- Independent core/backend and notes-module composition tests pass. Module
  shutdown, explicit watcher startup, immutable saved settings and connection
  attempt freshness and module-aware keyboard settings have regression coverage. No existing regressions are disabled for this work.
- A temporary runner import of OpenCode was rejected by depguard and removed.
  A temporary feature-local `palette.go` literal was rejected by the palette
  guard and removed. The guard exempts only the actual shared native palette.
- `WADA_LIVE_AGENT=1 go test -run 'TestLiveLocation|TestLiveSessionOps'
  ./internal/desktop`: passed (25.293s). These checks do not generate model turns.
- Native chat-only and default/two-terminal/maximized captures were inspected.
  Both used fresh temporary WADA_DATA_DIR paths. No developer store was reset.
  Images: `/Users/leotran/.codex/visualizations/2026/10/21/01a15cc3-0ba2-7851-8b8f-667bc4dbc080/modular-chat/`.

## Performance ledger

The original source was also archived into a temporary directory to measure the
actual pre-change baseline with the same Go/module versions, rather than relying
on an older development report. Headless timings vary; these are allocation and
layout checks, not claims about GPU/idle behavior or a timing improvement.

| Change | Measurement | Decision |
| --- | --- | --- |
| Reuse decoded text allocation during normalization | Stream/4096: initial modular 1,172,414 B and 8,228 allocations; corrected 844,531–844,646 B and 4,130 allocations | Keep; neutral types retain original streaming allocation behavior |
| Cache static action/panel/shortcut/slash declarations at registration | Transcript/1000: initial modular approximately 123 KB/frame; corrected approximately 8.6 KB/frame | Keep; descriptors are installation-time declarations |
| Compare final frames with actual original source | Original transcript/1000: 8,541 B, 173 allocations; final: 8,586–8,587 B, 165 allocations. Original sidebar/2000: 13,416 B, 322 allocations; final: 13,464 B, 314 allocations | Comparable frame allocation size, eight fewer allocations/frame |

Three final benchmark samples: transcript/1000 171.6–174.5 us; sidebar/2000
143.6–143.9 us; stream/4096 1.455–1.461 ms. A regression test prevents allocating
a second normalized text payload; a composition test ensures declarations are
read once, rather than rebuilding them during every frame.

## Deliberate scope

Modules are compiled Go code installed at startup. Pi and runtime plugin loading
are not implemented. The native shell retains optional folder/context navigation;
file/diff/review, mentions, VCS decoration, permission controls and terminal state
are module-owned. Existing app settings now live under a backend-specific folder;
the old unscoped development database remains untouched and is not imported.
