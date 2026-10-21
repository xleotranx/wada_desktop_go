# Shell and the built-in plugins

Declared in `internal/contract/shell`, `internal/contract/chat` and
`internal/contract/settings`; implemented by `internal/shell` and the plugins named
below.

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
clipboard, opening links, desktop notifications (`Notify`) and whether the window is
in front (`Foreground`). A clicked notification brings the window to the front, then
its ID is emitted on `shell.NoticeClicked`. The app's notifications are cleared when
it becomes active. On macOS they need a packaged app (`mygo dev` or `mygo build`);
under `go run` nothing is shown.

## Chat page

`internal/plugins/chat` (locked) requires `agent.ServiceKey` and `shell.HostKey`,
provides `chat.ControllerKey`, and contributes the `chat.PageID` page with the
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
  `chat.Outgoing`. `chat.ComposerAttachments` entries are drawn above the editor.
- While a `chat.FileDrop` handler exists, the composer accepts dropped files and
  passes their paths to the first handler after the frame.
- A send the backend refused (`agent.ErrNotSent`) says the message was not sent; any
  other failed send says it may not have reached the agent.
- User messages show the files that went with them as chips.

## Sessions and settings

`internal/plugins/sessions` contributes the sidebar list (`shell.Sidebar`, growing to
fill it) and its rename/delete dialogs (`shell.Overlays`). It reloads the first page
when the backend becomes ready and after a turn ends, pages with "Show more", searches
after a short pause (a newer query cancels the older), and marks conversations that
finished a turn while another was open. It has no pins, archive or grouping by folder.

`internal/plugins/settings` contributes the settings page, a sidebar button and
"Settings…" (⌘,). It draws every `settings.Sections` entry in order, and adds its own
Appearance and Plugins sections; the latter turns plugins on and off through
`shell.Host`. The OpenCode, Pi and notifications plugins contribute their own sections.

## Model picker

`internal/plugins/models` requires `agent.ModelsKey` (so it is Blocked for a backend
without a catalog) and adds pickers to `chat.ComposerAccessory`: the model, its effort
when the model has variants, and the mode for a new conversation when the backend
offers more than one. For a new conversation the choice is saved in its settings and
applied through `chat.Outgoing`, unless the backend no longer offers it. In an open
conversation, choosing a model calls `SetModel` and shows the choice until the
backend's `ModelChanged` confirms it; a failure is reported and the choice reverts.
`chat.Conversation.Model` carries the open conversation's model.

## Attachments

`internal/plugins/attachments` adds a paperclip to `chat.ComposerAccessory`, an
"Attach Files…" command (⇧⌘A, Chat menu), a `chat.FileDrop` handler and a
`chat.Paste` handler: pasted files are attached, and so is a pasted image unless text
came with it (`shell.Host.Clipboard` reads both). Files the backend's
`agent.FileTypesKey` refuses (Pi: images and text only) are reported when added. Files are
read off the UI thread, typed by extension and contents (text stays `text/*`
whatever the extension), and shown as removable chips with image thumbnails in
`chat.ComposerAttachments`. A message takes at most 10 files, 10 MB each and 20 MB
in all; a folder, an unreadable file or one over a limit is reported in a toast. Its
`chat.Outgoing` transform runs last (order 1000): it refuses a send while files are
still being read, then moves the files into `Draft.Files`.

## Notifications

`internal/plugins/notifications` listens to `agent.Events` and notifies a finished or
failed turn, or an approval, of a conversation the user isn't looking at: another
conversation or page is on show, or the window isn't in front. An interrupted turn is
the user's own doing and isn't notified. A conversation whose title it doesn't know is
looked up in the backend's first page of sessions. The notice ID is `chat:<session>`;
clicking it opens that conversation. Its settings section turns each kind off.
