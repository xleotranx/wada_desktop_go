# Shell and the built-in plugins

Declared in `internal/contract/chat`, `internal/contract/tabs`,
`internal/contract/workspace` and `internal/contract/workflow`, beside the kit's
`contract/shell` and `contract/settings`; implemented by the plugins named below.

## Shell

The window, its settings store, the settings page and plugin diagnostics come from
the independently versioned kit (`github.com/xleotranx/wada-kit`, pinned in
`go.mod`); see its [shell note](https://github.com/xleotranx/wada-kit/blob/v0.3.1/docs/shell.md)
and [dependency maintenance](kit-dependency.md). Wada's `main.go` runs it as:

- Name `Wada`, data folder `wada-desktop-go` (`wada-desktop-go-dev` for a
  development run), environment prefix `WADA` (`WADA_DATA_DIR`, `WADA_CAPTURE`,
  …);
- a 1400 × 900 window, at least 900 × 600;
- menus File, Chat, View, Agent, Help.

The tabs plugin's strip fills the title strip, so the page title shows only in the
window title.

## Chat page

`internal/plugins/chat` (locked) requires `agent.ServiceKey` and `shell.HostKey`,
provides `chat.ControllerKey`, and contributes the `chat.PageID` page with the
"Toggle Work Panel" (⌘B, View) and "Stop the Agent" (⌘.) commands, and a
Conversation settings section with the work panel's switch.

- A chat with nothing in it yet centers the composer between a greeting (with the
  chosen folder) and, for a new chat, the `chat.Starters` views. A new chat with no
  folder, no draft and no Inbox choice shows the folder gate instead of the
  composer: **Open a work folder** asks the workspace for one, and **Ask without a
  folder** opens the composer in the backend's Inbox. Once the transcript has rows the composer sits at the foot of
  the page; it keeps its place in the tree, so it keeps its focus as it moves.
- A `chat.Requirements` entry stands in front of an empty new chat while something
  is missing. PiG contributes one while no provider is connected: its setup reuses
  the provider search, list and login form from Settings → Providers and draws in
  place of the folder gate and composer. When a provider connects, the provider
  list reloads, the models catalog reloads and the folder gate appears.

- Each open conversation has its own kernel scope. Switching disposes it, so a load,
  send or reply started for the previous conversation can't land in the next one.
- Sending runs `chat.Outgoing` first, then creates the conversation when it is new,
  then sends. A transform takes nothing from the composer while it prepares the
  draft: the chat commits the transforms only once the whole chain has accepted it
  and the message goes out, so a rejection anywhere leaves every plugin's state. The user's words show at once. While the conversation is being created
  `chat.Conversation.Starting` is set. A created conversation is announced on
  `chat.Started` with the folder the draft named, and the open conversation keeps
  that folder even when the backend reports its default instead, so ⌘N stays in it.
  Creating belongs to the plugin, not the conversation's scope: when the user has
  opened another conversation meanwhile, the new one is still announced and its
  message sent, and a failure is a toast.
  Each new chat has a nonzero `DraftID`, unique within the chat plugin's lifetime,
  carried through its first outgoing draft and the `Changed` and `Started` events
  of its creation. Opening a stored conversation gives it zero.
- Just before the chat replaces the open conversation it emits `chat.Leaving` with the
  composer's unsent text, empty while an earlier message was being edited.
  `chat.Controller.SetDraft` puts text in the composer, ends an edit and focuses it.
  `DraftText` reads unsent text, empty while an earlier message is being edited.
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
- An answer that is arriving shows a word at a time, at an even pace that catches
  up with what came within 250 ms, 30 frames a second while the window is in front
  (`shell.Host.Foreground`) and 15 otherwise; with the system's reduced motion it
  shows as it comes. The chat paces the words itself and gives Markdown what shows,
  which Markdown draws at once (`Reveal` `AtOnce`). While a paced frame is due, a
  batch of words alone asks for none (`Scope.FrameDue`); other events of the open
  conversation ask for one. A finished answer shows what is left of it at the same
  pace.
- A turn in flight with nothing arriving ends the transcript with a "Working…"
  shimmer. After 2 s without an event of the conversation it says how long instead
  ("Working · 1m 05s"), drawn once a second.
- One message at a time: the composer is a Stop button while a message is on its way
  or a turn runs.
- When the connection drops mid-turn, the turn is settled with a note. When a new
  connection attempt becomes ready, the open conversation is reloaded.
- A note that history failed to load goes when a later read works; other notes
  survive a reload.
- Sending waits for history to load. If live events overtake a history request,
  the live transcript stays on screen and history is read again after the turn
  settles. App notes survive the reload.
- The eight conversations left most recently keep their transcript, scroll place,
  and opened tool calls and folds in memory, and their live events still apply to
  it. Opening one shows it at once as it was left, then reads it again in the
  background; a read that returns what the transcript was built from, untouched
  since, leaves it as it is, and any other replaces it. One whose turn is still
  running is read when the turn ends instead. A dropped connection forgets the
  kept conversations whose turns it cut off.
- `chat.ComposerPrimary` entries stay visible in the footer below the editor,
  such as the model picker. `chat.ComposerAccessory` entries fill the leading
  side and `chat.ComposerTrailing` entries follow the model, such as usage.
  The footer can wrap to preserve control widths. Below 600 px of footer width, accessories and trailing entries move into one
  **More composer options** popover. Send/Stop stays inside the editor card.
  `chat.ComposerAttachments` entries are drawn above the editor. Picker plugins
  set `Draft.Model`/`Draft.Mode` through `chat.Outgoing`.
- Beside a stored conversation the page draws the work panel, a 300 px column on
  `DrawerBg` at its end: each `chat.Work` section whose `Shown` holds (or has none),
  in order, its title over what it renders. The panel shows while at least one
  section does, the `work-panel` setting is on and no `chat.HideWork` entry reports
  true; the setting starts off so a stored conversation opens on the transcript,
  ⌘B or Conversation settings turns it on, and a plugin that hides the panel leaves
  the setting as it is.
  `Controller.WorkShown` reports it, so a plugin can step aside in the composer.
- `chat.Drawers` entries are drawn at the page's right edge, after the work panel.
  Each draws its own column, with its width and border, or nothing while closed.
- The transcript is selectable. As the drag ends, at the first frame where neither
  the selection nor the pointer moved (MyGo tells only the paragraph pressed of the
  release, and a held pointer draws frames only as it moves), a small popover
  stands under where the pointer let go, inside the transcript. It
  holds the `chat.SelectionActions` entries in order, each a title and an optional
  icon, then the chat's own **Reply**, which adds the selection to the composer as
  a Markdown quote (`> ` on each line) after the unsent text and focuses it. An
  action gets a `chat.Selection`: the open conversation and the selected words, a
  paragraph per line. The popover closes when an action runs, the selection goes,
  or the pointer goes down outside it, as in the composer; a new selection brings
  it back. The pointer's place is asked for only while a selection is being made.
- `chat.Workflows` lists one-task behaviors in the mode picker. The outgoing draft
  carries the selected `Workflow` through all transforms, including attachments,
  mentions and skills. Selection clears on commit. The optional `chat.DeliveryKey`
  validates before commits, admits prepared drafts after conversation creation,
  serializes execution, reports busy state and handles Stop. Without it chat sends
  directly to the backend; a selected workflow is refused rather than silently ignored.
- `chat.Activities` contributes stable transcript entries after a one-based user
  message ordinal. Contributions render through `kernel.Invoke`; plugin state and
  widget identity stay with the contributor. `UserAdmitted` reconciles queued inputs
  after navigation, and `QueueRemoved` removes an unsent queued input from the view.
  `chat.Incoming` presents child approvals and questions inside their parent, while
  preserving the real owner for replies. `RequestAnswered` removes successfully
  answered child requests from replay state, including after navigation.
- `chat.Completions` entries offer words while the user types. When the caret is in a
  word that starts with an entry's trigger (at the start of the text or after
  whitespace) the composer lists the entry's candidates in a panel that hangs above
  the editor without moving the transcript. Up and Down move through them, Tab or
  Enter choose, Escape hides the list until the word changes, and a click chooses
  without taking the focus from the editor. An entry's `Hint` ("mentions a file")
  joins the empty composer's hint after its trigger, so it names only what is
  active. A choice replaces the whole word with
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
  they were sent (not through `chat.Outgoing`, which would take the composer's files,
  mentions and skills); it is not offered for a message with files, which can't go again.
  Edit puts the words in the composer under a banner with Cancel; sending runs
  `chat.Outgoing` as usual. Either way the last turn leaves the transcript at once,
  then the conversation's messages are read for the last user message's ID (one sent
  in this session has none on show), `Rewind` takes it back and the new words are
  sent. A conversation the backend forks into becomes the open one, is announced on
  `chat.Started`, and a toast says the original was kept. A failed read or rewind
  sends nothing: it is noted and the conversation read again.
- User messages show the files that went with them as chips.

## New chat starters

`internal/plugins/starters` requires `chat.ControllerKey` and contributes the
`chat.Starters` view. On `chat.Changed` it reads the new chat's folder off the UI
thread; a stored conversation or a chat without a folder clears the read. Only the
latest read lands, and only while the same new chat still shows that folder. A
folder that cannot be read says so under the composer.

The read counts the folder's top-level files, skipping hidden files, directories and
symlinks. Code is a source extension or a project marker such as `go.mod`,
`package.json` or `Cargo.toml`. A folder with a marker or source files gets
code starters; an empty folder gets scaffold/explore starters; any other folder
gets general folder starters. The first three code names show above the list,
with the rest counted. Choosing a starter clears any stale workflow selection and
fills the composer; the user sends it.

## All chats

`internal/plugins/palette` adds "All Chats…" (⌘K, View menu) and a search button to
`tabs.EndActions`, which opens it from the tab strip. It toggles
Onevra's command dialog from a `shell.Overlays` entry. It needs no other plugin: the
commands are every `kernel.Commands` entry that has a `Run`, shown with their
shortcut spelled as in a macOS menu, and one that is not `Enabled` is listed greyed
out and does nothing. Conversations and folders come from the workspace record
(`workspace.ServiceKey`) when it is active, so the palette keeps working without it:
only the conversations the app started, with their folder.

With nothing typed it lists, in groups: the conversations open in tabs (from
`tabs.ServiceKey`, when there is one), the five newest others ("Earlier"), the five
folders worked in last ("Projects", by their newest conversation or when they were
added, each with its count of chats), then the commands. Typing filters all of them with one matcher, older conversations
included. Choosing runs the command, opens the conversation through
`chat.Controller` (the tabs follow), or starts a new chat in the folder, once the
handler that chose it has returned. The query is dropped each time the palette opens.

## Workspace

`internal/plugins/workspace` (locked) requires `agent.ServiceKey` and `shell.HostKey`,
and provides `workspace.ServiceKey`: the app's own record of the
folders the user works in and of the conversations started in this app. The backend
keeps the messages; the record says which conversations are the app's and where they
belong, so the tabs, All chats and the notifications show nothing else. It and the
tabs are locked: without them the app would keep no record of its
conversations and offer no way back to one. Like Chat, they still become Blocked
while no backend is active. The record needs no chat: it hears `chat.Started` but
requires no controller, so its consumers keep it when the chat fails.

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
  chosen folder is added before its callback runs. A folder is a local path used by
  the embedded agent.
- Folders are listed by name. Removing one forgets the record of its conversations
  under every origin; nothing is deleted from the backend or the disk.

The record is kept in `workspace.db` (pure-Go SQLite with WAL and numbered migrations)
in `shell.Host.DataDir` and read into memory when
the plugin starts; until then `Loaded` is false and the lists are empty. Changes apply
in memory and are announced on `workspace.Changed` at once. One worker writes them in
order, a transaction per batch, and writes what is still queued when the plugin stops.
Every statement is an upsert or a delete, so a failed batch is written again with the
next change; the first failure in a run is reported in a toast. An unrecognized
database or future unsupported schema is refused: the record lives in memory for
that run and a toast says so. Without a data folder (tests) it always lives in memory.

```sql
-- PRAGMA user_version = 1
CREATE TABLE folders (
  path      TEXT PRIMARY KEY,   -- absolute and clean
  collapsed INTEGER NOT NULL,   -- unused; always 0
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

`internal/plugins/folderpicker` requires `chat.ControllerKey` and
`workspace.ServiceKey`. "Open Folder…" (⌘O, File menu) chooses a folder and starts a
new chat there. For a new chat it adds a folder picker to `chat.ComposerAccessory`
(footer leading): the known folders, "No folder" (the agent's default) and "Choose
folder…", applied through `chat.Controller.SetDirectory` after the frame. A stored
conversation, or one Starting, shows none.

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
  conversation, or a new chat in another folder, opens in a new one. A conversation announced on `chat.Started` after the
  user moved on opens in a background tab.
- The strip shows one space at a time: the tabs of one folder (or of the agent's
  default folder), then the page tabs, which every space shows. Showing a conversation
  or a draft makes its folder the space; showing a page leaves the space as it is.
  Each space remembers the tab shown there last. A folder's tabs stay adjacent: a new
  tab opens beside the active one when that is in its folder, else after its folder's
  tabs, else at the end; a draft whose folder changes moves with it, and the space
  follows. `tabs.Service.Space` names the space on show.
- The switcher leads the strip: the space's name after a dot in its project color,
  and an orange mark while a conversation in another space waits for the user or has
  an unread end. It opens a popover listing every space (the workspace's folders, then
  the folders of open tabs it does not list, "No folder" among them), each with its
  color, the most urgent state of its conversations, its count of tabs and a check on
  the one on show. Choosing one shows its last tab, else its first, else a new chat in
  it. Below the list: New Chat in the space, Open Folder… (which starts a chat in the
  chosen folder), Remove “name”… for a folder, which asks first, counting the
  conversations that leave the record, then closes its tabs; and Tab Settings….
  Each folder keeps the first of the palette's project colors no other folder has,
  saved under `colors`.
- A conversation tab shows what the conversation is doing, from `agent.Events`:
  working once a turn starts or produces output, waiting for an approval or an answer,
  unread when a turn ends while the tab is not on show (an interrupted turn is not),
  idle otherwise. Showing the tab clears unread. `SessionRenamed` and the workspace
  record keep titles current. States are kept for conversations without a tab as
  well; only a change to a tab on the strip asks for a frame.
- A tab's marker is its state: a spinner while working, a square while waiting, a
  dot while unread; a new chat shows a plus. The first `tabs.Looks` entry whose
  `Applies` holds marks a kind of conversation, such as a design: its icon, named for
  assistive technology, shows in place of the plus and while the conversation is
  idle, and its `Draft` title in place of "New chat" for a new chat. The tab's own
  title, as `tabs.Service` reports it, is unchanged.
- Each tab keeps the text typed and not sent in it. On `chat.Leaving` the text is
  kept under the tab the chat leaves (a new chat's is the last draft tab shown), and
  `chat.Controller.SetDraft` gives it back when that tab is shown again. It is kept
  for the run only; files attached and mentions are dropped, as on any switch.
- Closing a tab deletes nothing. Closing the active tab shows its right neighbour on
  the strip, else its left one; closing the space's last conversation or draft opens a
  new chat in its folder, so the space stays. A tab whose conversation the record stops
  listing closes. Show Next Tab and Show Previous Tab move within the space.
- The open tabs and the active one are saved (`open`, `active`) and reopened once the
  workspace record is read, without the conversations it no longer lists and pages no
  plugin contributes. A conversation the chat already shows by then becomes the active
  tab instead.
- After `kernel.PointChanged` for `shell.Pages`, a page tab whose page is gone closes,
  and the others take their page's current title.
- The strip draws each tab as a 28 px pill centered in the strip, as wide as its
  marker and title (80–220 px): the active one filled with `ActiveTab` and a faint
  shadow, the others only while hovered. The close button sits over a tab's end and
  shows on the active tab, on hover and on focus; only the active tab keeps room for it.
  Then a "+" that opens a new chat in the space, then each `tabs.EndActions` entry
  (All chats' search), whose `Run` is posted after the click, and Settings (shows
  `settings.PageID` when contributed).
- The strip fits its tabs to the width their row had in the previous frame (the first
  frame draws everything and asks for another). Tabs shrink to 80 px; past that, tabs
  move, from the end, into a "More tabs" menu, never the active one.
- A tab's context menu has Close Tab, and for a conversation Rename…, Remove from List
  and Delete…. Rename and delete go to the backend, then the record; Remove from List
  only forgets the record. Either closes the tab. A failed rename or delete is
  reported and changes nothing. Menu choices act after the frame.
- Commands: New Chat (⌘N, File; the same as "+"), Close Tab (⌘W, File), Show Next Tab
  (⌃Tab) and Show Previous Tab (⌃⇧Tab, View).
- A Tabs settings section: Reopen tabs when Wada starts (`reopen`; off, the saved tabs
  are ignored at launch), and a row of the five project colors for each folder, which
  sets `colors`.

## Settings

The kit's settings page shows as a tab. The tabs, chat, PiG, models and
notifications plugins contribute their own sections.

## Model picker

`internal/plugins/models` requires `agent.ModelsKey` (so it is Blocked for a backend
without a catalog). It adds the model and its reasoning effort (when the model has
variants) as one group to `chat.ComposerPrimary`. A catalog with exactly one model
chooses it for new conversations, so the composer names the model instead of
"Default model". The group stays visible together
in both new and existing conversations. It reads the catalog when a new connection
attempt is ready and again on `agent.ModelsChanged`, which the backend emits for its
own provider changes and for the server's catalog notices. A new conversation starts
with the chosen model, saved in settings and set as `Draft.Model` through
`chat.Outgoing` while the backend still offers it.
Its Models settings section uses a compact checklist with collapsible provider groups
and Enable all / Disable all actions for the models matching the search. Model IDs
appear in tooltips and remain searchable. Search reveals matching models even inside
collapsed groups; clearing it restores the collapsed state. A model turned off leaves the
model picker; the setting (`hidden-models`) keeps the `provider/id` of each model turned
off, so a model the backend adds later is listed until it is turned off. Turning off the
model in use does not change it.
In an open conversation, choosing a model calls `SetModel` and shows the choice until the
backend's `ModelChanged` confirms it; a failure is reported and the choice reverts.
Switch work and the pending choice belong to that conversation; navigating away
cancels the work and drops its completion.
`chat.Conversation.Model` carries the open conversation's model. The model picker
has a subtle filled background; effort uses a quieter text control.

## Mode picker

`internal/plugins/modes` requires `agent.ServiceKey` and `chat.ControllerKey`, so it
works with a backend that has no model catalog. It reads the backend's optional
`agent.ModesKey` when a new connection attempt is ready, and again when a provider of
it comes or goes. Its picker goes in `chat.ComposerAccessory`, as a quiet text control,
when there is more than one choice across backend modes and workflows; a backend without
modes shows "Chat" when workflows exist. A choice applies once the frame is done.
The mode choice is saved in settings and applied to every message through
`chat.Outgoing`. With no valid saved choice, the first offered mode is used; a backend
without modes keeps its own behavior. Chat passes `Draft.Mode` to `Send.Mode`; the
backend selects it before sending the prompt.
The picker lists `chat.Workflows` after the access modes. Choosing a workflow
stamps the next outgoing draft, preserving its access mode. The choice clears only
when outgoing preparation and delivery validation succeed and the pipeline commits.
Navigation or choosing a later access mode clears an unsent workflow selection.
`chat.WorkflowSelected` lets an Agent-menu command select a workflow and focus the
current composer without starting it or opening another page.

## Usage

`internal/plugins/usage` requires `agent.ServiceKey` and `chat.ControllerKey`. What
fills the context is the latest assistant message that reported tokens: its
`Tokens.Context` over its model's `Model.Context`. It contributes two `chat.Work`
sections:

- **Context**: used over the window, a strip with each kind of token in its own color
  and the free space, and a line for each kind in use (input, cache read, cache write,
  output, reasoning) and the free space with its share of the window, or of what is
  used when the window is unknown;
- **Cost**: the model (with its variant), the number of replies, the total cost and
  the latest reply's cost ("Free" on a free model).

While the work panel is hidden it adds a button to `chat.ComposerTrailing` instead:
the total cost, the share of the context window in use and a ring that fills with it
(the accent, a warning from 80%, danger from 95%), opening a popover with both
sections.

Backends report usage only with stored messages, so the plugin reads the conversation's
`Messages` when it opens, again when one of its turns ends, and again when a new
connection attempt becomes ready (a read before it failed). A read belongs to the
conversation it was for: a later navigation drops it, and a failed read shows nothing,
since the chat page reports the failure. A new chat, or a conversation with no usage,
shows nothing. The model catalog is read once, for names and context windows.

## File changes

`internal/plugins/filechanges` requires `agent.ServiceKey` and `chat.ControllerKey`.
It contributes **File changes** to `chat.Work` after Context and before Cost. A
dropdown chooses the source, starting with **Session changes**; the choice stays
while navigating conversations but is not saved across launches:

- **Session changes** reads the backend's optional `agent.ChangesKey`, looked up
  at each read and read again when a provider comes or goes. Without that
  capability, the section says session changes are unavailable.
- **Uncommitted Git changes** reads the local Git repository containing the
  conversation's absolute folder. It includes staged, unstaged and untracked
  files, excluding ignored files. Paths are relative to the repository root,
  including changes outside a conversation's subfolder. It uses local Git even
  with a remote backend; an absent folder, Git executable or repository is
  reported. It does not assume the app's working directory when no folder is set.

The section shows the number of files and total lines added and removed in a
disclosure that collapses the file list while keeping the summary and source
selector visible. The list starts collapsed; its expanded or collapsed state stays
while navigating conversations.
Expand-all and collapse-all controls open or close every current file's diff.
Files are sorted by folder, then path, with root files first; each folder heading
appears once above compact rows with filenames, A/M/D status markers and line
counts. Full paths and status names are available in tooltips and accessibility
labels. Selecting a file expands its unified diff: hunk headers muted,
added and removed lines on `DiffAddBg` and `DiffDelBg`, up to 400 lines. A file
without a patch says "No diff to show".

Git keeps staged and unstaged patches separately labeled within each file; line
counts add both, so opposing staged and unstaged edits remain visible. A rename
shows as a deletion and an addition; binary files report zero line counts. Unmerged
files show their working copy against Git's "ours" stage. Git reads run off the UI
thread with a 30-second timeout, literal path arguments, optional index writes
disabled, and external diff, textconv and filesystem-monitor hooks disabled. Patch
previews are capped at 256 KiB per command and 4 MiB per repository; counts remain
complete when a preview is omitted. An oversized status listing is reported.

The selected source is read when a conversation opens or its folder changes,
after each of its turns ends, when a new connection attempt becomes ready and on
**Refresh file changes**. Refresh also picks up edits made outside Wada. A first
read shows a loading state; an empty result says "No file changes" or "No
uncommitted changes". A failed refresh reports the error and keeps the last known
files visible. Switching sources or conversations cancels the old read and clears
the files and expanded diffs. A new chat shows no section.

## Attachments

`internal/plugins/attachments` adds a paperclip to `chat.ComposerAccessory`, an
"Attach Files…" command (⇧⌘A, Chat menu), a `chat.FileDrop` handler and a
`chat.Paste` handler: pasted files are attached, and so is a pasted image unless text
came with it (`shell.Host.Clipboard` reads both). Files the backend's
`agent.FileTypesKey` refuses (PiG: supported images and text only) are reported when added. Files are
read off the UI thread, typed by extension and contents (text stays `text/*`
whatever the extension), and shown as removable chips with image thumbnails in
`chat.ComposerAttachments`. A message takes at most 10 files, 10 MB each and 20 MB
in all; a folder, an unreadable file or one over a limit is reported in a toast. Its
`chat.Outgoing` transform runs at order 1000, after the design's, so it numbers
images among those already in the draft: it refuses a send while files are still
being read, then adds the files to `Draft.Files`. They leave the composer when the
message is sent.

## File mentions

`internal/plugins/mentions` requires `agent.FilesKey`, so it is Blocked for a backend
that can't find and read files. It contributes a `chat.Completions` entry for
"@": typing it asks the backend's `Find` for files and folders in the open
conversation's folder (the backend's default for a new chat) and lists up to eight,
best first as the backend ranks them. A file is shown by name with its folder beside
it; a folder by name with a trailing slash and a folder icon. Choosing a file writes
`@<path> ` into the message and starts reading it through the backend's `Read`, off
the UI thread; choosing a folder writes `@<path>/ ` and reads nothing, leaving the
folder for the agent to explore. Its `chat.Outgoing` transform runs after the
attachments' (order 1001): every file whose `@<path>` the text still names, as a word
of its own, is added to `Draft.Files` under its path, in the order the text names
them; the text itself is unchanged. A file still being read holds the message back.
A file that can't go (over 1 MB, neither text nor an image, refused by
`agent.FileTypesKey`, unreadable), or an eleventh in one message, is reported in a
toast; its path stays in the text, and the agent may still open it. Mentions belong
to the draft: they are dropped when the message goes out or another conversation
opens. A path typed by hand without choosing it is plain text.

## Skills

`internal/plugins/skills` requires `agent.SkillsKey`, so it is Blocked for a backend
that has no skills. It contributes a `chat.Completions` entry for "/": typing it
lists the skills the backend reports for the open conversation's folder (the
backend's default for a new chat), sorted by name and filtered by what follows the
slash. Each row shows the skill's name and description. Choosing one replaces the
typed word with `/<name> `; the app never reads the skill itself. Its
`chat.Outgoing` transform runs after the attachments' and mentions' (order 1002):
every listed skill whose `/<name>` the text still names, as a word of its own, is
added to `Draft.Skills` in the order the text names them, and the chat sends them
with the prompt's skills. The text is unchanged, so the user still sees the name. A
skill removed from the text is not attached. The list is read once per word and
again when the conversation or its folder changes; a failed read is reported once
per word and leaves the text alone. Retry sends the words again without the
pipeline, so it attaches no skill; Edit runs the pipeline and can attach one again.
Its completion's hint offers "/" in the empty composer while the plugin is active.

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

The **Workflow engine** plugin provides `chat.DeliveryKey`; **Built-in workflows**
contributes `workflow.Definitions`, composer choices and Agent-menu commands.
`workflow.Flow` contains phase instructions and the review limit. The engine requires
backend child sessions and task snapshots; it owns transitions and knows no built-in
flow's prompt text. Plugins communicate through contracts and kernel registrations.

**Plan, code, review** is a one-task selection. The ordinary conversation is the
main agent throughout planning, work, fixes, and final reporting. Planning
is read-only and proceeds to work automatically unless clarification is
needed. Each independent review creates a fresh child under that main session,
inheriting the task's model and effort. Reviewers receive requirements, reference
attachments, plan, deliverable/check evidence, earlier findings and task-start
changes, and inspect the deliverable and its sources. They never inherit the main history.

A task snapshot contains project files before work begins, including pre-existing
edits and untracked, non-ignored files. Comparisons against that snapshot identify
additions, modifications and deletions. Snapshot limits or read failures fail the
phase explicitly; they are not treated as an empty diff. This attribution does not
isolate concurrent external edits.

Each phase requests a structured result through the backend. The model calls the
host's `submit_result` tool; a model that answers in text may end with a fenced JSON
block, which the backend validates and accepts instead. Planning, work and
fixes return ready or blocked with a summary; routing returns answer, continue,
new_task or resume; reviewers return pass, issues or incomplete with file, severity
and evidence for each finding. Missing, invalid or contradictory results fail the
phase. Findings go to the main agent to substantiate, fix or reject with evidence.
At most three reviews run in one cycle. The final report distinguishes **Passed**,
**Issues remain** and **Review incomplete** from execution failure.

Tab activity spans the whole task, including child review. Completion notices wait
until delivery is idle, while reviewer questions and approvals notify under the parent.

The composer remains usable during execution. Prepared inputs queue in order, with
files and skills retained. The current main/reviewer turn finishes before queued
messages are routed, and the queue takes precedence over the next automatic phase.
Questions are answered normally before execution continues. Related changes update
the requirements and start another implementation/review cycle with a fresh budget.
Unrelated work pauses the old task and clears its automatic review association;
that message is handled as ordinary chat. Resume explicitly restores the paused task.

Tasks appear as transcript activities anchored to their initiating message. A task
card shows the files its review's task-start diff found, each with **Open**, and a
**Sources** row naming the work folder and any reference attachments with **Reveal**;
the reviewer's summary stays above the card. Details expand the request, plan, work
summary, phase results, review findings, check evidence and finding dispositions. The
card is the deliverable, so the work panel is not needed to find it.
Approvals and questions from a reviewer appear in the main chat and replies target
the real child; they are not dismissed. Reviewers have no top-level tabs or chats.
The queue shows removable pending inputs. Stop aborts the active session and cancels
continuation, preserving pending inputs. Retry and Resume are explicit actions;
create, send and reply mutations are never automatically repeated. Request IDs
prevent an old turn's events from advancing a retried phase.

The engine saves origin-scoped task state, queued payloads, reports, snapshots and
prepared requests in `workflow.db` (pure-Go SQLite with WAL and numbered migrations)
under the shell data directory. An unrecognized database or future unsupported schema
is refused. Unfinished execution after restart is marked
interrupted and requires explicit resume. Navigation does not cancel tasks; deletion
and plugin disposal cancel their lifetimes and abort an active child. Store workers
serialize snapshots off the UI thread and flush pending writes during shutdown.

## Side chats

`internal/plugins/explore` requires the agent service and children capability, the
chat controller, workspace record and shell host. It contributes **Start a side
chat** to `chat.SelectionActions`, a drawer to `chat.Drawers` and **Show Side Chat**
(View, and the palette).

**Start a side chat** opens the drawer beside the conversation the text was selected
in: 420 px at the page's right edge, after the work panel, with the main
conversation still on show and usable to its left. Its header says **Side chat**,
beside which conversation and that it is read-only, with **Close side chat** (×).
Under it are the side chat's messages, or **No messages yet**, and its own composer,
focused and holding the selection as a Markdown quote followed by a blank line for
the question. A new chat with no conversation yet can't start one; a toast says so.
Nothing reaches the backend until the user sends.

The first message creates a child of the conversation in the same folder and model,
named after the user's words below the quote. Its immutable tool policy allows
reads and searches and denies edits, shell, web and unknown tools, whatever the
ordinary chat mode. The child starts without the main conversation's history, so
its first message goes with a short preamble saying it is a read-only side chat
that may quote another conversation; the drawer never shows it, also when it reads
the messages back. The child is not announced on `chat.Started`, so it has no tab,
is not in All chats and is not notified. Later messages go to the same child.
Creation belongs to the plugin, so closing the drawer or starting another side chat
loses neither a created child nor its first message; a child returned with an error
is remembered and the error noted in the drawer. A side chat that leaves the drawer
is kept while the app runs once it has a child or a message on its way, so its
sends finish and report as usual. Neither creation nor a send is retried; a send the
backend refused says it was not sent, any other failure that it may not have
reached the agent.

The plugin follows the events of every side chat it keeps, also while the drawer
is closed or shows another conversation's side chat: words as they arrive, a faint
line for each read or search, a note for a failed turn, Stop while a turn runs. Each finished answer
offers **Add to main chat** while no turn runs. It appends `From a side chat:` and
the answer to the main conversation's unsent text after a blank line and focuses
the composer. It never sends it.

The drawer shows only beside its own conversation under the backend origin it was
made in: opening another conversation hides it, coming back shows it again. One
side chat is in the drawer at a time; starting another replaces it. **Show Side
Chat** reopens the one beside the open conversation, or else that conversation's
newest side chat, as kept or read from the backend, and focuses its composer.

Links from conversations to their side chats, scoped by origin, live in the
plugin's `side-chats-v1` setting, each with whether the drawer was showing it.
Opening a conversation, or the backend becoming ready, brings back the drawer with
the conversation's newest side chat when it was showing, also after a restart,
without taking the focus; closing it with × records that it was closed. A link
whose conversation the current origin's workspace record no longer has is dropped
when the record changes. A change of origin closes the drawer and lets go of the
side chats kept under the old one.

## Screenshot markup

`internal/contract/attachments` declares the pending-image service, image actions
and change topic. The attachments plugin provides it: snapshots clone image bytes,
and each accepted file gets a unique identity and revision. `Attach` adds an image a
plugin made, with its preview, under the checks a dropped file gets, and returns its
identity. `Replace` puts an image
file, its notes and its details in place of a pending attachment and its details.
A detail is part of the image at full resolution with a name saying what it
shows. `Replace` checks the identity and revision, that the file and its details
are images of types the backend takes, and the 10 MB per-file, 20 MB and ten
files per-message limits, details counted, updates the thumbnail, and emits
`attachments.Changed`. Source files are never written. Removed, sent or replaced
images cannot accept a stale edit. Image decoding is bounded to 16 megapixels and
16,384 pixels per side before thumbnails are allocated. An image with notes says
"with notes" on its chip, and one with details how many. When the message is sent,
each image's notes are added to its words after a blank line, under `[Image N:
name]`, where N is the image's place among the message's images as the agent
receives them. Its details follow it, each its own image under `[Image N: name,
from image M]`. Removing the image removes its notes and details.

`internal/plugins/markup` requires `shell.Host`, that service and the chat
controller. It contributes **Mark up** beside each image chip and a modal editor
that takes the window less a margin: a toolbar of icon buttons, the canvas, and a
column of notes.

On macOS it also contributes **Capture Screenshot…** (Chat menu, ⇧⌘2, and ⌃⇧⌘2
from any app as the command's `Global` keys) and a screen button beside the
composer's paperclip. Started from another app, the capture covers what is on
screen there, and Wada comes to the front with the editor; a cancelled one brings
Wada to the front too. The command closes an open editor,
hides the window through `shell.Host.SetHidden`, waits 250 ms for it to leave the
screen, and runs `screencapture -i -o -x` on a worker: the user drags a region, or
presses Space and clicks a window, and Escape cancels. The window comes back
whatever happens, also when the plugin goes away mid-capture. The screenshot is
attached with `Attach` as `Screenshot.png`, then `Screenshot 2.png` and so on, and
opens in the editor. A cancelled capture attaches nothing; a failed one says so in
a toast, preserving the command's diagnostic even when it creates no image.
A quiet exit with no file and status 0 or 1 is cancellation. Capturing other apps' windows needs macOS's Screen Recording permission for
Wada, which macOS asks for on the first capture; without it the capture shows only
the desktop and Wada's own windows. Elsewhere there is no capture command.

- **Tools.** Box (B), Arrow (A), Highlight (H) and Hide (R) draw by dragging;
  Pin (P) places a point with a click, where it is let go; Draw (D) draws a
  freehand line, a point for every 2 DIPs the pointer moves, up to 2,000.
  Choosing a drawing tool lets go of the selection.
- **Select.** Select (V) takes a mark by its badge, a box's edge, an arrow's or a
  freehand line, or anywhere on a highlight or a hidden area, and moves it. The
  selected box, highlight or hidden area resizes by its corners and an arrow by
  its ends; a selected pin or line, which only move, shows a dashed frame. A
  press inside a box falls through to marks within it.
- **Numbers.** Every mark but a hidden area is numbered by its order among the
  numbered marks. There are at most 99 marks. Positions are fractions of the
  image, so they don't depend on the zoom.
- **Inks.** Five inks from `native.Palette.Inks` (Red, the default, Orange,
  Yellow, Green and Blue, keys 1–5) color new marks and recolor the selected one.
  Hidden areas are `native.Palette.Redaction`, an opaque near-black. Both are the
  same in both themes.
- **Drawing.** Line widths and badge sizes are in image pixels, so they look the
  same at every zoom and in the export. Hidden areas are painted first, then
  highlights in their ink's translucent wash, then lines, each with a thin dark
  halo, and last the numbered badges: on an area's top-left corner, at a line's
  start, just behind an arrow's tail, or, larger, as a pin itself. Hidden areas
  and highlights cover whole image pixels, so a hidden area's edge is opaque in
  the export.
- **Notes.** The column has an optional note about the whole screenshot, then a row
  per mark: its badge, a note field and a remove button; a hidden area's row says
  it is blacked out before sending and has no note. Drawing a numbered mark
  selects it and focuses its note. A focused note shows its mark selected, and a
  double click on a mark with Select focuses its note.
- **Editing.** Undo and Redo (⌘Z, ⇧⌘Z) cover adding, moving, resizing,
  recoloring and removing marks, up to 100 steps; notes keep their text through
  them. Backspace or Delete removes the selected mark; the arrow keys nudge it a
  pixel, or ten with Shift. These keys work while the canvas has focus.
- **Size sent.** With `agent.ImagesKey`, the editor asks the backend, on the
  worker that decodes the image, what size it sends the image at to the open
  conversation's model, or the default model for a new chat. The request includes
  the original image bytes: PiG runs its prompt-image resizer with the model's
  dimension, encoded byte and JPEG quality limits, including additional
  downscaling needed to fit the bytes. The header shows the resulting size,
  or "sent at full size". A new chat's model chosen in the composer is not known
  to the editor, which asks for the default model then. When the image is sent
  smaller, the eye button (M) shows the canvas at that size, averaged down, and
  zooms in until one pixel sent takes one DIP if it was shown smaller.
- **Details.** A box, highlight or pin can send its part of the image again at
  full resolution: an area with a margin of 15% of its longer side, at least 24
  pixels, or a square around a pin of a tenth of the image's shorter side each
  way, at least 160 pixels. A new one does by default when the image is sent
  smaller and the part would be under 480 pixels on its longer side as sent. The
  magnifier beside its note turns it on or off, with undo; an image takes at most
  four. Details are cut from the exported image, so they show their marks and
  their hidden areas.
- **Zoom.** The image fits the canvas, at most at one pixel per DIP, and nothing of
  it paints past the canvas's edge. ⌘-scroll
  zooms around the pointer, up to 800%; −, + (by 1.25), Fit and 1:1 (actual size)
  are in the toolbar. Scrolling, dragging with the middle button or Option, or dragging an
  empty spot with Select, pans the zoomed image.

**Use marked-up image** exports on a worker. MyGo's software renderer
(`ui.Render`) paints the marks with the same painter as the canvas onto a
clear layer the size of the image, which is laid over the original pixels.
Pixels outside the marks stay exactly as they were. Encoding enforces the PNG byte
limit. The result replaces the pending image as `<name>.png` with a legend as its
notes and its details as `<name> mark N.png`, named `mark N at full resolution`. The legend is the screenshot's note, then a line saying the user drew the
numbered marks and that positions run 0–1000 across and down, then one line per
numbered mark, for example `1. Red box from (250, 250) to (750, 750): Move this
panel`, `2. Red pin at (200, 800)`, `3. Orange freehand line spanning (350, 300)
to (470, 367)` or `4. Yellow highlight from (50, 850) to (450, 950)`. When parts
are hidden, a last line says solid dark blocks hide parts of the image on
purpose, without saying where. The hidden pixels are not in the image sent; the
kept original, which stays on this machine, still has them.
Failures keep the attachment as it was and show an error in the editor. The user
still sends explicitly.

The plugin keeps each replaced image's original, marks and notes, by image identity
and revision, until the image is sent, removed or changed elsewhere. **Mark up** on
it opens the original with the marks still editable. Applying it with no marks and
no note puts the original file back without notes (**Use original image**). GIF
images use their first frame.

Opening and exporting use workers tied to a child scope. Switching conversations
or projects, or removing, sending or replacing the pending image, ends that edit.
Cancel, Escape and the backdrop leave the attachment unchanged, and the decoded
image is released once the dialog closes.
