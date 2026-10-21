# Design: shell and chat page

Status: accepted. Declared in `internal/contract/shell` and `internal/contract/chat`;
implemented by `internal/shell` and `internal/plugins/chat`.

## Shell

`internal/shell` is the window host. It creates the kernel, opens the settings store,
builds the window and menu bar, and every frame draws what plugins contribute. It
contains no feature. Its own plugin (`shell`, locked) provides `shell.HostKey` and the
"Toggle Sidebar" command.

| Point | Drawn |
|---|---|
| `shell.Sidebar` | sections stacked in the resizable sidebar; no entries, no sidebar |
| `shell.Pages` | the main area, one page at a time; the lowest Order is the default |
| `shell.TitleBar` | items at the end of the title strip |
| `shell.Overlays` | above everything (dialogs) |

Every render, command and title callback runs through `kernel.Invoke`, so a panic
disables only that plugin; the shell shows a toast naming it.

Commands with a `Menu` appear in the menu bar under that name (File, Chat, View,
Agent, Help, in that order), and the menu owns their first shortcut. Other shortcuts
are window shortcuts. The menu is rebuilt whenever plugins change state.

`shell.Host` gives plugins the window services they need: show a page, toasts,
clipboard and opening links.

## Chat page

`internal/plugins/chat` (locked) requires `agent.ServiceKey` and `shell.HostKey`,
provides `chat.ControllerKey`, and contributes the `chat.conversation` page with the
"New Chat" (⌘N) and "Stop the Agent" (⌘.) commands.

- Each open conversation has its own kernel scope. Switching disposes it, so a load,
  send or reply started for the previous conversation can't land in the next one.
- Sending runs `chat.Outgoing` first, then creates the conversation when it is new,
  then sends. The user's words show at once. A failed send is reported and never
  retried, since it may have reached the agent.
- One message at a time: the composer is a Stop button while a message is on its way
  or a turn runs.
- When the connection drops mid-turn, the turn is settled with a note. When a new
  connection attempt becomes ready, the open conversation is reloaded.
- `chat.ComposerAccessory` entries are drawn in the composer's leading row; a model
  or mode picker plugin goes there and sets `Draft.Model`/`Draft.Mode` through
  `chat.Outgoing`.

## Not in this step

The conversation list (sidebar), settings page, model picker, notifications and
unread marks are separate plugins still to come.
