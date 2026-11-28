# Working in Wada

A **project** is a folder on the selected daemon's machine. A **workspace** is a
working directory for that project. Each project has a workspace for its main
folder. Git projects can also have workspaces in new or existing worktrees.
Chats and terminals in a workspace share its files. The daemon owns the project
and workspace records, so desktop, web, and CLI see the same state.

## Add a project and start work

1. On desktop, choose **File → Add Project…** (⌘O). Use the system folder picker
   for the local daemon, or type an absolute path for a remote daemon. The remote
   dialog suggests matching directories on the host. In the web sidebar, use
   **Add project** and enter the folder on the connected host.
2. Use the project's main workspace to work in the original checkout. To isolate
   a task, choose **File → New Workspace…** (⇧⌘N) on desktop or the project's
   **New workspace** action on the web.
3. Choose a new branch from a base ref, check out a branch or pull request, or
   open an existing worktree. Open the workspace to start a chat there. Chats
   started without a selected folder use the daemon's Inbox.

To do the same from the CLI, use the project and workspace IDs printed by
these commands:

```sh
wada project add /absolute/path/to/repo
wada workspace create --project PROJECT_ID --base main --slug review
wada run --workspace WORKSPACE_ID "Review the changes in this workspace"
```

Replace `main` with a base ref in the repository. Add `--host NAME` before the
command for a saved remote daemon. Paths are resolved on the daemon host.

## Review and test

| Action | Desktop | Web |
| --- | --- | --- |
| Open a terminal | **View → Show Terminal** (⌘J) or **New Terminal** (⇧⌘J) | **Terminal** tab |
| Inspect changes | **View → Show Changes** (⇧⌘D), or the conversation's work panel | **Diffs** shows changes recorded for the current chat |
| Edit a workspace file | **File → Workspace Files** (⇧⌘E) | **Files** tab |
| Keep chat visible while working | Right-click a Files, Changes, Terminal, or Browser tab and choose **Show Beside Chat** | **Chat beside files** in the Files tab |
| Open an embedded browser | **File → Open Address…** (⇧⌘L), or open a running service from the Workspace section | Open the service link when the daemon's service address is reachable |

The editor accepts regular UTF-8 files up to 256 KiB. Paths are relative to the
workspace. If the file changes on disk before you save, Wada keeps your draft
and reports a conflict. Copy your edits before using **Discard draft** and
**Reload** to get the latest version.

On desktop, **File → Commit…** and **File → Create Pull Request…** open drafts
you can edit before confirming. The CLI equivalents are `wada workspace commit`
and `wada workspace pr`. Creating a pull request requires `gh` authentication
for the repository's host and pushes the branch before creating the PR.

## Setup and services

Add a `wada.json` at the workspace's working directory. For example, a Node
project whose development script accepts Vite's `--port` flag can use:

```json
{
  "setup": "npm ci",
  "services": {
    "web": "npm run dev -- --host 127.0.0.1 --port \"$PORT\""
  }
}
```

Review the commands in the Workspace panel before trusting the file. Trust is
recorded per project for the file's exact contents. Editing the file requires
trust again. Wada gives each workspace with services a block of ten ports and
passes the assigned service port in `PORT`. The service command must use that
port. Start and stop services from the Workspace panel or with
`wada workspace start WORKSPACE_ID SERVICE` and
`wada workspace stop WORKSPACE_ID SERVICE`.

New desktop worktrees can run setup as part of creation once trusted. Setup can
also be launched from the Workspace panel or with
`wada workspace setup WORKSPACE_ID`. An optional trusted `teardown` command runs
before Wada removes its worktree during archive. See the
[wada.json reference](design/agent-contract.md#wadajson) for the remaining fields
and environment variables.

On the local desktop, you can ask the agent to use `workspace_preview` to open a
path, read visible text, or click a uniquely labeled button outside forms in a
running, trusted service in its workspace. Each request requires **Allow once**
in the desktop. Keep the requesting workspace selected to see the request.

## Archive and restore

Use **File → Archive Workspace…** on desktop or **Archive** in the web
Workspace panel. Archiving a Wada-owned worktree checks for unsaved files,
unpushed commits, and teardown errors before removing its checkout.
The conversation history and branch remain available. Archiving an existing
worktree leaves its checkout on disk. To archive the project's main workspace,
remove the project on desktop or use `wada project rm PROJECT_ID`.

Use **File → Restore Workspace…** on desktop or **Restore workspace** in the
web sidebar. Wada recreates an owned worktree from its branch at the recorded
path. The CLI equivalents are:

```sh
wada workspace ls --archived
wada workspace archive WORKSPACE_ID
wada workspace restore WORKSPACE_ID
```

Changes to the daemon's [worktree directory and branch prefix](self-hosting.md#worktree-configuration)
apply when creating new checkouts and branches.
