# Agent contract

Declared in `internal/contract/agent`; implemented by `internal/plugins/opencode` and
`internal/plugins/pi`.

A backend is a kernel plugin. It declares `Provides: agent.ServiceKey` (and
optionally `agent.ModelsKey` and `agent.ModesKey`) and owns everything about
its connection: configuration in its own settings, the process or socket,
reconnecting with backoff, and decoding its protocol.

| Declaration | Kind | Purpose |
|---|---|---|
| `ServiceKey` | `Key[Service]` | sessions, messages, send, abort, approvals, rename, delete |
| `ModelsKey` | `Key[Models]` | optional model catalog, default model, per-session model |
| `InteractionsKey` | `Key[Interactions]` | optional confirmation, selection, text input and editor dialogs |
| `ModesKey` | `Key[Modes]` | optional named agent modes (OpenCode's build/plan) |
| `FileTypesKey` | `Key[FileTypes]` | optional: a backend that takes only some kinds of files says which |
| `FilesKey` | `Key[Files]` | optional: find and read files of a conversation's folder, where the backend runs |
| `RewindKey` | `Key[Rewinder]` | optional: take back a user message and everything after it |
| `ChangesKey` | `Key[Changes]` | optional: the files a conversation changed, with line counts and a unified diff |
| `Events` | `Topic[[]Event]` | ordered batches of normalized events, on the UI thread |
| `StatusChanged` | `Topic[Status]` | connection state, on the UI thread |
| `ModelsChanged` | `Topic[struct{}]` | the model catalog changed on the same connection; the model picker reloads it |

Rules every backend follows:

- `Send` returns admission separately from execution: `SendAccepted` waits for
  run events, `SendQueued` reports queued input, and `SendHandled` means the input
  was consumed without requiring a new run. A handled input releases the composer
  but does not settle independent work. Dispositions are meaningful only on success.
- `Service.Status` is read on the UI thread; every other method may be called
  from any goroutine (normally a `kernel.Go` work function) and blocks on I/O.
- Without a connection, methods return `agent.ErrNotConnected`.
- Mutations (`Create`, `Send`, `Abort`, `Reply`, `Rename`, `Delete`) are never
  retried by the backend. A failed mutation has an unknown outcome on the server,
  except a `Send` whose error wraps `agent.ErrNotSent`: the backend refused it before
  sending anything.
- `Messages` returns a conversation's messages oldest first.
- The app never reads a backend's own names for tools or permissions. A backend maps
  them onto `ToolKind` (shell, read, search, edit, fetch, or other) in `Tool.Kind` and
  `Approval.Kind`, and the app words and draws a call from the kind alone; `Tool.Name`
  and `Approval.Action` are shown only for `ToolOther`. `ToolStarted` carries the
  kind; a later event of the same call may leave it out.
- `Send.Files` go with the message; stored user messages list theirs in
  `Message.Files`, without data.
- A stored assistant message carries its `Cost` and `Tokens`: input, output,
  reasoning, and cache reads and writes. `Output` never includes `Reasoning`; Pi
  counts reasoning within output, so its backend takes it out. `Tokens.Context` sums
  them all, the share of the context window the message filled. `Model.Context` is
  the model's context window, 0 when the backend doesn't say.
- `Rewinder.Rewind` is a mutation like the others: never retried, its failure an
  unknown outcome. It leaves the files the agent changed as they are, and returns the
  conversation to go on in: the same one when the backend rewinds in place, or a new
  one when it can only fork, leaving the original as it was.
- `Create` returns the created session together with an error when a later step,
  such as setting the model, fails.
- `Status.Attempt` increases with every connection attempt, also across the
  plugin's activations (the count lives outside the plugin). A consumer that keeps
  server state reloads it when a new attempt becomes `Ready`.
- `Status.Origin` names where the backend keeps its conversations, set on every
  status from the first attempt on: `opencode <data folder>` for the managed server
  (every run shares its storage; plain `opencode` when the app has no data folder,
  as in tests), `opencode <url>` for a remote one, `pi <session folder>` for Pi.
  A session ID means something only under its origin.
- Events are emitted only after the stream is established, in order, in batches of
  at most 64 events or 256 KiB, collected for at most 1/60 s. At most one batch is
  in flight to the UI thread, so a busy UI slows the stream instead of queueing.
- Unknown events are ignored. OpenCode reports the first malformed known event of a
  stream in `Status.Err` without quoting its payload; Pi skips malformed lines.
- Requests are independent: the contract has no command queue. Policies such as
  "one turn at a time" belong to the consumer (the chat plugin).

## Adding a backend

A backend is one plugin under `internal/plugins/<name>`; nothing outside it changes but
the line that installs it in `main.go`. It:

1. provides `agent.ServiceKey`, and each optional key it can honor (`ModelsKey`,
   `ModesKey`, `InteractionsKey`, `FileTypesKey`, `FilesKey`, `RewindKey`,
   `ChangesKey`), listing them in its manifest's `Provides`. Without an optional key
   the app hides what depends on it: the pickers, `@` mentions, retry and edit, the
   work panel's Changes;
2. translates its protocol into the contract inside its own package: events into
   `agent.Event`, stored messages into `agent.Message` oldest first, tool and
   permission names into `ToolKind`, usage into `Tokens` with reasoning apart from
   output;
3. keeps its connection settings in its own settings section;
4. runs `plugintest.CheckBackend` against a fake of its protocol, as OpenCode and Pi do.

Only one backend is active at a time: the kernel refuses a second provider of
`agent.ServiceKey`, so a new backend ships off by default (`DefaultOff`) unless it
replaces the default.

## OpenCode plugin

`internal/plugins/opencode` requires `shell.HostKey` and saves one setting,
`connection`:

- zero value: run `opencode serve` on a free local port, in the home directory,
  with a fresh password per run; the process ends with the plugin, and with the
  app even when it crashes on Linux (a parent-death signal) and Windows (a job
  object that kills its processes when the app's handle closes). The server is
  the app's own OpenCode build, never one on `PATH`: the plugin pins a version and,
  for the running OS and architecture, the npm tarball (`@opencode/cli-darwin-arm64`,
  `@opencode/cli-darwin-x64-baseline`, `@opencode/cli-windows-x64-baseline`,
  `@opencode/cli-windows-arm64`) and its published sha512 integrity; another
  platform reports that it has no build. On first use it downloads the tarball,
  refuses it unless the checksum matches, and unpacks `package/bin/opencode`
  (`opencode.exe` on Windows) to `<DataDir>/opencode/bin/<version>/`, then
  removes other versions there. While it downloads, the settings section shows the
  progress. The server keeps its data apart from the OpenCode CLI's: it runs with
  `XDG_DATA_HOME`, `XDG_STATE_HOME` and `XDG_CACHE_HOME` under
  `<DataDir>/opencode/` (`data`, `state`, `cache`), so its credentials, conversations
  and logs are the app's own. Config is shared: `~/.config/opencode` and the config
  of the tools the agent runs;
- `{"remote": true, "url": …, "password": …}`: connect to that server.

Files go in the prompt's `files` as `data:` URIs, which a remote server reads as well
as a local one; the server stores them inline.

It provides `agent.FilesKey` over the server's file routes: `Find` is
`GET /fs/find?type=file&query=…&limit=…`, which ranks and honors ignore files, and
`Read` is `GET /fs/read/<path>`; both take the folder as `location[directory]` and
use the server's own when it is empty. `Read` refuses a path that is empty, absolute
or leaves the folder, and an HTML answer, which is what a server without the route
sends in place of an error.

It provides `agent.RewindKey` in place: `POST /session/<id>/revert/stage` with the
message and `"files": false`, so the files stay, then `POST /session/<id>/revert/commit`,
which removes the message and everything after it. A commit that fails after the stage
is reported as such.

It provides `agent.ChangesKey` from `GET /session/<id>/diff`: each file's path, status
(added, deleted, otherwise modified), additions, deletions and patch. An entry without
a path is skipped.

A second settings section, Providers, manages the server's model providers over its
integration routes, for a managed or a remote server alike. `GET /integration` is
read once per connection attempt. Connected providers come first, each stored
credential with Disconnect (`DELETE /credential/<id>`); one connected through an
environment variable says which. Below, a search over every provider (the popular
ones before a search) opens a connect form: the provider's methods of type `key`,
`oauth` and `external` whose forms have only string fields, with each field's
`when` conditions, options and defaults, and a hidden field sent as its default.
A key goes to `POST /integration/<id>/connect/key`, an external method to
`.../connect/external`. A sign-in starts at `.../connect/oauth`, opens the returned
URL and shows its instructions; mode `auto` checks `.../connect/oauth/<attempt>`
until it is complete or failed, mode `code` posts the pasted code to
`.../complete`, and Cancel deletes the attempt. Each of these is a mutation, never
retried; a failure stays in the form. A change reloads the list and emits
`agent.ModelsChanged`.

The client is hand-written. `testdata/openapi-<version>.json` is the OpenAPI
document the pinned release serves at `/openapi.json`, saved by
`scripts/pin-opencode-spec.sh`. `TestClientSpeaksThePinnedSpec` makes every call
against a recording server and checks each against it: the route, the query
parameters, the request body's fields, and the fields of the named reply types
(`sessionListReply` and the rest) the client decodes. The document leaves event
payloads opaque, so events are covered by the decoder's own tests. Bumping the
version fails the test until the new document is pinned.

`Configure` saves a new connection and reconnects; `Reconnect` (also the
`opencode.reconnect` command) restarts the connection. Each connection runs in its
own kernel scope, so results of a replaced connection are dropped by the kernel.

## Pi plugin

`internal/plugins/pi` (off by default) implements the same contract over a very
different transport. It saves one setting, `settings`: the `pi` executable and the
session folder, both empty for Pi's defaults. "Restart Pi" restarts its processes.
Each connection's workers, the readers of its processes among them, run in the
connection's scope; its supervisor closes conversation processes off the UI thread
when the connection scope ends. A panic while reading a process's output ends that
process and fails the plugin.

- Pi's RPC mode (`pi --mode rpc`, JSONL on stdin/stdout) drives one session per
  process. A control process (`--no-session`) answers the model catalog and is the
  connection's health; each conversation gets its own process when first used
  (`--session <file>`), and idle ones beyond four are stopped.
- Pi maps the prompt response's `started`, `queued`, and `handled` dispositions to
  the shared admission result. An unrecognized response is an uncertain outcome,
  reported without retrying. OpenCode reports `SendAccepted` on a successful send.
- Pi has no command to list sessions, so the plugin reads the session files under the
  session folder (`~/.pi/agent/sessions` unless configured): the header for the ID and
  folder, `session_info` for the name, and the path from the last entry to the root
  for the messages. A folder that no longer exists falls back to the home folder.
- Events map as: `agent_start` → TurnStarted; text block updates → Text*;
  `tool_execution_*` → ToolCalled/ToolFinished; `agent_settled` → TurnSucceeded,
  TurnFailed (an assistant message stopped with an error; `Error` carries it, or the
  final error of Pi's retries) or TurnInterrupted
  (aborted); `session_info_changed` → SessionRenamed. A model switch is confirmed
  with a ModelChanged the plugin emits after `set_model` succeeds.
- Extension `confirm`, `select`, `input`, and `editor` dialogs emit
  `InteractionRequested`. Pi provides `InteractionsKey`; replies preserve the
  selected label, confirmation, submitted text (including empty text), or explicit
  cancellation. A reply must name the matching session and an offered selection.
  These are general questions, separate from OpenCode permission approvals.
- Images go in the prompt's `images`. Text files are appended to the message as fenced
  blocks headed `(file: name)`. Any other file makes `Send` fail with `ErrNotSent`.
  Stored images come back as files named "image", since Pi keeps no names. It
  provides `agent.FileTypesKey` so other files are refused when added.
- Pi can't take a turn back in place, so it provides `agent.RewindKey` with `fork`:
  the conversation's process forks before the message into a new session (its ID from
  `get_state`) and moves to it; the original session file stays as it was. A fork an
  extension cancels is an error.
- Thinking levels are the model's variants. Pi has no agent modes, so it doesn't
  provide `ModesKey`, and the mode picker stays hidden.
- Pi keeps no record of the files a session changed, so it doesn't provide
  `ChangesKey`.

Only one backend can be active: turn OpenCode off in Settings → Plugins, then turn Pi on.

## Not supported

Pairing links, editing OpenCode's permission files, workspace and VCS access,
project listing, per-message usage reads, and several saved servers. Pi does not
provide `FilesKey`: its RPC has no file routes. Any of these would be a new optional
capability key, provided by the backends that support it.
