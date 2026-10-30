# Agent contract

`internal/contract/agent` declares Wada's backend-neutral services and events.
`internal/plugins/pig` is their implementation, embedding PiG v0.5.3 through its
public Go SDK. PiG types never leave the plugin; the kit has no agent dependency.

## Capabilities

| Key | Purpose |
|---|---|
| `ServiceKey` | List/create/read/send/abort/rename/delete conversations and answer tool approvals |
| `ModelsKey` | Model catalog, default model, per-session model and thinking level |
| `ModesKey` | Ask before write and Full access |
| `ChildrenKey` | Independent conversations under a parent with fixed tool policies |
| `InteractionsKey` | Confirmation, selection, input and editor questions; pending questions can be read again |
| `FileTypesKey` | PNG, JPEG, GIF, WebP and text attachments |
| `ImagesKey` | `Fit(ctx, model, file)` returns the size an image is sent at from its bytes. PiG uses its prompt-image resizer, including the model's dimension, encoded byte and JPEG quality limits (defaults: 2000×2000 and 4.5 MiB of base64); with `images.autoResize` off it returns the original dimensions. Unpreparable images return an error. |
| `FilesKey` | Local project file/folder lookup and contained file reads |
| `SkillsKey` | Skills discovered by PiG for the conversation's project |
| `RewindKey` | Fork before a selected user message, retaining the original conversation and files |
| `SnapshotsKey` | Bounded project snapshots and task-start diffs, including pre-existing working files |
| `ChangesKey` | Session file changes against contents before the agent touched them |

`Events` carries immutable ordered normalized batches on the UI thread, in a
`Scope.PostQuiet` closure: a listener whose view a batch changed calls
`Invalidate`, so events of conversations out of sight (other tabs, side chats,
workflow reviewers) draw no frames.
`StatusChanged` describes startup/readiness/failure, and `ModelsChanged` invalidates
the provider/model catalog. Consumers depend on these declarations, not the SDK.

## Ownership and storage

The plugin requires `shell.HostKey`. Its storage root is `<DataDir>/pig`:

- `agent/`: SDK configuration and provider credentials, including `auth.json`;
- `sessions/`: SDK-owned JSONL transcripts;
- `metadata/`: atomic Wada records containing session identity, title, folder,
  access mode, parent and child policy, attachment display metadata and original
  file contents used by session diffs.

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
the new SDK owner. Session logs can be deferred until the first message; Wada
persists identity immediately and supplies it when reopening an empty
conversation. All remaining SDK resources, subscribers and pending requests end
with the plugin's scope. Restart PiG creates a new connection generation;
generations increase across plugin activations. Old services return
`ErrNotConnected`.

Plugin UI state stays on the UI thread. SDK state is synchronized separately.
Blocking service methods run off the UI thread, normally through `kernel.Go`.
Conversation mutations are serialized separately from metadata access. SDK model
and thinking-level notifications run without the metadata lock, so extension
handlers can ask questions through the interaction service.
SDK turns run through `Scope.Spawn`; the plugin has no bare goroutines. Events use
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
and terminates the tool batch. The tool and result state are cleared at settlement.
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

The composer uses the existing remembered mode selection. **Ask before write** is
the default: read/search tools run automatically; edit, shell, web and unknown tools
ask first. **Full access** skips ordinary-chat approvals. Pre-tool hooks wait for a
matching session/request reply; stale, duplicate and invalid replies fail. Always
grants apply only to that conversation and exact action. Stopping cancels waiters.

A child gets a separate history and persisted parent/policy metadata. Deny wins
over Allow; unlisted tool kinds ask. Its fixed policy takes precedence over ordinary
chat modes. Children are excluded from backend top-level lists. Workflow reviewers remain hidden. The side-conversation plugin can record its
read-only children through `chat.Started` so they appear in tabs and All chats.
General SDK UI questions use the existing interaction contract,
preserving empty answers, selections, confirmations and explicit cancellation.

## Providers and project resources

Settings → Providers lists SDK providers and their configured credential source.
API-key and OAuth login use `ModelRuntime.Login`; disconnect uses `Logout`. Awaited
login prompts and browser/device-code notices are presented through native controls.
Secrets are entered in password fields, stored by the SDK under Wada's agent directory,
and excluded from ordinary settings and error diagnostics. SDK catalog notices
refresh provider and composer model lists.

PiG discovers project instructions and skills. Skill references are checked against
the current project's catalog before their content is attached. Mentions use local
file reads; absolute paths, traversal and symlinks leaving the project are refused.
Git projects use Git's tracked/untracked, non-ignored file list. Other folders are
walked excluding `.git`, `node_modules` and `.cache`.

## File changes

Before write/edit calls, Wada records the target's current bytes. Before shell calls
it snapshots project files, then compares them after execution to identify touched,
created or deleted files. Mutating calls serialize across conversations for baseline
attribution; model streaming and reads remain independent. SDK tool batches execute
sequentially so a later tool's approval/snapshot cannot block an earlier execution.
Pre-existing edits form the baseline, rather than being counted as agent changes.
Baselines persist across launches. The separate uncommitted Git view remains local.

Snapshots accept regular files up to 8 MiB and shell workspaces up to 128 MiB;
a snapshot that exceeds these limits (or a walk of 25,000 files) marks session
changes unavailable, while the permitted tool action can still execute. Git ignores apply
to shell snapshots. Session diffs compare tracked baselines with the current files;
subsequent external edits to those same files are reflected too. This is change
tracking, not filesystem isolation.


`SnapshotsKey` captures task-wide baselines independently of per-session mutation
tracking. `Snapshot(ctx, session)` returns immutable file bytes; `Diff` compares a
baseline with the current project through the same contained-file and size limits.
Wada's workflow plugin persists its task snapshots in its own database. Snapshot
failure blocks that workflow phase rather than authorizing an unreviewed completion.
