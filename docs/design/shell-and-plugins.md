# Shell and the built-in plugins

Declared in `internal/contract/shell`, `internal/contract/chat`,
`internal/contract/settings` and `internal/contract/workspace`; implemented by
`internal/shell` and the plugins named below.

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

The window is flat: the sidebar and the page share the window background, split by
a hairline that drags to resize the sidebar (and takes the keyboard). Each pane has
its own header strip that drags the window: the sidebar's holds the sidebar toggle
beside the window controls, the page's the page title at its start and title-bar
contributions at its end. With the sidebar hidden, the toggle moves into the page's
header before the title.

A `shell.Page` can set `HideSidebar` to use the whole window and omit the sidebar
toggle. This leaves the saved sidebar visibility and width unchanged.

Every render, command and title callback runs through `kernel.Invoke`, so a panic
disables only that plugin; the shell shows a toast naming it.

Commands with a `Menu` appear in the menu bar under that name (File, Chat, View,
Agent, Help, in that order), and the menu owns their first shortcut. Other shortcuts
are window shortcuts. The menu is rebuilt whenever plugins change state.

`shell.Host` gives plugins the window services they need: show a page, toasts,
clipboard, opening links, desktop notifications (`Notify`), whether the window is
in front (`Foreground`) and the app's data folder (`DataDir`, where `settings.db`
lives; a plugin names its files after its ID; empty in tests). A clicked notification brings the window to the front, then
its ID is emitted on `shell.NoticeClicked`. The app's notifications are cleared when
it becomes active. On macOS they need a packaged app (`mygo dev` or `mygo build`);
under `go run` nothing is shown.

## Chat page

`internal/plugins/chat` (locked) requires `agent.ServiceKey` and `shell.HostKey`,
provides `chat.ControllerKey`, and contributes the `chat.PageID` page with the
"New Chat" (⌘N) and "Stop the Agent" (⌘.) commands.

- A chat with nothing in it yet centers the composer between a greeting (with the
  chosen folder) and, for a new chat, starter suggestions that fill the draft. Once
  the transcript has rows the composer sits at the foot of the page; it keeps its
  place in the tree, so it keeps its focus as it moves.

- Each open conversation has its own kernel scope. Switching disposes it, so a load,
  send or reply started for the previous conversation can't land in the next one.
- Sending runs `chat.Outgoing` first, then creates the conversation when it is new,
  then sends. The user's words show at once. While the conversation is being created
  `chat.Conversation.Starting` is set. A created conversation is announced on
  `chat.Started` with the folder the draft named, and the open conversation keeps
  that folder even when the backend reports its default instead, so ⌘N stays in it.
  Creating belongs to the plugin, not the conversation's scope: when the user has
  opened another conversation meanwhile, the new one is still announced and its
  message sent, and a failure is a toast.
- `chat.Controller.SetDirectory` changes the folder of a new chat without touching
  the draft; it does nothing for a stored conversation or one that is Starting. A
  failed send is reported and never retried, since it may have reached the agent.
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
  `chat.Outgoing`. `chat.ComposerTrailing` entries are drawn just before Send or
  Stop and keep their width. `chat.ComposerAttachments` entries are drawn above the
  editor.
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

`internal/plugins/palette` adds "Command Palette…" (⌘K, View menu) under the ID
`shell.SearchCommand`, so the sidebar's search field opens it too. It toggles
Onevra's command dialog from a `shell.Overlays` entry. It needs no other plugin: the
commands are every `kernel.Commands` entry that has a `Run`, shown with their
shortcut spelled as in a macOS menu, and one that is not `Enabled` is listed greyed
out and does nothing. Conversations come from the workspace record
(`workspace.ServiceKey`) when it is active, so the palette keeps working without it:
only those the app started, with their folder. Typing filters them with the same
matcher as the commands. With nothing typed it lists the five newest conversations,
then the commands. Choosing runs the command, or opens the conversation through
`chat.Controller`, once the handler that chose it has returned. The query is dropped
each time the palette opens.

## Workspace

`internal/plugins/workspace` requires `agent.ServiceKey`, `chat.ControllerKey` and
`shell.HostKey`, and provides `workspace.ServiceKey`: the app's own record of the
folders the user works in and of the conversations started in this app. The backend
keeps the messages; the record says which conversations are the app's and where they
belong, so the sidebar, the palette and the notifications show nothing else.

- It records a conversation on `chat.Started`, with the folder the user chose (empty
  for the backend's default, and for a folder that isn't absolute) and the backend's origin (`agent.Status.Origin`). A chosen
  absolute folder that isn't known yet is added. Only the current origin's
  conversations are listed, newest first; switching backend or server changes the list.
- It follows `agent.Events`: a turn starting, ending or interrupted is activity;
  `SessionRenamed` and `ModelChanged` update the title and model. When a recorded
  conversation finishes a turn it reads the backend's newest page for titles, and once
  per connection attempt it reads up to 20 pages until it has seen every recorded
  conversation or reached activity more than ten minutes older than the oldest
  one's creation. A failed connection sync is tried again after the next finished
  turn. A conversation the backend doesn't list stays recorded: a listing can't
  prove it is gone.
- `ChooseFolder` shows the system folder dialog off the UI thread, one at a time; the
  chosen folder is added before its callback runs. "Open Folder…" (⌘O, File menu)
  chooses one and starts a new chat there. A folder is a path on this Mac; with a
  remote OpenCode server the same path must exist there.
- For a new chat it adds a folder picker to `chat.ComposerAccessory` (leading): the
  known folders, "No folder" (the agent's default) and "Choose folder…", applied
  through `chat.Controller.SetDirectory` after the frame. A stored conversation, or
  one Starting, shows none.
- Folders are listed by name. Removing one forgets the record of its conversations
  under every origin; nothing is deleted from the backend or the disk.

The record is kept in `workspace.db` in `shell.Host.DataDir` and read into memory when
the plugin starts; until then `Loaded` is false and the lists are empty. Changes apply
in memory and are announced on `workspace.Changed` at once. One worker writes them in
order, a transaction per batch, and writes what is still queued when the plugin stops.
Every statement is an upsert or a delete, so a failed batch is written again with the
next change; the first failure in a run is reported in a toast. A database of another
schema is refused, not migrated: the record lives in memory for that run and a toast
says so. Without a data folder (tests) it always lives in memory.

```sql
-- PRAGMA user_version = 1
CREATE TABLE folders (
  path      TEXT PRIMARY KEY,   -- absolute and clean
  collapsed INTEGER NOT NULL,   -- the sidebar shows only its header
  added     INTEGER NOT NULL);  -- Unix ms
CREATE TABLE conversations (
  origin         TEXT NOT NULL, -- agent.Status.Origin
  session        TEXT NOT NULL,
  folder         TEXT NOT NULL, -- a folders.path, or '' for the backend's default
  title          TEXT NOT NULL,
  model_provider TEXT NOT NULL, model_id TEXT NOT NULL, model_variant TEXT NOT NULL,
  created        INTEGER NOT NULL,
  updated        INTEGER NOT NULL, -- last activity seen, Unix ms
  PRIMARY KEY (origin, session));
```

## Sessions and settings

`internal/plugins/sessions` requires the workspace record and contributes the sidebar
list (`shell.Sidebar`, growing to fill it) and its dialogs (`shell.Overlays`). Above
the list are "New chat" (in the open conversation's folder, as ⌘N), an "Open folder…"
button and the search field. The field is a button that runs `shell.SearchCommand`
(the palette's command) when a plugin contributes it; without one it is a text field
that filters the list. Each folder has a header with its name that collapses it, remembered in the record, and, on hover or focus, an options menu
(New Chat, Remove Folder…) and, last, a "+" that starts a chat there. Under it are
its conversations, newest first, or "No conversations". Conversations without a known
folder follow under "Other chats"; with no folders at all the list is flat. The inline
filter matches titles as you type, shows matches under their folders even when collapsed, and
hides folders without one. With no conversations at all the list is a short note with
an "Open folder…" button. Rows show the title, its last activity ("5m", "3h", a date)
while the options button is hidden, an unread mark for a recorded
conversation that finished a turn while another was open, and an options menu
(Rename…, Remove from List, Delete…) that stays keyboard accessible and appears on
hover, focus or selection. Menu choices act after the frame. The rows are laid out
again only when `workspace.Changed` arrives or the search changes. Rename and delete
go to the backend, then the record;
"Remove from List" only forgets the record. A failed delete is reported and the
conversation stays listed. Removing a folder asks first and counts the conversations
that leave the list.

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

## Usage

`internal/plugins/usage` requires `agent.ServiceKey` and `chat.ControllerKey` and adds
the open conversation's usage to `chat.ComposerTrailing`: its total cost, then
Onevra's context ring for how full the context window is, from the latest assistant
message's `Tokens.Context` over its model's `Model.Context`. Clicking the ring shows
the numbers ("44k / 200k (22%)"); without a catalog or a window it shows the tokens
alone. Backends report usage only with stored messages, so the plugin reads the
conversation's `Messages` when it opens and again when one of its turns ends. A read
belongs to the conversation it was for: a later navigation drops it, and a failed
read shows nothing, since the chat page reports the failure. A new chat, or a
conversation with no usage, shows nothing. The model catalog is read once.

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
conversation or page is on show, or the window isn't in front. While the workspace
record is active, only the conversations it holds are notified, titled from it; until
the record has been read, notices wait and are shown on `workspace.Changed`. An interrupted turn is
the user's own doing and isn't notified. A conversation whose title it doesn't know is
looked up in the backend's first page of sessions. The notice ID is `chat:<session>`;
clicking it opens that conversation. Its settings section turns each kind off.
