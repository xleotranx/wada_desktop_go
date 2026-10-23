# wada_desktop_go

A native macOS AI chat app built with Go, MyGo and Onevra, with no webview. The app
is a small plugin kernel; the window, the backend and every feature are plugins.
OpenCode is the default backend; Pi is the other one.

## Run

Requires macOS, the Go version in `go.mod`, and the OpenCode CLI on `PATH` (or an
OpenCode server to connect to from Settings). To use Pi instead, install the `pi` CLI,
then in Settings → Plugins turn OpenCode off and Pi on.

```sh
go tool mygo dev .       # live reload; q quits, r rebuilds
go run .
go tool mygo build       # production app/DMG in build/
```

## Features

Chat with streaming markdown, tool calls, approvals and stop; open chats, workflow
runs and Settings as tabs across the title strip, one folder's space at a time with a
switcher for the others, each tab showing whether its conversation is working, waiting for you or unread; All
chats (⌘K) to find any chat the app started (its own record, in SQLite), folder or
command; folders picked with ⌘O, All chats or the composer; a work panel beside a
conversation with the files it changed, its last command, its context and cost;
model, effort and mode pickers; file and image attachments; workflows such as plan,
code, review; desktop notifications for conversations in the background; settings
for appearance, tabs, the OpenCode connection, Pi, notifications, and turning plugins
on and off.

## Architecture

| Package | Responsibility |
| --- | --- |
| `wada-kit/` | The kit, its own Go module: kernel, window shell, palette, settings page, plugin diagnostics ([README](wada-kit/README.md)) |
| `main.go` | Wada's name, window and menus, and the plugins this build installs |
| `internal/contract/*` | Declarations Wada's plugins share: `agent`, `chat`, `tabs`, `workspace`, `workflow` |
| `internal/plugins/opencode` | OpenCode backend: managed or remote server, HTTP/SSE, reconnects |
| `internal/plugins/pi` | Pi backend: RPC processes per session, session files |
| `internal/plugins/chat` | Conversation page: transcript, composer, approvals |
| `internal/plugins/models` | Model, effort and mode pickers in the composer |
| `internal/plugins/usage` | Context and cost, in the work panel or the composer |
| `internal/plugins/activity` | Changed files and the last command, in the work panel |
| `internal/plugins/attachments` | Files and images sent with a message |
| `internal/plugins/workspace` | Folders and the conversations started here, in `workspace.db`; folder picker |
| `internal/plugins/tabs` | Tabs across the title strip, one folder's space at a time; the switcher and menus |
| `internal/plugins/palette` | All chats (⌘K): conversations, folders and commands |
| `internal/plugins/workflow` | Runs flows through hidden conversations; a tab per run |
| `internal/plugins/flows` | The app's flows, such as plan, code, review |
| `internal/plugins/notifications` | Desktop notices for conversations in the background |

Design notes: [kernel API](wada-kit/docs/kernel-api.md), [kit shell](wada-kit/docs/shell.md),
[agent contract](docs/design/agent-contract.md),
[shell and built-in plugins](docs/design/shell-and-plugins.md).

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
enable one backend at a time in Settings → Plugins.

## Verify

```sh
make check   # MyGo lifetime vet, lint, race tests and a no-cgo test, for Wada and the kit; also CI
WADA_DATA_DIR=/tmp/wada-preview WADA_CAPTURE=/tmp/wada.png go run .
```

Tests run plugins in `kerneltest` harnesses with fakes from `internal/plugintest` and
the kit's `kittest`, and
native views with `ui.NewTester`.
