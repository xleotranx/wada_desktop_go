# Changelog

## Unreleased

- Added daemon-owned projects and workspaces shared by desktop, web, and CLI.
  Worktree workspaces support branch and PR checkout, archive and restore,
  configurable storage and branch prefixes, and model-drafted commits and PRs.
- Added workspace Files, Changes, Terminal, and Browser tabs. Desktop can keep
  a pane beside chat. The web Files view can show chat beside its editor.
- Added trusted workspace setup scripts and services with per-workspace ports.
  Agents can request approved browser actions in local workspace services.
- Added agent profiles, supervised workers, and Chief tools for external CLI
  harnesses over loopback MCP, plus daemon-run schedules and heartbeats.
- Added SSH hosts and web project registration, worktree creation and adoption,
  archive and restore, schedule editing, and an installable PWA shell.
- Added dictation, screenshot markup, and read-only side chats.

For setup and current workflows, see the [README](README.md) and the
[workspace guide](docs/workspaces.md).
