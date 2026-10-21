# Shell and the built-in plugins

Declared in `internal/contract/shell`, `internal/contract/chat` and
`internal/contract/settings`; implemented by `internal/shell` and the plugins named
below.

## Shell

`internal/shell` is the window host. It creates the kernel, opens the settings store,
builds the window and menu bar, and every frame draws what plugins contribute. It
contains no feature. Its own plugin (`shell`, locked) provides `shell.HostKey`,
`kernel.InspectorKey` and the
"Toggle Sidebar" command.

| Point | Drawn |
|---|---|
| `shell.Sidebar` | sections stacked in the resizable sidebar; no entries, no sidebar |
| `shell.Pages` | the main area, one page at a time; the lowest Order is the default |
| `shell.TitleBar` | items at the end of the title strip |
| `shell.Overlays` | above everything (dialogs) |

The conversation sidebar sits on the warm window background. Main pages occupy
an inset, rounded panel with a lighter canvas and a subtle border and shadow.
A separate window toolbar above both panes keeps the sidebar toggle at the left,
the page title centered over the conversation pane, and title-bar contributions
at the right. With the sidebar hidden, the title is centered over the window. The sidebar resize divider blends into the window
background while retaining pointer and keyboard resizing.

A `shell.Page` can set `HideSidebar` to use the full content area and omit the
sidebar toggle, while keeping the same toolbar and inset canvas. This leaves the saved sidebar visibility and width unchanged.

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
- General backend questions appear as typed interaction cards: Yes/No, every
  offered selection, a text input, or a multiline editor. Submission disables the
  card until its answer completes; a failed answer is reported without retrying.
  Questions are removed on success or when the turn settles.
- One message at a time: the composer is a Stop button while a message is on its way
  or a turn runs.
- When the connection drops mid-turn, the turn is settled with a note. When a new
  connection attempt becomes ready, the open conversation is reloaded.
- Sending waits for history to load. If live events overtake a history request,
  the live transcript stays on screen and history is read again after the turn
  settles. App notes survive the reload.
- `chat.ComposerAccessory` entries are drawn in the composer's leading row; a model
  or mode picker plugin goes there and sets `Draft.Model`/`Draft.Mode` through
  `chat.Outgoing`. `chat.ComposerAttachments` entries are drawn above the editor.
- `chat.Completions` entries offer words while the user types. When the caret is in a
  word that starts with an entry's trigger (at the start of the text or after
  whitespace) the composer lists the entry's candidates in a panel that hangs above
  the editor without moving the transcript. Up and Down move through them, Tab or
  Enter choose, Escape hides the list until the word changes, and a click chooses
  without taking the focus from the editor. A choice replaces the whole word with
  what the entry's `Accept` returns and puts the caret after it. The Send button
  still sends. The entry is told what was typed (`Query`) after each frame in which
  it changed and reads its candidates (`Candidates`) while drawing, so lookups
  belong to the entry; the composer keeps only the draft, the caret and the keys.
- While a `chat.FileDrop` handler exists, the composer accepts dropped files and
  passes their paths to the first handler after the frame.
- A send the backend refused (`agent.ErrNotSent`) says the message was not sent; any
  other failed send says it may not have reached the agent.
- User messages show the files that went with them as chips.

## Command palette

`internal/plugins/palette` adds "Command Palette…" (⌘K, View menu), which toggles
Onevra's command dialog from a `shell.Overlays` entry. It needs no other plugin: the
commands are every `kernel.Commands` entry that has a `Run`, shown with their
shortcut spelled as in a macOS menu, and one that is not `Enabled` is listed greyed
out and does nothing. Conversations come from `agent.Service.Sessions` when a backend
is active, so the palette keeps working with the backend off. Opening reads the
newest page; typing filters those and what a title search (after a short pause, a
newer query replaces the older) adds beyond them, with the same matcher as the
commands. With nothing typed it lists the five newest conversations, then the
commands. Choosing runs the command, or opens the conversation through
`chat.Controller`, once the handler that chose it has returned. The query and
anything a search left behind are dropped each time the palette opens.

## Sessions and settings

`internal/plugins/sessions` contributes the sidebar list (`shell.Sidebar`, growing to
fill it) and its rename/delete dialogs (`shell.Overlays`). It reloads the first page
when the backend becomes ready and after a turn ends, pages with "Show more", searches
after a short pause (a newer query cancels the older), and marks conversations that
finished a turn while another was open. Rows prioritize the conversation title;
tooltips show the full title and project path. The options button stays keyboard
accessible and appears on hover, focus, or selection. It has no pins, archive or
grouping by folder.

`internal/plugins/settings` contributes the settings page, a sidebar button and
"Settings…" (⌘,). The page hides the conversation sidebar and offers “Back to chat” above its own
navigation. It lists `settings.Sections` entries in order in a navigation column and renders
only the selected section in a separately scrolling detail pane. If that section
disappears, it shows the first available section. It adds its own Appearance and
Plugins sections; appearance offers System, Light and Dark choices, and the plugin
list uses switches to turn optional plugins on and off through `shell.Host`. The OpenCode, Pi and notifications plugins contribute their own sections.

## Plugin diagnostics

The `inspector` plugin adds View → Plugin diagnostics and a button in Settings.
Its read-only page lists every installed plugin. Expanding one shows required
service providers, missing requirements, owned registrations, scope and worker
counts, and any failure and recovered panic stack. It uses the public
`kernel.InspectorKey` service and shell page contributions.

## Model picker

`internal/plugins/models` requires `agent.ModelsKey` (so it is Blocked for a backend
without a catalog) and adds pickers to `chat.ComposerAccessory`: the model, its effort
when the model has variants, and the mode for a new conversation when the backend
offers more than one. For a new conversation the choice is saved in its settings and
applied through `chat.Outgoing`, unless the backend no longer offers it. In an open
conversation, choosing a model calls `SetModel` and shows the choice until the
backend's `ModelChanged` confirms it; a failure is reported and the choice reverts.
Switch work and the pending choice belong to that conversation; navigating away
cancels the work and drops its completion.
`chat.Conversation.Model` carries the open conversation's model. The model picker
has a subtle filled background; effort and mode use quieter text controls.

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

## File mentions

`internal/plugins/mentions` requires `agent.FilesKey`, so it is Blocked for a backend
that can't find and read files (Pi). It contributes a `chat.Completions` entry for
"@": typing it asks the backend's `Find` for files in the open conversation's folder
(the backend's default for a new chat) and lists up to eight, best first as the backend
ranks them, by name with their folder beside it. Choosing one writes `@<path> ` into
the message and starts reading the file through the backend's `Read`, off the UI
thread. Its `chat.Outgoing` transform runs after the attachments' (order 1001): every
file whose `@<path>` the text still names, as a word of its own, is added to
`Draft.Files` under its path, in the order the text names them; the text itself is
unchanged. A file still being read holds the message back. A file that can't go (over
1 MB, neither text nor an image, refused by `agent.FileTypesKey`, unreadable), or an
eleventh in one message, is reported in a toast; its path stays in the text, and the
agent may still open it. Mentions belong to the draft: they are dropped when the
message goes out or another conversation opens. A path typed by hand without choosing
it is plain text.

## Notifications

`internal/plugins/notifications` listens to `agent.Events` and notifies a finished or
failed turn, or an approval, of a conversation the user isn't looking at: another
conversation or page is on show, or the window isn't in front. An interrupted turn is
the user's own doing and isn't notified. A conversation whose title it doesn't know is
looked up in the backend's first page of sessions. The notice ID is `chat:<session>`;
clicking it opens that conversation. Its settings section turns each kind off.
