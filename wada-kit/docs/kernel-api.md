# Kernel API

`kernel` is the plugin core: plugin lifecycle, scoped and reversible
registration, services, events, pipelines, extension points, commands, background
tasks and settings. It imports only the standard library and knows no feature or UI.
Signatures and exact behavior are documented in the package itself; this note gives
the model and the rules.

## Layers

```text
an app's main        composition: shell.Run with the app's plugins
an app's plugins     every feature; the kit's plugins/* are some of them
shell                the window host: kernel, settings store, layout, menus, shortcuts
contract/*           declarations shared between plugins (an app adds its own)
kernel               plugin core, standard library only
```

A contract package (`contract/<name>`, in the kit or an app) holds only
declarations: types, interfaces and the `Key`, `Topic`, `Pipeline` and `Point`
values plugins use to extend or consume a capability. No implementation, no state.

Import rules, enforced by depguard in the kit's and the app's `.golangci.yml`:

- `kernel` imports only the standard library.
- `contract/*` imports `kernel`, other contracts and MyGo `ui`.
- `shell` imports `kernel`, `contract/*`, `native`, MyGo and Onevra; never a plugin.
- A plugin imports `kernel`, `contract/*`, `native`, MyGo and Onevra; never another
  plugin and never `shell`.
- Only an app's main imports plugin packages and `shell`.
- Nothing in the kit imports an app.

## Concepts

| Concept | What it is | Many or one |
|---|---|---|
| `Plugin` | `Manifest()` plus `Setup(*Context) error` | — |
| `Factory` | `func() Plugin`: what `Install` takes; it makes a new plugin for each activation | one per installed plugin |
| `Context` | one plugin's handle and the root `Scope` of its registrations | one per active plugin |
| `Scope` | a lifetime whose registrations, workers and posts end together | any number (`NewScope`) |
| `Key[T]` | a service: `Provide`, `Require`, `Use` | exactly one provider |
| `Topic[T]` | an event: `On`, `Emit` | many listeners |
| `Pipeline[T]` | ordered transforms over a value: `Transform`, `Run` | many transforms |
| `Point[T]` | a collection of entries: `Contribute`, `Items`, `Invoke` | many entries |
| `Commands` | the kernel's own `Point[Command]`: title, menu, accelerators, run | many |
| `Task` | a replaceable background job (`Go`); stale results are dropped | — |
| `Settings`, `Setting[T]` | JSON values namespaced to the plugin; `Setting.Get` keeps what it decoded until a setting changes | one namespace per plugin |

Anything with many contributors is a `Point`; a `Key` has one provider.

## Manifest

- `ID` matches `[a-z][a-z0-9-]*` and prefixes every entry the plugin contributes
  (`notes.new`, `settings.page`). `kernel` and `app` are reserved; entries the
  host contributes through the `Kernel` itself start with `HostPrefix` (`app.`).
- `Provides` lists the keys Setup must provide; `Requires` the keys that must have a
  provider before Setup runs. Requirements are keys, not plugin IDs: a plugin that
  requires `shell.HostKey` doesn't care which plugin provides it.
- `Locked` plugins can't be disabled. `DefaultOff` plugins start disabled until the
  user enables them once.

## Lifecycle

```text
Install ──► Disabled ──Enable──► (requirements met?) ──no──► Blocked
                ▲                       │ yes                   │ provider appears
                │                     Setup ──error/panic──► Failed
             Disable                    │ ok                    │ Enable retries
                └────────────────── Active ◄────────────────────┘
```

1. **Install** takes factories and reads manifests only: it calls each factory once
   and keeps the plugin it makes for its first activation. It rejects a factory that
   makes no plugin, malformed or duplicate IDs, a plugin requiring a key it provides,
   and requirement cycles.
2. **Start** loads the store, then sets up enabled plugins in install order, repeating
   while a setup provides something new, so providers come before their consumers.
   Then it emits `Started` and `PluginsChanged`.
3. Every later activation calls the factory again, so a Setup always starts on a new
   plugin with nothing left of an earlier activation; state that must outlive one
   (such as a backend's count of connection attempts) lives outside the plugin. A
   factory that makes a plugin with another ID fails the activation.
4. A Setup that errors or panics, that declares a key it didn't provide, or during
   which a registration through its context failed (`Provide`, or `Contribute`, whose
   errors Setup need not check), has its registrations undone and the plugin is
   Failed. A plugin whose required provider
   went down while its Setup ran, as when a listener of the provider panicked at an
   event the Setup emitted, has its registrations undone once Setup returns and is
   Blocked.
5. **Disable** disposes, newest first, every active plugin that requires a key the
   target provides (they become Blocked), then the target. Its settings stay.
6. **Enable** sets the plugin up, then retries Blocked plugins. Enabling a second
   provider of an active key returns an error and changes nothing; if two providers
   are enabled at Start, the later one in install order is Failed. Switching between
   two providers of a key is `Disable` of one, then `Enable` of the other.
7. Enablement is saved under the `kernel` namespace.
8. **Close** disposes every plugin, waits for workers until its context ends, then
   collects the in-flight settings save and writes settings not yet saved. A failed
   batch is retried with newer changes taking precedence. A failed final save is
   returned to the caller; a timed-out save is never overlapped by another save.

Adding or removing a contribution posts `PointChanged` with the point's name: once
for the changes to a point that land before the post runs, never during a dispose.
A `Post` that runs at once announces each change as it happens.

The `off` function `On` returns ends the subscription and lets go of it, so a scope
that subscribes and unsubscribes through its life does not grow.

Disposing a scope marks it dead, cancels its `Lifetime`, disposes child scopes and
runs its disposers, newest first. Afterwards its `Post` closures and task results are
dropped, including failure reports from `Spawn` workers that panic after disposal.

## Threading

- The kernel is not safe for concurrent use. Everything runs on the UI thread except
  `Scope.Lifetime`, `Scope.Post`, the body of `Spawn` and the `work` function of `Go`.
- Workers never touch plugin state: they return a value (`Go`) or `Post` a closure.
- A plugin starts every goroutine with `Spawn` or `Go`, never a `go` statement, which
  the kernel can neither recover nor wait for. A worker (a `Spawn` body or `Go` work,
  of any plugin) may `Spawn` a goroutine of its own; `Spawn` reports false, starting
  nothing, once the scope is disposed. `kittest.PluginsSpawnTheirGoroutines` fails an
  app's test on a `go` statement in its plugins.
- Rendering performs no kernel mutation. Reading `Items`, `Use` and settings during a
  frame is fine; `Emit`, `Contribute`, `Go` and `Setting.Set` belong in handlers.
- Navigation-dependent work belongs to a child scope. A page that shows one document
  of many makes one per open document and disposes it on switch, which drops that
  document's loads and subscriptions in one step.

## Failures

- A panic in Setup, a listener, a transform, a disposer, a posted closure, a `Spawn`
  worker, a task's `work` (also of a run a later one replaced) or `apply`, or a
  callback run through `Invoke` is recovered
  and fails the owning plugin, which is disposed with a `*PanicError` (value and
  stack). Other listeners still run. Dependents become Blocked.
- The shell renders every contribution and runs every command through `Invoke`, so
  a panicking view fails only its plugin, and the shell shows a toast naming it.
- `Invoke` accepts entries returned by `Items` on the same kernel and checks their
  original scope. Disposed contributions stay invalid after a plugin restarts or
  another contribution reuses their ID, including callbacks already queued by a menu.
- `Options.Strict` re-panics instead (tests, and `<Env>_STRICT=1`; see the shell note).
- A Failed plugin can be enabled again from Settings → Plugins. Nothing retries
  automatically.
- Panics in host callbacks, registered on the `Kernel` itself, are app bugs and are
  not recovered.
- `Run` returns a transform's error as a `*PluginError` naming the plugin.

## Naming

- Keys, topics, pipelines and points: `<owning package>.<name>`, e.g. `shell.host`,
  `shell.strip`, `settings.sections`.
- A name is declared once per kind, with one type: `NewKey`, `NewTopic`,
  `NewPipeline` or `NewPoint` panics when the name was declared with another type,
  which would otherwise fail a type assertion later, possibly in the host.
- Entry IDs: `<plugin id>.<name>`; the kernel enforces the prefix.
- Shortcuts are fixed: each command declares its accelerators, and there is no
  remapping. The shell gives a command's first key to the menu bar when it has a
  `Menu`; its other keys are window shortcuts.

## Storage

The shell implements `kernel.Store` with SQLite through `github.com/egoist/mygo/plugins/sqlite`:
`settings.db` in `$<Env>_DATA_DIR`, or else `~/Library/Application Support/<Folder>/`
for a production build (`mygo build`) and `<Folder>-dev/` for a development run
(`go run`, `mygo dev`; see `mygo.IsDev`), where `Env` and `Folder` are the app's
`shell.App`; with one table:

```sql
CREATE TABLE settings (
  namespace TEXT NOT NULL,   -- plugin ID, or "kernel" for enablement
  key       TEXT NOT NULL,
  value     TEXT NOT NULL,   -- JSON
  PRIMARY KEY (namespace, key)
);
```

`Setting[T].Get` decodes the stored value once and keeps it until any setting
changes, so reading one while drawing costs nothing; the value it returns is shared,
so a caller copies a map or slice before changing it, then `Set`s the copy.

The kernel batches changed values and saves them off the UI thread, one save at a
time; a failed save emits `SaveFailed` and is retried with the next one. A database of
another schema is refused, not rewritten: the app keeps settings in memory for that run
and says so. Plugins never see this SQL; one that needs a database of its own keeps
it in `shell.Host.DataDir`, named after its ID.

## Example

The notes plugin of `examples/notes` (shortened here) adds a page, a command in the
File menu and a settings section, and keeps its list in a setting:

```go
func newNotes() kernel.Plugin { return &notes{} } // installed as shell.Run(app, newNotes, …)

func (*notes) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "notes", Title: "Notes", Locked: true, Requires: []kernel.Named{shell.HostKey}}
}

func (n *notes) Setup(c *kernel.Context) error {
	n.host = kernel.Require(c, shell.HostKey)
	n.saved = kernel.NewSetting(c, "list", []string{})
	kernel.Contribute(c, shell.Pages, "notes.page", 0, shell.Page{Title: func() string { return "Notes" }, Render: n.page})
	kernel.Contribute(c, kernel.Commands, "notes.new", 0, kernel.Command{Title: "New Note", Menu: "File",
		Keys: []string{"cmd+n"}, Run: func() { n.host.ShowPage("notes.page"); n.focus = true }})
	return kernel.Contribute(c, settings.Sections, "notes.settings", 10, settings.Section{Title: "Notes", Render: n.settings})
}
```

Without the settings plugin installed the section is simply never drawn; nothing
else changes.

## Testing

`Kernel.Inspect` returns plugin diagnostics in install order: status, resolved
required-service providers, live scope count, registered services/listeners/
transforms/contributions, running worker count and recovered panic stack. Workers
remain counted after cancellation until their bodies exit, including work from a
previous activation. The shell provides this read-only API as `InspectorKey`.

`kernel/kerneltest`:

- `kerneltest.New(t, factories...)` starts a Strict kernel with an in-memory store. The
  test goroutine is the UI thread; posts and background work queue until the test runs
  them. `Flush` drains both; `RunWork(i)`, `RunPosts` and `WaitPosts(n)` let a test
  choose the order in which work finishes and results arrive.
- `kerneltest.Start(t, kerneltest.Config{Store, Lenient}, factories...)` shares a
  store across restarts, or fails plugins on panic instead of re-panicking.
- `kerneltest.Keep(&p, func() *Plugin { return &Plugin{…} })` is a factory that keeps
  the plugin set up now in `p`, so a test looks into the current instance, also after
  a restart. A test double that records what plugins did, such as `kittest.Host`,
  is its own factory (`host.New` returns the host), so what it recorded survives
  restarts; an app's own plugins never are.
- Cleanup closes the kernel and fails the test if workers are still running.

`kittest` has `Host`, a fake `shell.Host` that records what plugins ask of the
window; `ColorsStayInThePalette`, the guard an app's test calls on its own tree so
that every color comes from `native.PaletteFor`; and `PluginsSpawnTheirGoroutines`,
which an app's test calls on its plugins folder.

## Out-of-process plugins

Not built. Registrations are plain data plus callbacks, so a bridge plugin could
later run an external process and register entries on its behalf, with callbacks
forwarding to it. Keep event and value types of points meant for that
JSON-encodable.
