# ADR-001: Stable chat contracts and optional native modules

Status: Accepted. Date: 2026-10-21.

## Context

This is an AI desktop chat app with useful coding workflows. General conversations
must work without those workflows. The previous native app owned file/diff/terminal
state directly and consumed OpenCode messages/events throughout its views. Replacing
the backend or removing a feature therefore changed unrelated app coordination.

There are no users or supported historical internal APIs. Existing external
OpenCode protocol behavior and developer conversation data must be preserved.

## Decision

Keep `internal/chat` pure. Define normalized application messages/events and the
chat client in `internal/agent`. Inject backend connection factories into the runner;
normalize protocol data inside `internal/opencode`. Optional persona/workspace,
filesystem/VCS/policy and pairing capabilities remain separate from core chat.
A missing model selection delegates to the backend's default.

Move the native shell into `internal/desktop`. Root `main.go` selects the backend
and installs compiled Go module factories. Coding and terminal own their state,
views, workers and resource lifetime in separate packages. The shell retains
conversation navigation and optional execution-context/folder organization.

A module implements `feature.Module` (identity and close), then only the interfaces
its real behavior needs. Contributions include actions, keyboard/menu/slash entries,
panels, dialogs, composer controls, header decoration, a bottom panel, saved settings
and explicit startup/context hooks. Duplicate IDs are rejected during composition.
Contributions are captured once at installation, preserving stable callbacks and
avoiding descriptor allocations during every frame. Keyboard settings derive their
command reference from those same declarations. No event bus, dependency container,
dynamic loader or speculative Pi adapter is added.

## Lifecycle and threading

Factories run on the UI thread without starting workers. `Start` runs after the
native dispatcher and background executor are installed. Connection notifications
can refresh features, but do not implicitly start their lifetime. This matters for
headless tests and alternate compositions.

A module derives a child lifetime and cancels it in `Close`. Native UI state stays
on the UI thread. `feature.Run` captures immutable context, owns cancellation and
rejects stale publications by connection, backend attempt, navigation and request
revision. `Host.Post` always uses the configured dispatcher. The runner retains
independent stop/switch controls, bounded event batches and non-replay of uncertain
mutations.

`Backend.Client` creates a lightweight handle without I/O. The shell caches optional
services for the immutable connection configuration. Active process startup and wire
stream ownership belong to the adapter/runner. Metadata paths include backend ID;
module snapshots use separate keys and are copied before asynchronous persistence.
Disabling a module retains its saved settings.

## Alternatives and consequences

Removing all coding features would reduce code size but discard required workflows.
Keeping them as optional native modules preserves those workflows while reducing the
chat shell's state ownership. General chat can run without constructing either module.

A generic plugin framework or runtime loading would add ABI/version/lifecycle policy
before there is a consumer. Compiled packages provide explicit dependencies and let
us change internal contracts directly during development. Adding a module or backend
still requires rebuilding the app; modules are trusted code in the same process.

There is one app-owned representation after the adapter boundary. Text DTO conversion
reuses its decoded allocation through a checked Go conversion, keeping JSON tags
inside the adapter without adding an allocation for every streaming delta.

## Verification

`make check` enforces imports, UI lifetime, enum coverage, races and no-cgo behavior.
Tests exercise a chat-only shell with an independent backend, an unrelated notes
module registered without shell changes, default coding/terminal interactions,
module cancellation, explicit monitoring startup, saved settings and attempt freshness.
Native captures cover chat-only and default compositions with isolated app settings.
