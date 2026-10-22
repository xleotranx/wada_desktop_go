# wada-kit

The core and window of a native MyGo desktop app, without the app: a plugin kernel,
a window shell (title strip, rounded panel, clear or frosted backdrop, light and dark
appearance, menus, shortcuts, a SQLite settings store), the palette every pane
paints with, a settings page and plugin diagnostics. An app adds its own plugins and
contracts on top. Wada is built on it, beside it in this repository.

| Package | What it is |
| --- | --- |
| `kernel` | Plugin lifecycle, services, events, pipelines, extension points, commands, tasks, settings; standard library only |
| `kernel/kerneltest` | A kernel for tests, with background work and posts the test runs |
| `contract/shell` | The window's slots (`Pages`, `Strip`, `TitleBar`, `Overlays`) and `shell.Host` |
| `contract/settings` | Settings sections |
| `shell` | The window host: `shell.Run(app, plugins...)`, and `shell.New` for tests |
| `native` | The palette (`native.PaletteFor`) and small shared controls |
| `plugins/settings` | The settings page, with Appearance and Plugins |
| `plugins/inspector` | Plugin diagnostics |
| `kittest` | A fake `shell.Host` and the palette guard for an app's tests |
| `examples/notes` | The smallest app on the kit |

Notes: [kernel API](docs/kernel-api.md), [shell and the kit's plugins](docs/shell.md).

## Use

```go
shell.Run(shell.App{Name: "Notes", Folder: "notes", Env: "NOTES", Width: 900, Height: 640},
	&notes{}, &inspector.Plugin{}, &settings.Plugin{})
```

`go run ./examples/notes` runs the example. An app beside the kit requires
`github.com/xleotranx/wada-kit` and replaces it with the folder, as Wada does:

```
require github.com/xleotranx/wada-kit v0.0.0
replace github.com/xleotranx/wada-kit => ./wada-kit
```

In its own repository, `git subtree split --prefix wada-kit` carries this folder
with its history; an app then drops the replace and requires a tagged version. The
import path stays the same.

## Check

`make check`: MyGo lifetime vet, pinned lint (the import boundaries; nothing here
imports an app), race tests and no-cgo tests.
