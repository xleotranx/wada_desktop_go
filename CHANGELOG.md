# Changelog

## Unreleased

- Native desktop coding workspace connected to the headless `wadad` daemon
  (embedded PiG backend, `wadad.db` idempotent receipts and sequenced timelines),
  plus `wada` CLI and React/TypeScript Web/PWA client (`web/`).
- Chief Agent orchestration across isolated Git worktrees (`worktrees/`) and
  supervised external coding CLI harnesses (Claude Code, Codex, and ACP), with a
  Work panel, Worker Inspector drawer, and `gh` PR status integration.
- Detached cross-platform PTY terminal worker (`wadad-term`) and interactive
  desktop Terminal drawer (`⌘J` / `⇧⌘J`).
- Zero-knowledge E2EE Noise NK relay (`wada-relay`), `#offer=` pairing links,
  daemon password authentication, multi-host Desktop switcher, and Docker
  self-hosting packaging (`docker/`).
- Folder-scoped conversation tabs, file diffs, usage tracking, screenshot
  annotation, read-only side chats, and reviewed Plan → Work → Review workflows.
- Independently versioned MIT-licensed `wada-kit` core and shell.
- Third-party license inventory, secret/vulnerability scans, and multi-platform
  CI/release workflows.

The packaging version is `0.1.0`.
