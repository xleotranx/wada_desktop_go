# Agent contract

`internal/contract/agent` declares Wada's backend-neutral services and events.
`internal/plugins/daemon` implements the desktop side by connecting over WebSocket RPC
(`internal/client` + `internal/protocol`) to the headless `wadad` daemon
(`internal/daemon/server` + `internal/daemon/pig`), which embeds PiG v0.8.0 through
its public Go SDK. PiG types never leave `internal/daemon/pig`; the kit and UI
plugins have no PiG dependency.

## Capabilities

| Key | Purpose |
|---|---|
| `ServiceKey` | List/create/read/send/abort/rename/delete conversations and answer tool approvals |
| `ModelsKey` | Model catalog, default model, per-session model and thinking level |
| `ModesKey` | Ask before changes and Work independently |
| `ChildrenKey` | Independent conversations under a parent with fixed tool policies |
| `InteractionsKey` | Confirmation, selection, input and editor questions; pending questions can be read again |
| `FileTypesKey` | PNG, JPEG, GIF, WebP and text attachments |
| `ImagesKey` | `Fit(ctx, model, file)` returns the size an image is sent at from its bytes. PiG uses its prompt-image resizer, including the model's dimension, encoded byte and JPEG quality limits (defaults: 2000×2000 and 4.5 MiB of base64); with `images.autoResize` off it returns the original dimensions. Unpreparable images return an error. |
| `FilesKey` | Local project file/folder lookup and contained file reads |
| `SkillsKey` | Skills discovered by PiG for the conversation's project |
| `RewindKey` | Fork before a selected user message, retaining the original conversation and files |
| `SnapshotsKey` | Bounded project snapshots and task-start diffs, including pre-existing working files |
| `ChangesKey` | Session file changes against contents before the agent touched them |
| `HarnessesKey` | Available embedded and external coding CLI harnesses (PiG, Claude Code, Codex, ACP) |
| `WorkersKey` | Supervised external worker metadata and status filtered by parent session |
| `WorktreesKey` | Git worktree creation, listing (including dirty state), safe secondary removal, and authenticated `gh` PR status (`PRStatus`) |

See also `internal/contract/terminal` (`terminal.ServiceKey` and `terminal.Updated`), which the daemon plugin provides over the same `wadad` connection for detached interactive PTY streams.

`Events` carries immutable ordered normalized batches on the UI thread, in a
`Scope.PostQuiet` closure: a listener whose view a batch changed calls
`Invalidate`, so events of conversations out of sight (other tabs, side chats,
workflow reviewers) draw no frames.
`StatusChanged` describes startup/readiness/failure, `ModelsChanged` invalidates
the provider/model catalog, and `WorkerUpdated` announces supervised worker
status transitions. Consumers depend on these declarations, not the SDK.

## Ownership and storage

The daemon plugin requires `shell.HostKey`. On activation, if the `active-host`
setting names a saved remote profile in `<DataDir>/hosts.json`, it dials that
remote `wadad` host via `client.DialHost` (using direct WebSocket or Noise NK E2EE
relay); otherwise it attaches to an already running local `wadad` instance (via
`wadad.pid` and `local-credential`), or spawns a detached
`os.Executable() --daemon --desktop-managed` worker inside `<DataDir>` and
reconnects automatically if the WebSocket drops. `wadad.db` tracks server identity,
startup epoch, idempotent creation/delivery receipts, and sequenced timeline events.

Ordinary chats use PiG's built-in coding tools
(`read`, `bash`, `edit`, `write`, `grep`, `find`, `ls`) with Wada's engineering system prompt.
New conversations without a folder use `<DataDir>/workspaces/inbox`; existing
conversation directories remain as recorded. The daemon's PiG storage root is `<DataDir>/pig`:

- `agent/`: SDK configuration and provider credentials, including `auth.json`;
- `sessions/`: SDK-owned JSONL transcripts;
- `metadata/`: atomic Wada records containing session identity, title, folder,
  access mode, parent and child policy, and attachment display metadata;
- `baselines/`: lazy on-disk original file contents used by session diffs (legacy
  inline baselines in `metadata/` migrate here automatically on startup).

The origin is `pig <absolute storage root>`. Session IDs are meaningful only within
that origin. Workspace records and tabs remain scoped by origin. The plugin reads
only its own session records; it does not import another backend's history.

An open conversation owns a directory-specific Services container, Runtime and
Session. The model/auth runtime is shared. The backend keeps the eight most
recently used idle SDK owners; older owners release their session, runtime,
services and subscription together. Service calls, running turns and pending
extension dialogs protect their owner from eviction and can exceed that idle
budget. A turn stays protected through metadata persistence and terminal event
publication. Teardown runs outside the metadata and cache locks. A suspended
extension dialog triggers idle reclamation on a separate worker when it ends,
so SDK teardown can join the callback without blocking that same callback.

Reopening reads the SDK transcript and restores Wada's identity, model, thinking
level, access mode and attachment metadata. Extension bindings start afresh for
the new SDK owner. Session logs can be deferred until the first message; `wadad`
persists identity immediately and supplies it when reopening an empty
conversation. Closing the desktop window leaves active `wadad` runs intact so
re-attaching catches up the timeline. **Restart Daemon** restarts an attached
desktop-managed daemon and creates a new connection generation; generations increase
across plugin activations. Old services return `ErrNotConnected`.

Plugin UI state stays on the UI thread; daemon RPC and PiG state are synchronized in
`wadad` and `internal/client`. Blocking service methods run off the UI thread through
`kernel.Go`, while the desktop plugin's WebSocket reconnect and event loop runs in
`Scope.Spawn` with no bare goroutines inside `internal/plugins/daemon`. Events use
bounded backpressure: at most 64 events or 256 KiB per batch, collected for at most
1/60 second, with one UI batch in flight.

## Sending and completion

`Send` reports admission through `SendAccepted`, `SendQueued` or `SendHandled`;
execution is observed through events. PiG's prompt preflight callback supplies the
disposition. A turn owns its lifetime independently of navigation. The delivery service serializes queued inputs, and chat without it
prevents concurrent prompts to one session, and the backend refuses a busy session
before sending. Different sessions can run independently.

The first send binds extension UI and dispatches `session_start` outside the
metadata lock. Later prompts reuse successful bindings for that SDK session;
the SDK preserves the bindings and handles startup notifications on reload.

Mutations are never automatically retried. SDK automatic turn retry is disabled.
A failed mutation has an unknown outcome unless `Send` returns `ErrNotSent`, which
means validation refused it before admission. Provider errors carried by assistant
messages become `TurnFailed`; an aborted run becomes `TurnInterrupted`. Settlement,
rather than an intermediate tool-loop boundary, ends the Wada turn.

Stored messages follow the active session branch, oldest first, with stable entry
IDs. Tool names map to the contract's semantic `ToolKind`. Assistant messages carry
text, reasoning, tools, cost and usage; reasoning tokens are separated from output.
Images use SDK image content, text files are appended as named fenced blocks, and
Wada preserves the original user text and attachment names for display.

`Send.ID` is echoed as `Event.RequestID`, including turn-owned approvals and
questions. `TurnSucceeded.Result` carries an optional validated JSON result.
`Send.ResultSchema` activates the reserved host `submit_result` SDK tool for that
turn. It validates its arguments, accepts one result, blocks later tool execution,
and terminates the tool batch. The prompt tells the model not to end with text
alone; a model that still answers in text may end with one fenced JSON block, which
PiG validates the same way and uses when no tool result was submitted. The tool and
result state are cleared at settlement.
`Send.Instructions` are scoped to the current prompt. `Send.Internal` keeps generated
continuation prompts out of visible user history; the model still sees them.

`Send.Policy` adds a restrictive policy for one turn. It cannot weaken fixed child
policies or the conversation's access mode. Turn restrictions apply even in Full
access and cannot be bypassed by previously remembered approvals. Planning/routing
and review deny edits; shell commands still require approval. Ordinary implementation
turns without a policy retain their access mode. Cleanup occurs before the terminal
event publishes so the next admitted turn cannot inherit restrictions.

`Deleted` announces a successfully deleted session on the UI thread. Consumers use
it to cancel related task scopes. Approvals and interactions can name their actual
`Session` when rendered in another conversation; answers must target that owner.

## Permissions and interactions

The composer uses the existing remembered mode selection. **Ask before changes**
is the default: reads and searches run automatically;
file edits, shell commands and web requests ask first. **Work independently** skips
individual approvals for those operations while retaining session and policy boundaries.
Pre-tool hooks wait for a matching session/request reply; stale, duplicate and invalid replies fail. Always
grants apply only to that conversation and exact action. Stopping cancels waiters.

A child gets a separate history and persisted parent/policy metadata. Deny wins
over Allow; unlisted tool kinds ask. Its fixed policy takes precedence over ordinary
chat modes. Children are excluded from backend top-level lists. Workflow reviewers remain hidden. The side-conversation plugin can record its
read-only children through `chat.Started` so they appear in tabs and All chats.
General SDK UI questions use the existing interaction contract,
preserving empty answers, selections, confirmations and explicit cancellation.

## Providers and project resources

Settings → Providers lists daemon providers and their configured credential source
via `agent.providers.list`, `agent.provider.login.*` (streaming `provider_auth`
prompts/URL/device-code notices), and `agent.provider.logout`. Awaited login prompts
and browser/device-code notices are presented through native controls. Secrets are
entered in password fields, stored by PiG under `<DataDir>/pig/agent/`, and excluded
from ordinary settings and error diagnostics. Daemon catalog notices refresh
provider and composer model lists.

Wada supplies its engineering system prompt through the pinned SDK's public resource
loader and SessionStartOptions. Automatic extensions,
AGENTS.md/CLAUDE.md, project prompt templates and automatic skills are disabled.
Project-local SDK settings are untrusted. The explicit skill picker discovers
`.agents/skills` in the selected work folder and `pig/agent/skills`; selected skill
references are checked against that catalog before their content is attached. Mentions use local
file reads; absolute paths, traversal and symlinks leaving the project are refused.
Git projects use Git's tracked/untracked, non-ignored file list. Other folders are
walked excluding `.git`, `node_modules` and `.cache`.

## File changes

Before file write/edit/shell calls, Wada records the target's current bytes and
compares them after execution to identify changes. Mutating calls serialize per
conversation directory for baseline attribution; model streaming, reads, and
mutations in distinct directories remain concurrent. SDK tool batches execute
sequentially so a later tool's approval/snapshot cannot block an earlier execution.
Pre-existing edits form the baseline, rather than being counted as agent changes.
Baselines persist across launches. The separate uncommitted Git view remains local.

Snapshots accept regular files up to 8 MiB and project workspaces up to 128 MiB;
a snapshot that exceeds these limits (or a walk of 25,000 files) marks session
changes unavailable, while the permitted tool action can still execute. Git ignores apply
to workspace snapshots. Session diffs compare tracked baselines with the current files;
subsequent external edits to those same files are reflected too. This is change
tracking, not filesystem isolation.


`SnapshotsKey` captures task-wide baselines independently of per-session mutation
tracking. `Snapshot(ctx, session)` returns immutable file bytes; `Diff` compares a
baseline with the current project through the same contained-file and size limits.
Wada's workflow plugin persists its task snapshots in its own database. Snapshot
failure blocks that workflow phase rather than authorizing an unreviewed completion.
