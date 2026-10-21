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

## Evidence

Baseline: `go test ./...` passed (root 5.245s). The first full modular gate passed: MyGo vet, lint (0 issues), race tests and no-cgo tests. Final evidence is recorded on completion.
