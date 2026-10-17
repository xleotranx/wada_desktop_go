# AGENTS.md — wada_desktop_go

A single-package MyGo desktop app: all native UI in Go (`ui`), no webview — a design mock of the ChatGPT desktop client. MyGo is pinned in `go.mod`; docs: https://github.com/egoist/mygo/blob/main/docs/README.md

Layout: `main.go` (state, window options, `view()`) → `sidebar.go`, `chat.go`, `composer.go` (panes), `palette.go` (colors), `icons.go` (SVGs), `main_test.go` (view tests).

## Commands

This is a `mygo init -template native` project: the CLI is pinned as a Go tool in `go.mod`, so run it as `go tool mygo <cmd>` at that pinned version — no Bun/npm, no installed `mygo`. Of the CLI's commands only `dev`, `vet`, `build` and `doctor` matter here; `generate`, `i18n` and `keygen` serve web frontends and update channels this app doesn't use (no bindings, no catalogs).

- `go tool mygo dev .` — run the app with live reload: any `.go` edit rebuilds and relaunches, and a build that fails to compile keeps the running one; `q` + Enter or Ctrl+C quits, `r` + Enter forces a rebuild. Dev builds live in gitignored `.mygo/dev/` and appear as the `wada_desktop_go Dev` process; killing the `go tool mygo` process stops the app as well. The `fatal: your current branch … does not have any commits yet` git line at startup is harmless.
- `go run .` — plain instance, no live reload.
- `go test ./...` — single test: `go test -run TestChooseChat ./...`.
- `go tool mygo vet .` — checks Go vet **and** native-UI build lifetimes. No lint/CI is configured; this plus tests is the gate. There is no `test` subcommand under `go tool mygo` — use `go test`.
- `go tool mygo build` — production `.app`/`.dmg` into `build/` (gitignored; the `wada-desktop-go` binary at the root is just a quick `go build -o` artifact).

## Verifying UI changes

- `WADA_CAPTURE=/path/out.png go run .` — writes a PNG of the window content and quits. It shows only what the view draws: macOS traffic lights are native chrome and never appear. Iterate layout with this instead of clicking around.
- `StateKey: "main"` restores the window's position/size from the last run — don't assume it is centered when screenshotting live; ask System Events / `screencapture -R x,y,w,h` for the real frame.
- View tests run headless against the real view: `ui.NewTester(a.view, 1400, 920)` (see `main_test.go`). Prefer adding behaviors there over manual repro.
- Dev builds include the native-UI inspector: `F12` or `Alt+Cmd+I` opens the element tree, `Shift+Cmd+C` picks an element in the window. Its Computed tab gives exact element boxes (size and position), which beats pixel-measuring screen captures for alignment work.

## Custom title bar (macOS gotcha)

- The window is `TitleBarHidden`; the view draws its own top rows. On macOS, `c.TitleBar()` measures the strip the traffic lights live in: `Height = traffic-light button height + 2 × TrafficLightPosition.Y`, controls are vertically centered in that strip, and `Left` is the width they occupy (used as the nav row's left padding).
- `main.go` derives one `headerH := max(bar.Height, 48)` shared by the sidebar's nav row and the content title bar, and `TrafficLightPosition` is chosen so the lights share that center (close button is 14 DIP tall; `Y: 25` → 64 strip, everything centered at 32). If you change one side of that pairing, recompute the other or the dots visibly drift.

## Conventions

- Route all colors through `paletteFor` (light/dark palettes in `palette.go`); don't scatter `ui.Hex` into panes.
- Icons are single-color SVGs recolored by the text color; `ui.Icon(...).FontSize(n)` is the icon size. The one multicolor icon (`icBooks`) goes through `ui.Image`, not `ui.Icon`.
- The transcript in `chat.go` — including fake shell output like `go tool mygo test ./...` — is demo copy, not toolchain documentation.
- Comments are compact prose sentences; match that voice.

## Where the deep MyGo rules live

- Repo-local skill `.agents/skills/mygo-maintenance/SKILL.md` — element lifetime, focus/handle identity, threading and migration pitfalls. Read it before nontrivial `ui` work.
- Module source: `go list -m -f '{{.Dir}}' github.com/egoist/mygo` (read `ui/doc.go`).
