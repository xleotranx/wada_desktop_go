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
| `ModesKey` | `Key[Modes]` | optional named agent modes (OpenCode's build/plan) |
| `Events` | `Topic[[]Event]` | ordered batches of normalized events, on the UI thread |
| `StatusChanged` | `Topic[Status]` | connection state, on the UI thread |

Rules every backend follows:

- `Service.Status` is read on the UI thread; every other method may be called
  from any goroutine (normally a `kernel.Go` work function) and blocks on I/O.
- Without a connection, methods return `agent.ErrNotConnected`.
- Mutations (`Create`, `Send`, `Abort`, `Reply`, `Rename`, `Delete`) are never
  retried by the backend. A failed mutation has an unknown outcome on the server,
  except a `Send` whose error wraps `agent.ErrNotSent`: the backend refused it before
  sending anything.
- `Send.Files` go with the message; stored user messages list theirs in
  `Message.Files`, without data.
- `Create` returns the created session together with an error when a later step,
  such as setting the model, fails.
- `Status.Attempt` increases with every connection attempt. A consumer that keeps
  server state reloads it when a new attempt becomes `Ready`.
- Events are emitted only after the stream is established, in order, in batches of
  at most 64 events or 256 KiB, collected for at most 1/60 s. At most one batch is
  in flight to the UI thread, so a busy UI slows the stream instead of queueing.
- Unknown events are ignored. OpenCode reports the first malformed known event of a
  stream in `Status.Err` without quoting its payload; Pi skips malformed lines.
- Requests are independent: the contract has no command queue. Policies such as
  "one turn at a time" belong to the consumer (the chat plugin).

## OpenCode plugin

`internal/plugins/opencode` saves one setting, `connection`:

- zero value: run `opencode serve` on a free local port, in the home directory,
  with a fresh password per run; the process ends with the plugin;
- `{"remote": true, "url": …, "password": …}`: connect to that server.

Files go in the prompt's `files` as `data:` URIs, which a remote server reads as well
as a local one; the server stores them inline.

`Configure` saves a new connection and reconnects; `Reconnect` (also the
`opencode.reconnect` command) restarts the connection. Each connection runs in its
own kernel scope, so results of a replaced connection are dropped by the kernel.

## Pi plugin

`internal/plugins/pi` (off by default) implements the same contract over a very
different transport. It saves one setting, `settings`: the `pi` executable and the
session folder, both empty for Pi's defaults. "Restart Pi" restarts its processes.

- Pi's RPC mode (`pi --mode rpc`, JSONL on stdin/stdout) drives one session per
  process. A control process (`--no-session`) answers the model catalog and is the
  connection's health; each conversation gets its own process when first used
  (`--session <file>`), and idle ones beyond four are stopped.
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
- Extension `confirm` and `select` dialogs become approvals (Allow → yes or the first
  option, Deny → no or cancel). `input` and `editor` dialogs are cancelled, since the
  app has no place to answer them.
- Images go in the prompt's `images`. Text files are appended to the message as fenced
  blocks headed `(file: name)`. Any other file makes `Send` fail with `ErrNotSent`.
  Stored images come back as files named "image", since Pi keeps no names. It
  provides `agent.FileTypesKey` so other files are refused when added.
- Thinking levels are the model's variants. Pi has no agent modes, so it doesn't
  provide `ModesKey`, and the mode picker stays hidden.

Only one backend can be active: turn OpenCode off in Settings → Plugins, then turn Pi on.

## Not supported

Pairing links, editing OpenCode's permission files, workspace, VCS and file access,
project listing, per-message usage reads, and several saved servers. Any of these
would be a new optional capability key, provided by the backends that support it.
