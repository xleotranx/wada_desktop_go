# Shell and the built-in plugins

Declared in `internal/contract/shell`, `internal/contract/chat`,
`internal/contract/settings`, `internal/contract/workspace` and
`internal/contract/workflow`; implemented by `internal/shell` and the plugins named below.

## Shell

`internal/shell` is the window host. It creates the kernel, opens the settings store,
builds the window and menu bar, and every frame draws what plugins contribute. It
contains no feature. Its own plugin (`shell`, locked) provides `shell.HostKey` and
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

`shell.Host.ShowPage` emits `shell.PageChanged` with the ID it chose whenever that
differs from the last one.

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
"New Chat" (⌘N), "Toggle Work Panel" (⌘B, View) and "Stop the Agent" (⌘.) commands.

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
- Just before the chat replaces the open conversation it emits `chat.Leaving` with the
  composer's unsent text, empty while an earlier message was being edited.
  `chat.Controller.SetDraft` puts text in the composer, ends an edit and focuses it.
- `chat.Controller.SetDirectory` changes the folder of a new chat without touching
  the draft; it does nothing for a stored conversation or one that is Starting. A
  failed send is reported and never retried, since it may have reached the agent.
- General backend questions appear as typed interaction cards: Yes/No, every
  offered selection, a text input, or a multiline editor. Submission disables the
  card until its answer completes; a failed answer is reported without retrying.
  Questions are removed on success or when the turn settles.
- A turn's steps show as they happen. Once it settles, its work — from its first
  words or tool call through its last tool call — folds under one "Worked · N
  steps" header, closed, and the answer after it stands alone. Opening the header
  shows the steps as they were. Of the agent's words, only that answer offers
  Copy; the user's messages always do.
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
- Beside a stored conversation the page draws the work panel, a 300 px column on
  `DrawerBg` at its end: each `chat.Work` section whose `Shown` holds (or has none),
  in order, its title over what it renders. The panel shows while at least one
  section does and the `work-panel` setting is on; ⌘B turns the setting over.
  `Controller.WorkShown` reports it, so a plugin can step aside in the composer.
- `chat.Targets` entries are other places a new chat's first message can go than a
  conversation with the agent, such as a workflow. A target has a group, a title, a
  summary and `Take(Draft)`. The mode picker lists them; the plugin that owns the
  choice makes a `chat.Outgoing` transform return `chat.ErrTaken` after handing the
  draft to `Take`. Chat then empties the composer and sends nothing, shows no error,
  and leaves attached files in place.
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
- With a backend that provides `agent.RewindKey`, the last user message shows Retry
  and Edit beside Copy while no turn runs or loads. Retry sends its words again as
  they were sent (not through `chat.Outgoing`, which would take the composer's files
  and mentions); it is not offered for a message with files, which can't go again.
  Edit puts the words in the composer under a banner with Cancel; sending runs
  `chat.Outgoing` as usual. Either way the last turn leaves the transcript at once,
  then the conversation's messages are read for the last user message's ID (one sent
  in this session has none on show), `Rewind` takes it back and the new words are
  sent. A conversation the backend forks into becomes the open one, is announced on
  `chat.Started`, and a toast says the original was kept. A failed read or rewind
  sends nothing: it is noted and the conversation read again.
- User messages show the files that went with them as chips.

## All chats

`internal/plugins/palette` adds "All Chats…" (⌘K, View menu) under the ID
`shell.SearchCommand`, so the tab strip's search button opens it too. It toggles
Onevra's command dialog from a `shell.Overlays` entry. It needs no other plugin: the
commands are every `kernel.Commands` entry that has a `Run`, shown with their
shortcut spelled as in a macOS menu, and one that is not `Enabled` is listed greyed
out and does nothing. Conversations and folders come from the workspace record
(`workspace.ServiceKey`) when it is active, so the palette keeps working without it:
only the conversations the app started, with their folder.

With nothing typed it lists, in groups: the conversations open in tabs (from
`tabs.ServiceKey`, when there is one), the five newest others ("Earlier"), the
folders ("Projects", each with its count of chats, then "Open Folder…"), then the
commands. Typing filters all of them with one matcher, older conversations
included. Choosing runs the command, opens the conversation through
`chat.Controller` (the tabs follow), or starts a new chat in the folder, once the
handler that chose it has returned. The query is dropped each time the palette opens.

## Workspace

`internal/plugins/workspace` (locked) requires `agent.ServiceKey`, `chat.ControllerKey`
and `shell.HostKey`, and provides `workspace.ServiceKey`: the app's own record of the
folders the user works in and of the conversations started in this app. The backend
keeps the messages; the record says which conversations are the app's and where they
belong, so the tabs, All chats and the notifications show nothing else. It and the
tabs are locked: without them the app would keep no record of its
conversations and offer no way back to one. Like Chat, they still become Blocked
while no backend is active.

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
  collapsed INTEGER NOT NULL,   -- its tab group shows only its label
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

## Tabs

`internal/plugins/tabs` (locked) requires `agent.ServiceKey`, `chat.ControllerKey`,
`shell.HostKey` and `workspace.ServiceKey`, provides `tabs.ServiceKey` and contributes
the `shell.Strip` view and its dialogs (`shell.Overlays`).

- A tab only points at what it shows: a stored conversation, a new chat in a folder
  (a draft), or a shell page other than the chat. The chat still has one open
  conversation; selecting a tab opens it there (`Open`, `NewChat`) or shows its page.
- Tabs follow navigation rather than drive it. After `chat.Changed` while the chat
  page is on show, and after `shell.PageChanged`, what is shown becomes the active
  tab: an open tab is selected, otherwise one opens. A new chat takes over the active
  draft tab, and so does a stored conversation shown from one: sending a draft turns
  its tab into the conversation. A draft with unsent text keeps its tab, and the
  conversation opens in a new one. A conversation announced on `chat.Started` after the
  user moved on opens in a background tab.
- Tabs are grouped by folder: a group's tabs are adjacent, after a label with the
  folder's name in its project color and over a line in that color; tabs without a
  folder have neither. A new tab opens beside the active one when that is in its group,
  else after its group, else at the end; a draft whose folder changes moves to its new
  group. Each folder keeps the first of the palette's project colors no other folder
  has, saved under `colors`.
- A conversation tab shows what the conversation is doing, from `agent.Events`:
  working once a turn starts or produces output, waiting for an approval or an answer,
  unread when a turn ends while the tab is not on show (an interrupted turn is not),
  idle otherwise. Showing the tab clears unread. `SessionRenamed` and the workspace
  record keep titles current.
- Each tab keeps the text typed and not sent in it. On `chat.Leaving` the text is
  kept under the tab the chat leaves (a new chat's is the last draft tab shown), and
  `chat.Controller.SetDraft` gives it back when that tab is shown again. It is kept
  for the run only; files attached and mentions are dropped, as on any switch.
- Closing a tab deletes nothing. Closing the active tab shows its right neighbour,
  else its left one; closing the last one opens a new chat in its folder. A tab whose
  conversation the record stops listing closes.
- The open tabs and the active one are saved (`open`, `active`) and reopened once the
  workspace record is read, without the conversations it no longer lists and pages no
  plugin contributes. A conversation the chat already shows by then becomes the active
  tab instead.
- After `kernel.PointChanged` for `shell.Pages`, a page tab whose page is gone closes,
  and the others take their page's current title.
- The strip draws a tab's marker, title and a close button (shown on the active and
  hovered tab, and on focus), then a "+" that opens a new chat in the active tab's
  folder, then "All chats" (runs `shell.SearchCommand` when contributed) and Settings
  (shows `settings.PageID` when contributed).
- A group's label is a menu: New Chat in the folder, Collapse Group (remembered in the
  record as `Folder.Collapsed`; a collapsed group shows its label with a count and
  only its active tab) or Expand Group, and Remove Folder…, which asks first, counting
  the conversations that leave the record, then closes the group's tabs.
- A tab's context menu has Close Tab, and for a conversation Rename…, Remove from List
  and Delete…. Rename and delete go to the backend, then the record; Remove from List
  only forgets the record. Either closes the tab. A failed rename or delete is
  reported and changes nothing. Menu choices act after the frame.
- Commands: Close Tab (⌘W, File), Show Next Tab (⌃Tab) and Show Previous Tab (⌃⇧Tab,
  View).

## Settings

`internal/plugins/settings` contributes the settings page and "Settings…" (⌘,). The
page offers “Back to chat” above its own navigation. It lists `settings.Sections` entries in order in a navigation column and renders
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
offers more than one choice. It reads the catalog when a new connection attempt is
ready and again on `agent.ModelsChanged`. A backend without modes shows "Chat" when targets exist.
The mode picker also lists `chat.Targets` after the modes, under the group each one
names. Choosing a target applies to the next message only: it is not saved, the picker
returns to the chosen mode once the message is sent, and it is dropped when another
conversation opens, the new chat's folder changes or a later mode is chosen. The message
goes to the target's `Take` through `chat.Outgoing` (order 0, before attachments and
mentions) and the transform returns `chat.ErrTaken`. A target that is gone, or whose
`Take` fails, makes the transform return an error instead: the message is not sent to
the agent in its place and stays in the composer. For a new conversation the mode choice is saved in its
settings and applied through `chat.Outgoing`, unless the backend no longer offers it. In an open
conversation, choosing a model calls `SetModel` and shows the choice until the
backend's `ModelChanged` confirms it; a failure is reported and the choice reverts.
Switch work and the pending choice belong to that conversation; navigating away
cancels the work and drops its completion.
`chat.Conversation.Model` carries the open conversation's model. The model picker
has a subtle filled background; effort and mode use quieter text controls.

## Usage

`internal/plugins/usage` requires `agent.ServiceKey` and `chat.ControllerKey`. What
fills the context is the latest assistant message that reported tokens: its
`Tokens.Context` over its model's `Model.Context`. It contributes two `chat.Work`
sections:

- **Context**: used over the window, a strip with each kind of token in its own color
  and the free space, and a line for each kind (input, cache read, cache write,
  output, reasoning) and the free space with its share of the window, or of what is
  used when the window is unknown;
- **Cost**: the model (with its variant), the number of replies, the total cost and
  the latest reply's ("Free" on a free model), then the conversation's tokens of each
  kind, summed over every reply.

While the work panel is hidden it adds a button to `chat.ComposerTrailing` instead:
the total cost, the share of the context window in use and a ring that fills with it
(the accent, a warning from 80%, danger from 95%), opening a popover with both
sections.

Backends report usage only with stored messages, so the plugin reads the conversation's
`Messages` when it opens and again when one of its turns ends. A read belongs to the
conversation it was for: a later navigation drops it, and a failed read shows nothing,
since the chat page reports the failure. A new chat, or a conversation with no usage,
shows nothing. The model catalog is read once, for names and context windows.

## Activity

`internal/plugins/activity` requires `agent.ServiceKey` and `chat.ControllerKey` and
contributes two `chat.Work` sections for the conversation on show:

- **Changes**, when the backend provides `agent.ChangesKey`: the number of files and
  the lines added and removed, then each file (its name, colored when added or
  deleted, its folder and its counts), which opens to its diff: hunk headers muted,
  added and removed lines on `DiffAddBg` and `DiffDelBg`, up to 400 lines. The
  changes are read when the conversation opens and after each of its turns ends; a
  failed read keeps what was shown.
- **Last command**: the latest finished shell call, read from the stored messages
  when the conversation opens and then taken from `ToolFinished` events (a call
  counts as a shell command when its `ToolStarted` or `ToolCalled` said so, or named
  a command): the command, the last 12 lines of its output and its exit status.

Each section shows only while it has something to show.

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

## Workflows

A flow is Go code that drives the agent backend through conversations the user does not
see, and returns a result for the user. `internal/contract/workflow` declares the types:
`Flow{ID, Title, Summary, Run}`, `Input{Brief, Directory}`, and the `Runner` a flow's
`Run(ctx, Runner) (string, error)` is called with. `Runner.Step(ctx, Step)` sends one
prompt, waits for the agent's turn to end and returns an `Answer{Session, Text}`; the
flow decides from the text what to ask next, so branches and loops are plain Go. A
`Step` names itself, its prompt, optionally an agent mode, and a `Session` to continue
(an earlier answer's) instead of opening a new conversation. Flows use only the agent
contract, so they run on every backend.

`internal/plugins/workflow` provides `workflow.ServiceKey` and knows no flow. `Prompt(f)`
opens a dialog for the brief, in the open chat's folder with a "Choose…" button when the
workspace record is active; `Start(f, in)` runs the flow on a worker (`Spawn`) whose
context ends when the user stops the run, and shows the run's page. A panic in a flow
fails its run, not the plugin. At most eight runs are kept; the oldest finished ones go
first, and their pages with them.

- **Steps.** `Step` opens the conversation with `agent.Service.Create` (in the run's
  folder, with the step's mode) unless the step continues one, registers it with the
  engine on the UI thread, then sends the prompt and waits. `agent.Events` for a watched
  conversation drive everything: `TextEnded` keeps the latest finished text block, which
  is the answer when the turn succeeds (the stored messages stand in when the events
  named none); `TurnSucceeded`, `TurnFailed` and `TurnInterrupted` wake the flow's
  goroutine. A prompt the backend consumed as a command (`SendHandled`) fails the step.
- **Permissions.** A step's `Policy{Allow, Deny}` lists `agent.ToolKind`s answered
  without asking: Deny rejects, Allow allows once, and Deny wins. Any other request waits
  in the step's card with Allow once, Allow for this run (every request of that kind in
  the run from then on, those already waiting included) and Deny. A reply that can't be
  delivered fails the attempt. Questions (`InteractionRequested`) are dismissed through
  `agent.InteractionsKey` and the step says so; a backend that can't take the dismissal
  fails the attempt.
- **Failures.** The engine retries no mutation. A failed attempt (the conversation can't
  be created, the prompt can't be sent, the turn fails) leaves the step "Needs your
  decision": Try again repeats the attempt in the conversation already opened, Stop run
  ends the run. Stop aborts a turn that is still running; if the abort fails, the step
  says the agent may go on. Step returns `ErrStopped` once the run is stopped.
- **Hidden conversations.** Step conversations are not recorded in the workspace, so the
  tabs, All chats and the notifications ignore them. "Open in chat" on a step emits
  `chat.Started` once, which records it, then opens it in the chat.
- **Pages.** Each run kept contributes its own `shell.Pages` entry (`workflow.run/<n>`,
  order 50), titled by its brief's first line, from a child scope disposed with the
  run; the tabs show it as a tab. It shows the brief and folder, a badge (Running,
  Needs you, Done, Failed, Stopped), a card for each step with its state, the latest
  tool activity or a preview of its answer, "Show answer" (the answer as markdown) and
  "Open in chat", and at the end the result as markdown with Copy. Beside them, when
  there is something to say, a column lists the files each step's conversation
  changed (read through `agent.ChangesKey` when the step's turn succeeds), what the
  steps may do without asking (each kind with the steps whose policy allows it, and
  "this run" for what the user allowed for the run) and what they may never do. A run
  that ends while its page is not on show raises a toast, or a desktop notice (ID
  `workflow`, whose click shows the run) when the window is not in front; a stop the
  user asked for is not announced. "Show Workflow Run" (View menu) opens the run that
  needs the user, else the newest.

`internal/plugins/flows` brings the app's flows. Each is a `chat.Targets` entry in the
group "Workflows", so it is in the composer's mode picker beside Build and Plan: choosing
it and sending a new chat's first message starts the flow with that text as the brief,
in the folder chosen for the chat (`Take` calls `Start`), with no dialog. Each also gets
a command, "<Title>…" in the Agent menu and the palette, that calls `Prompt`. **Plan, code, review** runs a plan step
(read-only: edits are denied), a code step, then a review step (read-only) that must end
with `VERDICT: PASS` or `VERDICT: FAIL`. A FAIL sends the findings back to the coder in
its own conversation ("Fix 1") and a new reviewer looks again ("Review 2"), for at most
three reviews. The result is how the review ended, the coder's last report and the
reviewer's findings; a review with no verdict ends the run and says so. Steps read and
search without asking; edits by the coder, and commands in any step, are put to the user.
