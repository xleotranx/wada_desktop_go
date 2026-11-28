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
| `ChangesKey` | Session file changes against contents before the agent touched them |
| `HarnessesKey` | Available embedded and external coding CLI harnesses (PiG, Claude Code, Codex, ACP) |
| `WorkersKey` | Supervised external worker metadata and status filtered by parent session |
| `WorktreesKey` | Authenticated `gh` PR status of a directory's branch (`PRStatus`) |
| `WorkspacesKey` | The daemon's projects and their workspaces: list, add (restoring an archived one), rename and archive; create, archive and restore worktree workspaces; a project's refs; a workspace's changes against its base. An archive refused for unsaved work fails with `ErrWorkUnsaved`. |

See also `internal/contract/terminal` (`terminal.ServiceKey` and `terminal.Updated`), which the daemon plugin provides over the same `wadad` connection for detached interactive PTY streams.

`Events` carries immutable ordered normalized batches on the UI thread, in a
`Scope.PostQuiet` closure: a listener whose view a batch changed calls
`Invalidate`, so events of conversations out of sight (other tabs, side chats)
draw no frames.
`StatusChanged` describes startup/readiness/failure, `ModelsChanged` invalidates
the provider/model catalog, and `WorkerUpdated` announces supervised worker
status transitions, and `WorkspaceUpdated` carries a project or workspace that was
added or changed. Consumers depend on these declarations, not the SDK.

## Ownership and storage

The daemon plugin requires `shell.HostKey`. On activation, if the `active-host`
setting names a saved remote profile in `<DataDir>/hosts.json`, it dials that
remote `wadad` host via `client.DialHost` (using direct WebSocket or Noise NK E2EE
relay); otherwise it attaches to an already running local `wadad` instance (via
`wadad.pid` and `local-credential`), or spawns a detached
`os.Executable() --daemon --desktop-managed` worker inside `<DataDir>` and
reconnects automatically if the WebSocket drops, except after the daemon or
relay refused its credentials, which it reports instead. One `wadad` serves a data
directory at a time: it holds an exclusive lock on `<DataDir>/wadad.lock` for its
whole run, so a replacement waits until the daemon it replaces has shut down and
removed its runtime files. `wadad.db` tracks server identity,
startup epoch, idempotent creation/delivery receipts, sequenced timeline events, and
the projects and workspaces (below).
The timeline is for catch-up only: it keeps the current and previous epochs, the
last 20000 events of each session per epoch, and nothing for deleted sessions.

A conversation on an external CLI harness (Claude Code, Codex, ACP) takes a
message's text only: the daemon refuses, as not sent, a send with files or
skills, and refuses `SetModel` and child conversations under it. Its mode is
the one it was created with.

Ordinary chats use PiG's built-in coding tools
(`read`, `bash`, `edit`, `write`, `grep`, `find`, `ls`) and, on top-level
conversations, Chief Agent orchestration tools (`list_providers`, `create_workspace`,
`list_workspaces`, `archive_workspace`, `spawn_agent`, `send_agent`, `wait_agent`,
`inspect_agent_diff`, `stop_agent`) with Wada's engineering system prompt. The
workspace tools act on the conversation's own project and refuse the inbox.
`spawn_agent` runs a worker in the conversation's folder, a folder inside it
(`directory`), or a workspace of the same project (`workspace_id`); `stop_agent`
leaves the workspace in place.
New conversations without a folder use `<DataDir>/workspaces/inbox`; existing
conversation directories remain as recorded. The daemon's PiG storage root is `<DataDir>/pig`:

- `agent/`: SDK configuration and provider credentials, including `auth.json`;
- `sessions/`: SDK-owned JSONL transcripts;
- `metadata/`: atomic Wada records containing session identity, title, folder,
  access mode, parent and child policy, and attachment display metadata;
- `baselines/`: lazy on-disk original file contents used by session diffs.

The origin is `pig <absolute storage root>`. Session IDs are meaningful only within
that origin. Each daemon keeps its own projects and workspaces; the app's copy of them starts over when the origin changes. The plugin reads
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
disposition. A turn owns its lifetime independently of navigation. Chat prevents
concurrent prompts to one session, and the backend refuses a busy session before
sending. Different sessions can run independently.

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
chat modes. Children are excluded from backend top-level lists. Side chats live in
a drawer beside their parent conversation, are not emitted on `chat.Started`, and do
not appear in tabs or All chats.
General SDK UI questions use the existing interaction contract,
preserving empty answers, selections, confirmations and explicit cancellation.

## Projects and workspaces

`wadad` keeps the record of where the user works in `wadad.db` (schema 3), in
`internal/daemon/workspaces`, read into memory at startup:

```sql
CREATE TABLE projects (
  id       TEXT PRIMARY KEY, -- opaque "prj_" + 16 hex; never a path
  root     TEXT NOT NULL,    -- absolute and clean, as added
  name     TEXT NOT NULL,    -- the user's name; '' for the root's base name
  created  INTEGER NOT NULL, -- Unix ms
  archived INTEGER NOT NULL);-- Unix ms; 0 while active
CREATE UNIQUE INDEX projects_active_root ON projects(root) WHERE archived = 0;
CREATE TABLE workspaces (
  id        TEXT PRIMARY KEY, -- opaque "wks_" + 16 hex
  project   TEXT NOT NULL REFERENCES projects(id),
  isolation TEXT NOT NULL,    -- "project", "worktree" (wadad made it) or "existing"
  dir       TEXT NOT NULL,    -- where agents and terminals run
  title     TEXT NOT NULL,    -- the user's; '' shows the branch or the project's name
  created   INTEGER NOT NULL,
  archived  INTEGER NOT NULL,
  worktree_root TEXT NOT NULL DEFAULT '', -- the checkout dir is in, for a worktree
  owned     INTEGER NOT NULL DEFAULT 0,   -- wadad made worktree_root and removes it
  branch    TEXT NOT NULL DEFAULT '',
  base_ref  TEXT NOT NULL DEFAULT '',     -- what its changes compare against
  pr        INTEGER NOT NULL DEFAULT 0);  -- the pull request it checks out
CREATE UNIQUE INDEX workspaces_active_dir ON workspaces(dir) WHERE archived = 0;
```

- A project has one workspace in its own directory, made with it. No two active
  workspaces share a directory, so a conversation or terminal belongs to the
  workspace at its directory: the active one there, else the one archived there last.
- The inbox project is at `<DataDir>/workspaces/inbox`, the embedded agent's folder
  for conversations started without one. It is made at first start, named Inbox, and
  can't be archived.
- Every change is written before it applies in memory, then announced to every
  all-sessions subscription as a `workspace_updated` event carrying the project or
  workspace. These events are not kept in the timeline: a client lists the record
  again when it connects.
- `workspace.project.add` returns the active project at a root and its workspace;
  if there is none it restores the one archived there last, or else makes one.
  `workspace.project.archive` archives a project and its workspaces; their
  conversations stay with the backend. `workspace.projects.list`,
  `workspace.project.rename`, `workspace.list` and `workspace.rename` complete the
  set. A missing or archived project or workspace is `not_found`.
- Every session the server returns names its workspace (`workspace`). A session in
  a directory no workspace was ever at gets a project there as it is returned, so
  conversations started by the CLI or another client join the record. A failed add
  leaves the session unplaced.
- `agent.session.create`, `agent.sessions.list` and `terminal.create` take
  `workspace` instead of `directory`: it must name an active workspace, and a
  directory given as well must be that workspace's. Terminals report the active
  workspace at their directory.

Worktree workspaces are made by `workspaces.Service` with `worktree.Manager.Add`:

- `workspace.create` (under a creation receipt) takes a project and an isolation.
  `worktree` fetches `origin`, then makes a worktree under
  `<DataDir>/worktrees/<repo hash>/` that branches off a ref (`branch-off`, default
  origin's default branch, as `wada/<slug>`), checks out a branch (`checkout-branch`,
  tracking `origin/<branch>` when it is only remote), or checks out a pull request
  (`checkout-pr`: the head branch for one from the same repository, else
  `pull/N/head` as `wada/pr-N`), and copies the files `.worktreeinclude` lists.
  `existing` records a worktree already on disk. A project inside a repository
  gets the same subfolder of the worktree. A worktree whose recording fails is
  removed again.
- `workspace.archive` refuses a project's own workspace. For a worktree wadad
  made that no other active workspace uses, it lists uncommitted files and commits
  on no remote branch or the base, refuses with `conflict` while there are any
  unless `force`, then removes the worktree; the branch stays. It then aborts the
  conversations and kills the terminals in the directory.
- `workspace.restore` makes an archived workspace active again, recreating a
  worktree wadad made from its branch.
- `workspace.refs` lists a project's default ref, local and remote branches, open
  pull requests (through `gh`) and worktrees; `workspace.changes` diffs a
  workspace against the merge base with its base ref, untracked files included.

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
walked excluding `.git`, `node_modules` and `.cache`, up to 25,000 files; file
search offers what that walk found.

## File changes

Before file write/edit/shell calls, Wada records the target's current bytes and
compares them after execution to identify changes. Mutating calls serialize per
Git repository (or per directory outside Git) for baseline attribution: a write
or edit holds the lock for the whole call, a shell command only while its
before-snapshot is taken. Model streaming, reads, and mutations in distinct
repositories remain concurrent. SDK tool batches execute
sequentially so a later tool's approval/snapshot cannot block an earlier execution.
Pre-existing edits form the baseline, rather than being counted as agent changes.
Baselines persist across launches. The separate uncommitted Git view remains local.

Snapshots skip files that are not regular, larger than 8 MiB, or reached through
a symlink leaving the folder; such a file the agent changes is listed as modified
without a diff. A project workspace over 128 MiB, a walk that stops at 25,000
files, or a failed read marks session changes unavailable, while the permitted
tool action can still execute. Git ignores apply
to workspace snapshots. Session diffs compare tracked baselines with the current files;
subsequent external edits to those same files are reflected too. This is change
tracking, not filesystem isolation.
