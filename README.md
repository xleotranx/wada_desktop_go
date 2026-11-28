# Wada

A native desktop coding-agent workspace: explore codebases, edit files, run commands, and inspect diffs beside the conversation. Built with Go, MyGo and Onevra; the headless `wadad` daemon embeds PiG so coding turns keep running even when the desktop window closes.
Chat and controls are native.

![Wada's native desktop chat interface](docs/images/new_dekstop.png)

Wada is development software. macOS is the tested platform. Windows compilation
is checked in CI, but interactive Windows behavior has not been verified. There
is no signed, notarized public binary release yet; build from source below.

## Run

Install the Go version in [go.mod](go.mod), Python 3 for repository checks, and
Apple's Command Line Tools (`xcode-select --install`) on macOS. The first run needs
network access to download Go modules and MyGo's native libraries.

```sh
git clone https://github.com/xleotranx/wada_desktop_go.git
cd wada_desktop_go
go mod download
go tool mygo dev .       # live reload; q + Enter quits, r rebuilds
```

`go run .` also runs the app. Connect your own provider in **Settings → Providers**
using an API key or supported browser sign-in. Tests use fake providers and need
no API credentials. Provider usage is subject to that provider's pricing and terms.

To build an app and disk image locally:

```sh
make package            # checks notices; output under build/; ad-hoc signing
```

A distributable macOS binary needs the maintainer's signing and notarization
configuration. This repository does not supply those credentials.

The public [wada-kit](https://github.com/xleotranx/wada-kit) module is pinned in
`go.mod` and downloaded through normal Go module resolution. It needs no GitHub
credentials, deploy key, or `GOPRIVATE` configuration.

For local kit work and version upgrades, see
[kit dependency maintenance](docs/design/kit-dependency.md).

## Features

Chat with streaming markdown, tool calls, approvals and stop; open chats and Settings as tabs across the title strip, one folder's space at a time with a
switcher for the others, each tab showing whether its conversation is working, waiting for you or unread; All
chats (⌘K) to find any chat the app started (its own record, in SQLite), folder or
command; folders picked with ⌘O, All chats or the composer; a work panel beside a
conversation with its context, session or uncommitted Git changes with expandable diffs, cost, and supervised CLI workers; workspaces in Git worktrees made from a branch or pull request (⇧⌘N), with their branch, `gh` PR status and changes since the base, archived with a check for unsaved work and restored from their branch, named from their first message, with model-written commit messages and pull requests; a `wada.json` whose setup runs in each new worktree and whose services each get a port of the workspace's own, once you trust it; interactive PTY terminals (⌘J / ⇧⌘J) and a workspace's changes (⇧⌘D) as tabs beside its chats;
model, effort and mode pickers; file/image attachments, `@file`/`@folder` mentions, and `/skill` completions; external CLI harnesses (Claude Code, Codex, ACP) and remote access over Noise NK E2EE relay, Web/PWA, or `wada` CLI; desktop notifications for conversations in the background; schedules and heartbeats the daemon runs on a cron or an interval; web pages as tabs, such as a workspace's running service; dictation into the composer; settings
for appearance, conversation, tabs, daemon, providers, models, notifications, diagnostics, and turning plugins
on and off.

Select text in a conversation for **Start a side chat** or **Reply**. A side chat
opens in a drawer beside the conversation with the selection quoted in its own
composer; it can read files but not change them, and **Add to main chat** puts an
answer in the original chat's draft.

For visual instructions, capture the screen (⌘⇧2 / ⌃⌘⇧2 on macOS) or paste/attach a screenshot and choose **Mark up**
beside its image chip. Draw boxes, arrows, pins, freehand lines, highlights, or pixel redactions, add per-mark notes and optional full-resolution detail crops, then
choose **Use marked-up image**. The marked PNG goes with your next message;
the source file stays unchanged. Images are limited to 16 megapixels and 16,384
pixels per side, in addition to the attachment byte limits.

Ordinary PiG chats use PiG's built-in coding tools (`read`, `bash`, `edit`, `write`, `grep`, `find`, `ls`) and Chief Agent orchestration tools (`list_providers`, `list_profiles`, `create_workspace`, `list_workspaces`, `archive_workspace`, `spawn_agent`, `send_agent`, `wait_agent`, `inspect_agent_diff`, `stop_agent`) inside the selected project folder; external Claude Code, Codex and ACP workers reach the same tools through a loopback MCP server, each with its own token. Wada does not automatically load repository agent instructions; skills are included when explicitly selected. Without a selected folder, new conversations use a dedicated Inbox rather than the user's home folder.

The composer offers **Ask before changes** (the default) and **Work independently**.
Ask mode permits reads and searches, and asks before edits, shell commands, or web requests.
Independent mode performs those operations within the selected project folder.

## Architecture

| Package | Responsibility |
| --- | --- |
| `github.com/xleotranx/wada-kit` | Independently released kit: kernel, window shell, color palette, settings page and plugin diagnostics ([repository](https://github.com/xleotranx/wada-kit)) |
| `main.go` | Wada's name, window and menus, `--daemon` / `--terminal-worker` dispatch, and the plugins this build installs |
| `cmd/wadad` | Standalone headless coding-agent daemon (`wadad`) |
| `cmd/wadad-term` | Detached cross-platform PTY terminal worker (`wadad-term`) surviving daemon restarts |
| `cmd/wada-relay` | Stateless zero-knowledge Noise NK WebSocket relay (`wada-relay`) for remote/mobile pairing |
| `cmd/wada` | CLI client (`wada`) talking to local or remote `wadad` (`ls`, `run`, `logs`, `abort`, `delete`, `project`, `workspace`, `providers`, `workers`, `pr`, `terminal`, `daemon`, `host`) |
| `internal/protocol` | Append-only WebSocket JSON envelopes, RPC methods, event streams, and binary frame codecs |
| `internal/relay` | Zero-knowledge `Noise_NK_25519_ChaChaPoly_SHA256` tunneling, `#offer=` pairing codecs, and stateless relay router |
| `docker/` | Self-hosted `wada-relay` + Caddy TLS and `wadad` Dockerfiles & Compose stack ([self-hosting guide](docs/self-hosting.md)) |
| `web/` | Responsive React/TypeScript PWA (`Noise NK` E2EE + direct WS client, sessions by project and workspace, project registration with host folder suggestions, creating and opening worktrees, workspace archive and restore, and their `wada.json` setup and services, schedules and heartbeats, agent profiles, dictation, steer/queue/interrupt, chat/approvals/diffs/terminal views, an offline shell and in-app updates; optional `wadad --web-dir`) |
| `internal/client` | Go WebSocket & Noise NK relay client SDK, `ssh://` hosts over `wadad --stdio-bridge`, saved remote `hosts.json`, binary terminal streams, and local daemon attach/auto-spawn helpers |
| `internal/daemon/*` | Headless `wadad` WebSocket server, idempotency/timeline/project SQLite store (`wadad.db`), projects and workspaces registry, Git worktree manager, CLI harnesses (ACP, Claude Code, Codex), detached PTY/ConPTY terminal supervisor, orchestrator, and embedded PiG runtime |
| `internal/contract/*` | Declarations Wada's plugins share: `agent`, `attachments`, `chat`, `tabs`, `terminal`, `workspace` |
| `internal/plugins/daemon` | Connects Desktop to local or remote `wadad` (`agent.*`, `terminal.*`, pairing, multi-host settings, and provider auth) |
| `internal/plugins/workers` | Composer harness picker, live supervised worker section, and Worker Inspector drawer |
| `internal/plugins/worktrees` | New, Archive and Restore Workspace… dialogs, and the work panel's Workspace section (branch, base, PR badge) |
| `internal/plugins/terminal` | Interactive PTY terminals as tabs in each workspace (`⌘J` / `⇧⌘J`), backed by `wadad-term` |
| `internal/plugins/chat` | Conversation page: transcript, composer, approvals |
| `internal/plugins/models` | Model and effort pickers in the composer |
| `internal/plugins/modes` | Access mode selection |
| `internal/plugins/usage` | Context and cost, in the work panel or the composer |
| `internal/plugins/filechanges` | Session or local uncommitted Git changes and expandable diffs below Context |
| `internal/plugins/attachments` | Files and images sent with a message |
| `internal/plugins/markup` | Screen capture and annotated screenshots (boxes, arrows, pins, lines, highlights, redaction), exported as PNG attachments |
| `internal/plugins/mentions` | `@file` and `@folder` completions and contained file attachments in the composer |
| `internal/plugins/skills` | `/skill` completions and skill attachments in the composer |
| `internal/plugins/workspace` | The app's copy of the daemon's projects, workspaces and their conversations |
| `internal/plugins/folderpicker` | Add Project and the new conversation's workspace picker |
| `internal/plugins/tabs` | Tabs across the title strip, one workspace's space at a time; the switcher and menus |
| `internal/plugins/explore` | Read-only side chats in a drawer, started from selected text, with answers added to the original draft |
| `internal/plugins/palette` | All chats (⌘K): conversations, folders and commands |
| `internal/plugins/starters` | Folder-aware coding starters on new chats |
| `internal/plugins/notifications` | Desktop notices for conversations in the background |

Design notes: [kernel API](https://github.com/xleotranx/wada-kit/blob/v0.4.0/docs/kernel-api.md), [kit shell](https://github.com/xleotranx/wada-kit/blob/v0.4.0/docs/shell.md),
[agent contract](docs/design/agent-contract.md),
[shell and built-in plugins](docs/design/shell-and-plugins.md),
[self-hosting guide](docs/self-hosting.md).

## Extend

Write a type that implements `kernel.Plugin`, register what it adds in `Setup`, and
list its factory in `main.go` (`notes.New`); the kernel makes a new one for each
activation:

```go
type Notes struct{ host shell.Host }

func New() kernel.Plugin { return &Notes{} }

func (*Notes) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "notes", Title: "Notes", Requires: []kernel.Named{shell.HostKey}}
}

func (n *Notes) Setup(c *kernel.Context) error {
	n.host = kernel.Require(c, shell.HostKey)
	kernel.Contribute(c, shell.Pages, "notes.page", 50, shell.Page{Render: renderNotes})
	kernel.Contribute(c, kernel.Commands, "notes.open", 0, kernel.Command{
		Title: "Open Notes", Menu: "View", Keys: []string{"cmd+shift+n"},
		Run: func() { n.host.ShowPage("notes.page") }})
	return nil // a registration that failed fails the Setup by itself
}
```

A new backend is a plugin that provides `agent.ServiceKey` (see the agent contract);
the `daemon` plugin is the built-in backend installed by this app, talking to `wadad` (embedded PiG plus supervised CLI harnesses).

## Local data and permissions

Production settings and app records live under
`~/Library/Application Support/wada-desktop-go/`; `go run` and `mygo dev` use
`wada-desktop-go-dev/`. Set `WADA_DATA_DIR` to choose another directory.

- `settings.db`: settings;
  `wadad.db`: daemon identity, idempotency receipts, sequenced event timelines, and projects and workspaces.
- `wadad.pid`, `wadad.lock`, `local-credential`, `wadad.log`: local daemon discovery, lock, and authentication files.
- `daemon-keypair.json`, `remote-token`, `password.hash`, `hosts.json`: E2EE relay identity keys, optional remote password hash, and saved remote host profiles.
- `worktrees/`: the default location for new Git worktrees. Use `wadad --worktree-dir /absolute/path` or set `WADA_WORKTREE_DIR` in the daemon's environment to place new worktrees elsewhere on the host. Restored workspaces use their recorded locations.
- `terminal-worker/`: detached PTY worker IPC descriptors.
- `pig/sessions/`: conversation transcripts; `pig/metadata/`: app session metadata;
  `pig/baselines/`: lazy on-disk session diff baselines; `pig/agent/`: provider credentials and configuration.

New worktree branches use the `wada/` prefix by default. Set `wadad --branch-prefix`
or `WADA_BRANCH_PREFIX` to change it, or use an empty value to omit it.

Treat these directories as sensitive. Messages, attachments and tool results sent
to a model go to your chosen provider. Do not include credentials or private
conversation content in issues or screenshots. **Ask before changes** is a tool
permission policy, not an operating-system sandbox. See [SECURITY.md](SECURITY.md).

For experiments and captures, isolate the app's data:

```sh
WADA_DATA_DIR="$(mktemp -d)" WADA_CAPTURE=/tmp/wada.png go run .
```

Database schemas are currently version 1. Unsupported schemas are refused rather
than migrated; back up your data before testing a different build.

## Verify and contribute

```sh
GOWORK=off make check   # lifetime vet, lint, notices, race and no-cgo tests
GOWORK=off go build ./...
make audit             # known Go vulnerabilities
make secrets           # committed source history
make web-test          # web client tests and TypeScript typecheck
make web-build         # web client production bundle
```

Tests run plugins in `kerneltest` harnesses with fakes from `internal/plugintest`,
the kit's `kittest`, and native views with `ui.NewTester`. `make demo` starts the
app with synthetic, throwaway data. See [CONTRIBUTING.md](CONTRIBUTING.md) for
setup, focused changes and packaging, and [CHANGELOG.md](CHANGELOG.md) for scope.

## License

Wada is [MIT licensed](LICENSE). Dependencies retain their own licenses. The
[third-party inventory and full notices](resources/licenses/README.md) ship in
packaged builds; run `make licenses` after changing dependencies.
