# Design: plugin kernel API

Status: accepted. The kernel is implemented in `internal/kernel`; this doc tracks it.

## Problem

Every feature in the app today is wired into `internal/desktop` directly, or through
`feature.Host` (15 callbacks) and about 15 optional module interfaces. Each new kind
of feature has grown that surface. The backend contract is shaped by OpenCode's HTTP
API. We expect many more features and at least one more backend (Pi), so we need a
core that stays small and stable while everything else is added around it.

## Goals

- **Everything is a plugin.** Backends, the chat view, the session list, settings and
  every later feature are plugins written against one public API. Built-in plugins get
  no private access; if one can't be written as a plugin, the API is incomplete.
- **Reversible registration.** Everything a plugin registers is undone when it's
  disposed. Turning a plugin off at runtime is just dispose; on is setup.
- **Stable kernel.** New kinds of extension are new keys, topics, pipelines or points
  declared by the package that owns them. The kernel's API doesn't grow.
- **Failure is contained.** A plugin that errors or panics is disabled and reported.
  The app keeps running.
- **No UI in the kernel.** `internal/kernel` imports only the standard library, so it
  is testable headless and usable by a future out-of-process bridge.

## Non-goals

- Runtime loading of Go code. Plugins are compiled in and listed in `main.go`.
- Hot reload, service waiting/injection, configuration patch layers.
- Out-of-process plugins now. The design keeps the path open (see the end of this doc).
- Defining the shell's slots or the agent seam. Those are separate docs; they appear
  here only as examples of how packages declare extension points on the kernel.

## Layers

```text
main.go              composition: the list of plugins
plugins/*            opencode · pi · chat · sessions · settings · notify · files …
internal/shell       window, layout, slots (Points of render funcs), menus, shortcuts
internal/contract/*  declarations shared between plugins: agent, chat, …
internal/session     pure conversation model
internal/kernel      this document — standard library only
```

A **contract package** (`internal/contract/<name>`) holds only declarations: types,
interfaces, and the `Key`, `Topic`, `Pipeline` and `Point` values other plugins use to
extend or consume a capability. It has no implementation and no state. The backend
seam is `contract/agent`; what the chat plugin lets others extend is `contract/chat`.

Import rules (enforced with depguard):
- `kernel` imports nothing internal.
- `contract/*` imports only `kernel`, `session` and other contracts. A contract that
  declares UI slots may import MyGo `ui`.
- `shell` imports `kernel` and `contract/*`, never `plugins/*`.
- A plugin imports `kernel`, `shell` and `contract/*`, never another plugin's package.
- Only `main.go` imports plugin packages.

## Concepts

| Concept | What it is | Who declares it | Many or one |
|---|---|---|---|
| `Plugin` | A `Manifest` and a `Setup` function | each plugin | — |
| `Context` | One plugin's handle; the root `Scope` of its registrations | kernel | one per active plugin |
| `Scope` | A child lifetime whose registrations unwind together | plugins | any number |
| `Key[T]` | A capability seam: one provider, many consumers | the package defining `T` | one provider |
| `Topic[T]` | A broadcast notification | the package emitting it | many listeners |
| `Pipeline[T]` | An ordered chain of transforms over a value | the package running it | many transforms |
| `Point[T]` | A named collection of contributions (commands, slots, renderers) | the package reading it | many entries |
| `Task` | A replaceable background job whose stale results are dropped | plugins | — |
| `Settings` | JSON values namespaced to the plugin, kept when it's disabled | kernel | one per plugin |

## API

The complete public surface. Bodies are omitted; see `internal/kernel` for the code.

```go
package kernel

// ---- Plugins

type Plugin interface {
	Manifest() Manifest
	// Setup runs on the UI thread. It registers everything the plugin needs through
	// c. If it returns an error or panics, everything registered so far is undone and
	// the plugin is marked Failed.
	Setup(c *Context) error
}

type Manifest struct {
	ID          string  // unique, [a-z][a-z0-9-]*; prefixes every entry ID the plugin registers
	Title       string  // shown in Settings → Plugins
	Description string
	Provides    []Named // service keys Setup provides
	Requires    []Named // service keys that must be provided before Setup runs
	Locked      bool    // the user cannot disable it (e.g. chat)
	DefaultOff  bool    // installed but disabled until the user enables it (e.g. pi)
}

// Named is implemented by Key, Topic, Pipeline and Point.
type Named interface{ Name() string }

// ---- Owners and scopes

// Owner is anything registrations can belong to: *Context, *Scope and *Kernel.
// The method is unexported so only the kernel implements it.
type Owner interface{ owner() *scope }

type Scope struct{ /* unexported */ }

func (s *Scope) Lifetime() context.Context  // cancelled when the scope is disposed; safe on any goroutine
func (s *Scope) Alive() bool
func (s *Scope) OnDispose(fn func())        // runs during dispose, in reverse registration order
func (s *Scope) NewScope() *Scope           // child scope, disposed with its parent at the latest
func (s *Scope) Dispose()                   // cancels Lifetime, then runs disposers; idempotent
func (s *Scope) Post(fn func())             // any goroutine: run fn on the UI thread if still alive
func (s *Scope) Spawn(fn func(ctx context.Context)) // long-lived worker bound to Lifetime

// Context is the plugin's root scope plus plugin-level facilities.
type Context struct {
	Scope
	/* unexported */
}

func (c *Context) ID() string
func (c *Context) Settings() *Settings

// ---- Services (capability seams)

type Key[T any] struct{ /* name */ }

func NewKey[T any](name string) Key[T]
func (k Key[T]) Name() string

// Provide is allowed only during Setup, for a key declared in Provides. A plugin
// that declares a key but doesn't provide it fails Setup. A second provider for the same key fails
// that plugin's Setup. The service is withdrawn when the provider is disposed;
// plugins that Require it are disposed first.
func Provide[T any](c *Context, k Key[T], v T) error

// Require returns a service listed in the Manifest's Requires. It may be kept for
// the plugin's lifetime. It panics when k is not declared (a programming error).
func Require[T any](c *Context, k Key[T]) T

// Use returns the current provider of an optional capability. Don't keep the
// result beyond the current call; ask again next time.
func Use[T any](o Owner, k Key[T]) (T, bool)

// ---- Events

type Topic[T any] struct{ /* name */ }

func NewTopic[T any](name string) Topic[T]
func (t Topic[T]) Name() string

// On subscribes fn until o is disposed or off is called.
func On[T any](o Owner, t Topic[T], fn func(T)) (off func())

// Emit calls listeners synchronously on the UI thread, in subscription order.
// Values are passed by value and must be treated as immutable.
func Emit[T any](o Owner, t Topic[T], v T)

// ---- Pipelines

type Pipeline[T any] struct{ /* name */ }

func NewPipeline[T any](name string) Pipeline[T]
func (p Pipeline[T]) Name() string

// Transform adds fn to the chain, ordered by order (ascending), until o is disposed.
func Transform[T any](o Owner, p Pipeline[T], order int, fn func(T) (T, error))

// Run passes v through every transform. The first error stops the chain and is
// returned as a *PluginError naming the plugin that rejected it.
func Run[T any](o Owner, p Pipeline[T], v T) (T, error)

// ---- Extension points

type Point[T any] struct{ /* name */ }

func NewPoint[T any](name string) Point[T]
func (p Point[T]) Name() string

type Entry[T any] struct {
	ID     string // "<plugin-id>.<name>", unique within the point
	Plugin string
	Order  int
	Value  T
}

// Contribute adds an entry until o is disposed. id must start with the plugin's ID
// and a dot.
func Contribute[T any](o Owner, p Point[T], id string, order int, v T) error

// Items returns the entries ordered by Order, then host before plugins in install
// order, then registration order. The slice is cached until the point changes, so
// reading it every frame is cheap. Callers must not modify it.
func Items[T any](o Owner, p Point[T]) []Entry[T]

// Invoke runs fn with an entry's value on behalf of the entry's plugin, so a panic
// fails that plugin instead of the host. The shell calls command Run and slot
// Render callbacks through it.
func Invoke[T any](o Owner, e Entry[T], fn func(T))

// HostPrefix starts the IDs of entries the host (the shell) contributes through
// the Kernel, e.g. "app.toggle-sidebar". "app" and "kernel" are not valid plugin IDs.
const HostPrefix = "app."

// ---- Commands (a Point the kernel declares, since every plugin needs one)

type Command struct {
	Title    string
	Category string      // groups commands in the palette, e.g. "Chat"
	Menu     string      // app-menu path, e.g. "View"; empty means palette only
	Keys     []string    // fixed accelerators, e.g. "cmd+shift+k"; parsed by the shell
	Enabled  func() bool // nil means always enabled
	Run      func()
}

var Commands = NewPoint[Command]("kernel.commands")

// ---- Tasks

// Task is a replaceable background job. The zero value is ready to use.
type Task struct{ /* unexported */ }

// Go cancels the task's previous run, runs work off the UI thread with a context
// derived from o's Lifetime, and calls apply on the UI thread only if o is still
// alive and this is still the task's latest run.
func Go[T any](o Owner, t *Task, work func(context.Context) (T, error), apply func(T, error))

func (t *Task) Cancel()
func (t *Task) Running() bool

// ---- Settings

// Settings stores JSON values namespaced to the plugin. Values survive disable,
// re-enable and restart.
type Settings struct{ /* unexported */ }

func (s *Settings) Get(key string, into any) bool
func (s *Settings) Set(key string, v any) error
func (s *Settings) Delete(key string)

// Setting is a typed convenience over Settings with a default.
type Setting[T any] struct{ /* unexported */ }

func NewSetting[T any](c *Context, key string, def T) *Setting[T]
func (s *Setting[T]) Get() T
func (s *Setting[T]) Set(v T) error

// ---- The host side (used by main.go and the shell)

type Options struct {
	Post       func(func()) // required: schedule on the UI thread (MyGo Window.Update)
	Background func(func()) // optional: run off the UI thread; default is a goroutine
	Store      Store        // required: persistence for settings and enablement
	Strict     bool         // re-panic instead of disabling the plugin (tests, dev)
}

// Store persists settings as (namespace, key) -> JSON value. The kernel batches
// changed values and calls Save off the UI thread, one call at a time.
type Store interface {
	Load(ctx context.Context) ([]Value, error)
	Save(ctx context.Context, changed []Value) error // Value.JSON nil deletes the key
}

type Value struct {
	Namespace string // plugin ID, or "kernel" for enablement
	Key       string
	JSON      json.RawMessage
}

type Kernel struct{ /* unexported */ }

func New(Options) *Kernel
func (k *Kernel) Install(plugins ...Plugin) error // manifests only; validates IDs and the graph
func (k *Kernel) Start() error                    // sets up enabled plugins in dependency order
func (k *Kernel) Enable(id string) error
func (k *Kernel) Disable(id string) error
func (k *Kernel) Plugins() []Status
func (k *Kernel) Close(ctx context.Context) error // disposes all, waits for workers until ctx ends

type State uint8 // implements fmt.Stringer

const (
	Active   State = iota
	Disabled       // by the user, or DefaultOff
	Blocked        // a required key has no provider
	Failed         // Setup or a callback errored or panicked
)

type Status struct {
	Manifest Manifest
	State    State
	Err      error
	Missing  []string // unprovided keys when Blocked
}

type PluginError struct {
	Plugin string
	Err    error
}

// PanicError is a recovered panic, stored as a Failed plugin's Err.
type PanicError struct {
	Value any
	Stack []byte
}

var (
	Started        = NewTopic[struct{}]("kernel.started")
	PluginsChanged = NewTopic[[]Status]("kernel.plugins")
	SaveFailed     = NewTopic[error]("kernel.save-failed") // the batch is retried with the next save
)
```

## Lifecycle

```text
Install ──► Disabled ──Enable──► (requirements met?) ──no──► Blocked
                ▲                       │ yes                   │ provider appears
                │                     Setup ──error/panic──► Failed
             Disable                    │ ok                    │ Enable retries
                └────────────────── Active ◄────────────────────┘
```

1. **Install** reads manifests only. It rejects duplicate or malformed IDs, a plugin
   requiring a key it provides, and `Requires`/`Provides` cycles that could never
   resolve. No plugin code beyond `Manifest` runs.
2. **Start** loads the store, then sets up each enabled plugin in dependency order
   (a provider before anyone that requires its key; otherwise install order). Then it
   emits `Started`. A plugin may not rely on anything except its `Requires` being set up
   before it.
3. **Requirements** are service keys, not plugin IDs, so `chat` requires
   `agent.Service` and doesn't care whether OpenCode or Pi provides it.
4. **Disable** disposes, in reverse dependency order, every active plugin that requires
   a key the target provides (they become Blocked), then the target. Its settings stay.
5. **Enable** sets the plugin up, then retries Blocked plugins whose requirements are now
   met. Switching backends is `Disable("opencode")` then `Enable("pi")`. Enabling a
   second provider of an active key returns an error and changes nothing. If two
   providers are enabled at Start, the later one in install order is Failed.
6. Enablement is persisted in the store under the `kernel` namespace. `Locked` plugins
   can't be disabled; `DefaultOff` ones start disabled until the user enables them once.
7. **Close** disposes every plugin in reverse order, then waits for `Spawn` workers and
   running tasks to return, until the context passed to it ends, then writes any
   settings not yet saved.

Disposing a scope always does the same thing, in order: mark it dead, cancel
`Lifetime`, dispose child scopes (newest first), then run its own disposers (newest
first). After that, `Post` and task results for that scope are dropped.

## Threading

- The kernel isn't safe for concurrent use. Everything runs on the UI thread except
  `Scope.Lifetime`, `Scope.Post`, the body of `Spawn` and the `work` function of `Go`.
- Workers never touch plugin state. They return a value (`Go`) or `Post` a closure.
- Rendering doesn't run kernel mutations. Reading `Items`, `Use` and settings during a
  frame is fine; `Emit`, `Contribute` and `Go` belong in input handlers or callbacks.
- Navigation-dependent work uses a scope. For example, the chat plugin creates one
  scope per open conversation and disposes it on switch, which cancels that
  conversation's tasks and subscriptions in one step. This replaces today's per-request
  revision checks.

## Failure policy

- An error from `Setup` marks the plugin Failed with that error and undoes its
  registrations. Dependents become Blocked.
- A panic in `Setup`, an event listener, a transform, a disposer, a posted closure, a
  `Spawn` worker, a task's `work` or `apply`, or a callback run through `Invoke` is
  recovered. The kernel attributes it to the owning plugin,
  which is disposed and marked Failed with the panic value and stack. Other listeners
  for the same event still run.
- With `Options.Strict`, panics are re-raised so tests and development fail loudly.
- A plugin in Failed state can be re-enabled from Settings. There is no automatic retry.
- Panics in host callbacks (registered on the `Kernel` itself) are not recovered:
  those are app bugs, not plugin bugs.

## Naming

- Plugin IDs: `opencode`, `chat`, `sessions`.
- Names of keys, topics, pipelines and points: `<owning-package>.<name>`, such as
  `agent.service`, `agent.turn-ended`, `chat.outgoing`, `shell.sidebar`.
- Entry IDs: `<plugin-id>.<name>`, such as `chat.send` or `files.quick-open`. The kernel
  enforces the prefix, so two plugins can't collide.
- Shortcuts are fixed: each command declares its accelerators and there is no user
  remapping. If two active commands declare the same accelerator, the shell keeps the
  first by `Items` order and reports the conflict in Settings → Plugins.

## Storage

The shell implements `Store` with SQLite through `github.com/egoist/mygo/plugins/sqlite`,
which MyGo already bundles, so there is no new dependency. One database,
`settings.db` in `$WADA_DATA_DIR` or the user's config directory under
`wada-desktop-go/`, holds one table:

```sql
CREATE TABLE settings (
  namespace TEXT NOT NULL,
  key       TEXT NOT NULL,
  value     TEXT NOT NULL, -- JSON
  PRIMARY KEY (namespace, key)
);
```

A database of another schema is refused, never rewritten; the app then keeps settings
in memory for that run and says so. Each `Save` is one transaction that upserts or deletes the changed rows, so a crash
never leaves half-written settings and a change rewrites only its own rows. Plugins
never see SQL; they use `Settings`. Conversation contents stay with the backend.
A plugin that later needs its own queryable data gets a separate design rather than
a raw database handle.

## How other packages build on it

The kernel only provides mechanisms. Packages declare their extension points with them:

```go
// internal/contract/agent
var ServiceKey = kernel.NewKey[Service]("agent.service")
var Events     = kernel.NewTopic[Event]("agent.event")
var FilesKey   = kernel.NewKey[Files]("agent.files") // optional backend capability

// internal/shell
type Item struct {
	When   func() bool
	Render func(*ui.Context)
}
var Sidebar      = kernel.NewPoint[Item]("shell.sidebar")
var RightPanel   = kernel.NewPoint[Panel]("shell.right-panel")
var ToolRenderer = kernel.NewPoint[ToolView]("shell.tool-renderer") // keyed by tool kind

// internal/contract/chat: what plugins/chat lets others extend
var Outgoing = kernel.NewPipeline[Draft]("chat.outgoing")
```

`plugins/chat` runs `kernel.Run(c, chat.Outgoing, draft)` before sending; `plugins/mentions`
adds a transform to it. Both import `contract/chat`; neither imports the other.

The shell reads `kernel.Items(k, shell.Sidebar)` each frame and renders the entries in
order. It turns `kernel.Commands` into the app menu, shortcuts and the command palette.

## Examples

A backend provides the seam:

```go
type Plugin struct{}

func (Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "opencode", Title: "OpenCode",
		Provides: []kernel.Named{agent.ServiceKey}}
}

func (Plugin) Setup(c *kernel.Context) error {
	svc := newService(c.Settings()) // connection details live in the plugin's settings
	c.Spawn(svc.supervise)          // owns the process and event stream; stops on dispose
	return kernel.Provide[agent.Service](c, agent.ServiceKey, svc)
}
```

A feature that adds `@file` mentions without touching the chat plugin:

```go
func (p *mentions) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "mentions", Requires: []kernel.Named{agent.FilesKey}}
}

func (p *mentions) Setup(c *kernel.Context) error {
	files := kernel.Require(c, agent.FilesKey)
	kernel.Transform(c, chat.Outgoing, 100, p.expandMentions)
	kernel.Contribute(c, shell.ComposerOverlay, "mentions.menu", 0, shell.Item{Render: p.menu})
	return kernel.Contribute(c, kernel.Commands, "mentions.insert", 0, kernel.Command{
		Title: "Mention a file", Category: "Chat", Keys: []string{"cmd+shift+2"},
		Run: func() {
			kernel.Go(c, &p.search, func(ctx context.Context) ([]agent.File, error) {
				return files.Find(ctx, p.query)
			}, p.showResults)
		},
	})
}
```

If the backend has no file capability, `mentions` is Blocked and shown that way in
Settings. Nothing else changes.

## Testing

`internal/kernel/kerneltest` provides:

- `kerneltest.New(t, plugins...)`: a started Strict kernel with an in-memory `Store`.
  The test goroutine is the UI thread. Posts and background work queue until the test
  runs them: `Flush()` drains both, while `RunWork(i)`, `RunPosts()` and `WaitPosts(n)`
  let a test choose the order in which work finishes and results arrive.
- `kerneltest.Start(t, kerneltest.Config{Store, Lenient}, plugins...)`: the same with a
  shared store (to test restarts) or with panics failing plugins instead of re-panicking.
- `t.Cleanup` closes the kernel and fails the test if workers are still running.

The kernel's own tests also assert that no registration remains after `Close`. They
cover dispose order, A→B→A replacement of a `Task`, `Post` after dispose, panics in
each callback kind, Blocked/unblocked transitions, switching providers, provider
conflicts, install validation, entry ID prefixes, `Items` ordering and caching,
pipeline order and rejection, and settings surviving disable and restart, coalesced
saves and retry after a failed save.

## Path to out-of-process plugins (later)

Everything a plugin registers is plain data plus callbacks: commands, accelerators,
settings, pipeline transforms, event payloads. A future `bridge` plugin can start an
external process, speak JSON-RPC over stdio (like MCP or LSP), and register entries on
its behalf whose callbacks forward to the process. External plugins would contribute
commands, settings, transforms, event handlers and declarative views (markdown or a
small schema), but not arbitrary native widgets. The kernel needs no changes for this,
provided event and value types for bridged points stay JSON-encodable.

## Alternatives considered

**Keep `feature.Host` and the optional interfaces.** Every new kind of contribution
adds a method to the host or a new interface the shell must check. That's the growth
we're trying to stop.

**A Cordis-style context with service injection and waiting.** Plugins would wait on
services to appear instead of declaring `Requires`. It's flexible, but ordering becomes
implicit and a missing service only shows up as a plugin that never starts. Explicit
requirements plus the Blocked state give the same effect with clearer diagnostics.

**A single untyped event bus.** Simple to build, but payload types aren't checked and
it's hard to see who depends on what. Typed keys, topics, pipelines and points give the
compiler and `grep` something to follow.

**Go's `plugin` package.** It requires the exact same toolchain and dependency versions
as the host, doesn't support unloading, and is fragile on macOS.

**WebAssembly plugins.** A possible later option for sandboxed third-party code. Today
it would mean a runtime dependency and an ABI, before any third-party plugin exists.

**Interfaces on the plugin type instead of registration functions** (for example a
plugin implementing `Commands() []Command`). That's what the current modules do. It
forces contributions to be static, and it gives the kernel nothing to undo
automatically.

## Review decisions

1. **One provider per key.** A `Key` has exactly one provider; anything with many
   contributors is a `Point`.
2. **Storage.** SQLite key-value table through MyGo's bundled driver (see Storage).
3. **Shortcuts.** Fixed accelerators declared by each command; no user remapping.
4. **Plugin-to-plugin extension.** Through contract packages in `internal/contract/*`;
   plugins never import each other.
