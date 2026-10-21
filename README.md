# wada_desktop_go

A native macOS AI chat app built with Go, MyGo and Onevra, with no webview. The app
is a small plugin kernel; the window, the backend and every feature are plugins.
OpenCode is the current backend.

## Run

Requires macOS, the Go version in `go.mod`, and the OpenCode CLI on `PATH` (or an
OpenCode server to connect to from Settings).

```sh
go tool mygo dev .       # live reload; q quits, r rebuilds
go run .
go tool mygo build       # production app/DMG in build/
```

## Features

Chat with streaming markdown, tool calls, approvals and stop; a searchable, paged
conversation list with rename, delete and unread marks; settings for appearance,
the OpenCode connection, and turning plugins on and off.

## Architecture

| Package | Responsibility |
| --- | --- |
| `main.go` | The plugins this build installs |
| `internal/kernel` | Plugin lifecycle, services, events, pipelines, extension points, tasks, settings |
| `internal/contract/*` | Declarations plugins share: `agent`, `chat`, `shell`, `settings` |
| `internal/shell` | Window host: frame, slots, menus, shortcuts, SQLite settings store |
| `internal/plugins/opencode` | OpenCode backend: managed or remote server, HTTP/SSE, reconnects |
| `internal/plugins/chat` | Conversation page: transcript, composer, approvals |
| `internal/plugins/sessions` | Sidebar conversation list |
| `internal/plugins/settings` | Settings page |
| `internal/native` | Palette and small shared controls |

Design notes: [kernel API](docs/design/kernel-api.md),
[agent contract](docs/design/agent-contract.md),
[shell and chat](docs/design/shell-and-chat.md).

## Extend

Write a type that implements `kernel.Plugin`, register what it adds in `Setup`, and
list it in `main.go`:

```go
type Notes struct{}

func (Notes) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "notes", Title: "Notes", Requires: []kernel.Named{shell.HostKey}}
}

func (Notes) Setup(c *kernel.Context) error {
	host := kernel.Require(c, shell.HostKey)
	kernel.Contribute(c, shell.Pages, "notes.page", 50, shell.Page{Render: renderNotes})
	return kernel.Contribute(c, kernel.Commands, "notes.open", 0, kernel.Command{
		Title: "Open Notes", Menu: "View", Keys: []string{"cmd+shift+n"},
		Run: func() { host.ShowPage("notes.page") }})
}
```

A new backend is a plugin that provides `agent.ServiceKey` (see the agent contract);
enable one backend at a time in Settings → Plugins.

## Verify

```sh
make check   # MyGo lifetime vet, lint, race tests and a no-cgo test pass; also CI
WADA_DATA_DIR=/tmp/wada-preview WADA_CAPTURE=/tmp/wada.png go run .
```

Tests run plugins in `kerneltest` harnesses with fakes from `internal/plugintest`, and
native views with `ui.NewTester`.
