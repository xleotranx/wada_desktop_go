# Shell and the kit's plugins

Declared in `contract/shell` and `contract/settings`; implemented by `shell`, and by
the `settings` and `inspector` plugins. The model and rules of plugins are in the
[kernel note](kernel-api.md).

## Running an app

An app's main calls `shell.Run` with its `shell.App` and its plugins:

```go
shell.Run(shell.App{
	Name: "Notes", Folder: "wada-kit-notes", Env: "NOTES",
	Width: 900, Height: 640, MinWidth: 600, MinHeight: 400,
}, newNotes, inspector.New, settings.New)
```

- `Name` ends the window title, as "Page — Name".
- `Folder` names the data folder in the user's config directory
  (`~/Library/Application Support/<Folder>/`); a development run (`go run`,
  `mygo dev`) keeps its data in `<Folder>-dev/`. Run refuses an App without a Name,
  or whose Folder is not one plain folder name (no separators, `.` or `..`).
- `Env`, when set, prefixes the environment variables Run reads:
  `<Env>_DATA_DIR` overrides the data folder; `<Env>_STRICT=1` re-panics plugin
  failures; `<Env>_CAPTURE=/path.png` writes a screenshot after startup
  (`<Env>_CAPTURE_WAIT`, default 4s) and quits; `<Env>_APPEARANCE=light|dark` fixes
  the palette without saving it.
- `Width` and `Height` size the window the first time it opens; `MinWidth` and
  `MinHeight` bound it. The frame is restored from then on (`StateKey: "main"`).
- `Menus` are the menus between the app menu and Window, in order.

`shell.New` builds the same shell without a window, for tests and benchmarks: its
`Options` take the Name and Menus, and the kernel's Post, Store and Strict.

The shell gives the kernel a fixed restart policy: a plugin that fails at run time
is set up again, up to 3 times a minute. A toast says "<Title> restarted after an
error" for a restart and "<Title> stopped working" once the plugin stays Failed;
Settings → Plugins shows it as Restarting or Stopped with the error.

## Window

`shell` is the window host. It creates the kernel, opens the settings store, builds
the window and menu bar, and every frame draws what plugins contribute. It contains
no feature. Its own plugin (`shell`, locked) provides `shell.HostKey` and
`kernel.InspectorKey`.

| Point | Drawn |
|---|---|
| `shell.Pages` | the main area, one page at a time; the lowest Order is the default |
| `shell.Strip` | views across the title strip, after the window controls |
| `shell.TitleBar` | items at the end of the title strip |
| `shell.Overlays` | above everything (dialogs) |

The title strip, on `StripBg`, holds the strip views in a row after the window
controls (one with `Grow` takes the remaining width), then the title-bar items. It
drags the window where nothing in it takes the mouse. Without a strip view it shows
the page's `Title` instead. Below it the page fills a panel on `PanelBg`, inset 8 px
with a 12 px radius. The window title follows the page's `Title`.

The window is created transparent. `shell.Host.Backdrop()` (the shell's `backdrop`
setting) says what shows behind the strip and the panel:

- `clear` (the default): the desktop as it is. The window reports no material then,
  so the shell tells the palette with `native.SetClearWindow`, and `PaletteFor`
  makes `StripBg` and `PanelBg` veils of the background and `DrawerBg`, `Hover` and
  `ActiveTab` tints.
- `frosted`: the platform's under-window material, blurred. `ui.Context.Vibrancy()`
  reports it, and `PaletteFor` clears `StripBg`, veils `PanelBg` and `ActiveTab`, and
  tints `DrawerBg` and `Hover`.
- `solid`: the opaque palette.

Whenever `StripBg` is not opaque the root draws no background. `Bg` stays solid for
cards, bubbles and inputs. The `appearance` setting is System, Light or Dark (the
default).

`shell.Host.ShowPage` emits `shell.PageChanged` with the ID it chose whenever that
differs from the last one.

Every render, command and title callback runs through `kernel.Invoke`, so a panic
fails only that plugin, which the restart policy below brings back.

Commands with a `Menu` appear in the menu bar under that name, in the order of
`App.Menus` (File, View, Help without it), and the menu owns their first shortcut.
File is followed by the standard Edit menu, View ends with the developer tools, and
Window comes last. Other shortcuts are window shortcuts. The menu is rebuilt
whenever plugins change state and whenever the commands change, as when a plugin
contributes one from a scope it opens later.

`shell.Host` gives plugins the window services they need: show a page, toasts,
clipboard, opening links, desktop notifications (`Notify`), whether the window is
in front (`Foreground`) and the app's data folder (`DataDir`, where `settings.db`
lives; a plugin names its files after its ID; empty in tests). A clicked notification
brings the window to the front, then its ID is emitted on `shell.NoticeClicked`. The
app's notifications are cleared when it becomes active. On macOS they need a
packaged app (`mygo dev` or `mygo build`); under `go run` nothing is shown.

## Palette

`native.PaletteFor` is where every color comes from: the theme's colors, the strip,
panel and drawer, tints for hover and the active tab, and the project colors. An
app's test calls `kittest.ColorsStayInThePalette` on its tree, which fails on a
`ui.Hex` or `ui.RGBA` outside the kit's `native/palette.go`.

## Settings

`plugins/settings` contributes the settings page and "Settings…" (⌘,). It lists
`settings.Sections` entries in order in a navigation column and renders only the
selected section in a separately scrolling detail pane. If that section disappears,
it shows the first available section. It adds its own Appearance and Plugins
sections; appearance offers System, Light and Dark, each drawn as a small window in
that theme (`native.PaletteOf`): the strip with a project's tabs over the panel,
System half of each, and a Window background choice of Clear, Frosted or Solid. The
plugin list groups Optional features above Required plugins, preserving installation
order within each group. Optional features have switches to turn them on and off
through `shell.Host`; required plugins have a Required badge.

## Plugin diagnostics

The `inspector` plugin adds View → Plugin diagnostics and a button in Settings.
Its read-only page lists every installed plugin. Expanding one shows required
service providers, missing requirements, owned registrations, scope and worker
counts, the time its callbacks took on the UI thread (in all, how many, the
slowest), its recent restarts, and any failure and recovered panic stack. It uses the public
`kernel.InspectorKey` service and shell page contributions.
