# Kernel API

`internal/kernel` is the plugin core: plugin lifecycle, scoped and reversible
registration, services, events, pipelines, extension points, commands, background
tasks and settings. It imports only the standard library and knows no feature or UI.
Signatures and exact behavior are documented in the package itself; this note gives
the model and the rules.

## Layers

```text
main.go              composition: the plugins this build installs
internal/plugins/*   every feature and backend
internal/shell       the window host: kernel, settings store, layout, menus, shortcuts
internal/contract/*  declarations shared between plugins
internal/kernel      plugin core, standard library only
```

A contract package (`internal/contract/<name>`) holds only declarations: types,
interfaces and the `Key`, `Topic`, `Pipeline` and `Point` values plugins use to extend
or consume a capability. No implementation, no state.

Import rules, enforced by depguard in `.golangci.yml`:

- `kernel` imports only the standard library.
- `contract/*` imports `kernel`, other contracts and MyGo `ui`.
- `shell` imports `kernel`, `contract/*`, `native`, MyGo and Onevra; never a plugin.
- A plugin imports `kernel`, `contract/*`, `native`, MyGo and Onevra; never another
  plugin and never `shell`.
- Only `main.go` imports plugin packages.

## Concepts

| Concept | What it is | Many or one |
|---|---|---|
| `Plugin` | `Manifest()` plus `Setup(*Context) error` | — |
| `Context` | one plugin's handle and the root `Scope` of its registrations | one per active plugin |
| `Scope` | a lifetime whose registrations, workers and posts end together | any number (`NewScope`) |
| `Key[T]` | a service: `Provide`, `Require`, `Use` | exactly one provider |
| `Topic[T]` | an event: `On`, `Emit` | many listeners |
| `Pipeline[T]` | ordered transforms over a value: `Transform`, `Run` | many transforms |
| `Point[T]` | a collection of entries: `Contribute`, `Items`, `Invoke` | many entries |
| `Commands` | the kernel's own `Point[Command]`: title, menu, accelerators, run | many |
| `Task` | a replaceable background job (`Go`); stale results are dropped | — |
| `Settings`, `Setting[T]` | JSON values namespaced to the plugin | one namespace per plugin |

Anything with many contributors is a `Point`; a `Key` has one provider.

## Manifest

- `ID` matches `[a-z][a-z0-9-]*` and prefixes every entry the plugin contributes
  (`chat.new`, `opencode.connection`). `kernel` and `app` are reserved; entries the
  host contributes through the `Kernel` itself start with `HostPrefix` (`app.`).
- `Provides` lists the keys Setup must provide; `Requires` the keys that must have a
  provider before Setup runs. Requirements are keys, not plugin IDs: `chat` requires
  `agent.ServiceKey` and doesn't care whether OpenCode or Pi provides it.
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

1. **Install** reads manifests only. It rejects malformed or duplicate IDs, a plugin
   requiring a key it provides, and requirement cycles.
2. **Start** loads the store, then sets up enabled plugins in install order, repeating
   while a setup provides something new, so providers come before their consumers.
   Then it emits `Started` and `PluginsChanged`.
3. A Setup that errors or panics, or that declares a key it didn't provide, has its
   registrations undone and the plugin is Failed.
4. **Disable** disposes, newest first, every active plugin that requires a key the
   target provides (they become Blocked), then the target. Its settings stay.
5. **Enable** sets the plugin up, then retries Blocked plugins. Enabling a second
   provider of an active key returns an error and changes nothing; if two providers
   are enabled at Start, the later one in install order is Failed. Switching backends
   is `Disable("opencode")`, then `Enable("pi")`.
6. Enablement is saved under the `kernel` namespace.
7. **Close** disposes every plugin, waits for workers until its context ends, then
   writes settings not yet saved.

Disposing a scope marks it dead, cancels its `Lifetime`, disposes child scopes and
runs its disposers, newest first. Afterwards its `Post` closures and task results are
dropped.

## Threading

- The kernel is not safe for concurrent use. Everything runs on the UI thread except
  `Scope.Lifetime`, `Scope.Post`, the body of `Spawn` and the `work` function of `Go`.
- Workers never touch plugin state: they return a value (`Go`) or `Post` a closure.
- Rendering performs no kernel mutation. Reading `Items`, `Use` and settings during a
  frame is fine; `Emit`, `Contribute`, `Go` and `Setting.Set` belong in handlers.
- Navigation-dependent work belongs to a child scope. The chat plugin makes one per
  open conversation and disposes it on switch, which drops that conversation's loads,
  sends and subscriptions in one step.

## Failures

- A panic in Setup, a listener, a transform, a disposer, a posted closure, a `Spawn`
  worker, a task's `work` or `apply`, or a callback run through `Invoke` is recovered
  and fails the owning plugin, which is disposed with a `*PanicError` (value and
  stack). Other listeners still run. Dependents become Blocked.
- The shell renders every contribution and runs every command through `Invoke`, so
  a panicking view fails only its plugin, and the shell shows a toast naming it.
- `Options.Strict` re-panics instead (tests, and `WADA_STRICT=1`).
- A Failed plugin can be enabled again from Settings → Plugins. Nothing retries
  automatically.
- Panics in host callbacks, registered on the `Kernel` itself, are app bugs and are
  not recovered.
- `Run` returns a transform's error as a `*PluginError` naming the plugin.

## Naming

- Keys, topics, pipelines and points: `<owning package>.<name>`, e.g. `agent.service`,
  `agent.events`, `chat.outgoing`, `shell.sidebar`.
- Entry IDs: `<plugin id>.<name>`; the kernel enforces the prefix.
- Shortcuts are fixed: each command declares its accelerators, and there is no
  remapping. The shell gives a command's first key to the menu bar when it has a
  `Menu`; its other keys are window shortcuts.

## Storage

The shell implements `kernel.Store` with SQLite through `github.com/egoist/mygo/plugins/sqlite`:
`settings.db` in `$WADA_DATA_DIR`, or `~/Library/Application Support/wada-desktop-go/`,
with one table:

```sql
CREATE TABLE settings (
  namespace TEXT NOT NULL,   -- plugin ID, or "kernel" for enablement
  key       TEXT NOT NULL,
  value     TEXT NOT NULL,   -- JSON
  PRIMARY KEY (namespace, key)
);
```

The kernel batches changed values and saves them off the UI thread, one save at a
time; a failed save emits `SaveFailed` and is retried with the next one. A database of
another schema is refused, not rewritten: the app keeps settings in memory for that run
and says so. Plugins never see SQL. Conversations belong to the backend.

## Example

The model picker (`internal/plugins/models`, shortened here) extends the chat plugin
without importing it: both use `contract/chat`.

```go
func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "models", Title: "Model picker",
		Requires: []kernel.Named{agent.ServiceKey, agent.ModelsKey, chat.ControllerKey, shell.HostKey}}
}

func (p *Plugin) Setup(c *kernel.Context) error {
	p.models = kernel.Require(c, agent.ModelsKey)
	kernel.Transform(c, chat.Outgoing, 0, p.outgoing) // sets Draft.Model for a new chat
	return kernel.Contribute(c, chat.ComposerAccessory, "models.pickers", 0, shell.View{Render: p.view})
}
```

A backend without a model catalog doesn't provide `agent.ModelsKey`, so the picker is
Blocked and shown that way in Settings → Plugins. Nothing else changes.

## Testing

`internal/kernel/kerneltest`:

- `kerneltest.New(t, plugins...)` starts a Strict kernel with an in-memory store. The
  test goroutine is the UI thread; posts and background work queue until the test runs
  them. `Flush` drains both; `RunWork(i)`, `RunPosts` and `WaitPosts(n)` let a test
  choose the order in which work finishes and results arrive.
- `kerneltest.Start(t, kerneltest.Config{Store, Lenient}, plugins...)` shares a store
  across restarts, or fails plugins on panic instead of re-panicking.
- Cleanup closes the kernel and fails the test if workers are still running.

`internal/plugintest` has fakes for the contracts (agent, host, chat controller).

## Out-of-process plugins

Not built. Registrations are plain data plus callbacks, so a bridge plugin could
later run an external process and register entries on its behalf, with callbacks
forwarding to it. Keep event and value types of points meant for that
JSON-encodable.
