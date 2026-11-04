# Video editor

`internal/plugins/video` is a video editor beside the conversation for
[HyperFrames](https://github.com/heygen-com/hyperframes) projects: HTML and CSS
compositions rendered to MP4. It reads and writes the project's own files, shows
the frame at the playhead, draws a timeline from the timing attributes, reports
what breaks a render and runs the HyperFrames CLI to render.

The editor is a port of the HyperFrames authoring contract, not of its renderer:
the composition format, the timing rules, the check that guards a render and the
seek that shows a frame. Rendering stays with the official CLI, which owns the
headless browser and FFmpeg.

## Where a video lives

A video belongs to a conversation, like a design: the plugin keeps
`{Origin, Session, Dir}` in its `videos-v1` setting, scoped by backend origin, so
a conversation shows its editor again when it is opened, also after a restart. A
conversation the workspace record no longer has is forgotten when the record
changes.

A project is a folder with `index.html` and the compositions it mounts:

- `index.html` is the root composition: one element with `data-composition-id`,
  `data-width`, `data-height`, `data-fps` and, normally, `data-duration`, the
  film's length.
- `compositions/*.html` are scenes. A scene keeps its markup inside `<template>`
  and its own `data-composition-id`; the root mounts it with a host that has
  `data-composition-src`, `data-start` and `data-duration`.
- `assets/` holds media and fonts; media paths are relative to the project.

**New Video** (File, ⇧⌘V), and the title strip's button, take the open
conversation's folder, or else the folder whose tabs are on show, or else one
the user picks. A starter chooser offers the installed collections' templates
and **Start blank**. A worker makes the first `videos/video-N` not there yet,
instantiates the starter or a blank scene, and opens a new chat in the
conversation's folder beside it. The first message records the video; a new video left before
its first message is a plain new chat. The conversation's chat history stays in
the transcript; the video is files in the folder.

Every outgoing video message carries internal authoring instructions and a
serializable `agent.ProjectScope` bound to the selected video directory. This
includes the first message, existing conversations and project switches. These
instructions stay separate from user attachments; no synthetic brief file is sent. The instructions keep the task on the user's requested content and
leave editor debugging, app internals, conversation history and other projects
outside the task.

The video plugin contributes a worker-safe handler to `agent.ProjectTools`. PiG
exposes the `project` tool during a scoped turn, with these actions:

| Action | Behavior |
| --- | --- |
| `inspect` | Current scenes, overrides, component fields, compatible layouts and themes; implementation markup, motion and runtime assets are omitted. For HTML projects, returns the root and scene file paths. |
| `update` | Updates `title`, `theme`, `scenes` and/or `overrides` in a component document. Preserves definitions, validates before saving, regenerates HTML and returns the quick check. |
| `read` / `write` | Source files inside the selected HTML project. Writes to component projects are refused; they use `update`. |
| `check` | Synchronizes generated output and returns the quick check, duration and scene count. |

Source paths must be local, contain no hidden or traversal segments, and use
HTML, CSS, JavaScript, JSON or SVG extensions. Existing symlinks and ancestors
must resolve inside the selected video; reads and writes are limited to 2 MiB.
The model cannot supply or switch the project directory. PiG hides general tools
and blocks their invocation for the turn, including shell and filesystem searches
even in Full Access mode. Its normal tools return when the turn settles.
Preview refresh and rendering remain app actions; the tool does not install or
launch a second editor or render CLI.

A conversation opens the editor for the project recorded for it. The project
menu in the header lists the folder's other projects (the folder itself when its
`index.html` is a composition, and each folder under `videos/`) and switches to
one; the choice is remembered for that conversation.

## Reusable components

`internal/contract/videokit` declares the collection contribution point and the
structured document. `internal/plugins/videocomponents` contributes three
collections: **Video foundations** (theme, visual primitives, title/takeaway and
layouts), **Excel essentials** (spreadsheet, keyboard shortcut, formula/result
and two starters), and **Excel actions & animation** (ten action demos and two
more starters). The editor imports only the contract. More collection plugins
contribute definitions through `videokit.Collections`; they do not import the
editor or another collection plugin.

The layers are theme tokens → shared CSS primitives → components → scene layouts
→ starter sequences. A definition has an immutable `{id, version}` reference.
Collections share a definition namespace, so an Excel starter can use the
foundation title and layouts. Exact duplicate definitions, conflicting assets
and incompatible animation runtimes are refused.

A component declares its label, description, group, typed fields with defaults
and limits, markup, scoped CSS and animation. Optional `layouts` references
restrict where it fits; `defaultDuration` sets its suggested scene length.
Numeric fields can declare `step`; a fractional range index is rejected rather
than silently selecting no cells. Markup is a Go text template with
`value` (escaped scalar), `binding` (instance-specific variable ID), `split`
(bounded escaped list), `grid` (bounded escaped rows matching headers),
`selected` (one-based numeric selection), `within` (a bounded inclusive range),
`number` (a validated numeric field), `series` (escaped labels paired with bounded
numeric values) and `inc` (one-based row labels) helpers. These are generic content
primitives; the compiler has no Excel component IDs. Styles are flat rules,
scoped to the scene or component; nested rules and `@` directives are refused.
Motion uses a shared paused GSAP timeline, the component's `box`, its scene's
`root`, and named second offsets: `cues.enter`, `action`, `reveal`, `exit`.
Action and reveal fall at 18% and 58% of the scene's duration. Components author
absolute seek-safe tweens, without a private clock.

Each starter creates **video.json**, schema 1, with the title, theme reference,
scenes, user overrides and a project snapshot. Scenes record IDs, titles, layout
references, start/duration/track and component instances with authored inputs.
Omitted inputs use component defaults. Overrides use `scene--part--field` keys
and win over authored inputs. Native field controls and the agent edit this same
document; overrides survive reopening without relying on app settings.

The snapshot includes component definitions, layouts, themes, shared styles and
UTF-8 assets. The foundation bundles GSAP 3.15.0 and its notice, copied into the
project's assets; these starters need no remote images, fonts, scripts or AI
request to preview. Collection updates or disabling the collection plugin do not
change an existing project's definitions. There is no automatic component
upgrade. The runtime's original license header remains intact and its notice
is included in the application's third-party inventory.

Opening or reloading a component project validates its document and compiles
`index.html`, `compositions/<scene-id>.html` and assets. Files whose bytes match
are not rewritten. Invalid references, values, IDs, layout capacity or content
limits stop compilation before overwriting generated files. A save validates
before atomically replacing the source; derived files are replaced individually
and can be recovered from the source on the next load. Generated HTML edits are
replaced on reload. Blank projects keep the direct HTML workflow.

The starters are **One Excel tip · AutoSum** (20 seconds), **Excel tips roundup**
(24 seconds), **Four daily Excel actions** (42 seconds), and **Lookup, highlight
and chart** (32 seconds). They use the same components and the centered or
side-by-side layout at 1920×1080, 30 fps. Components expose editable titles,
explanations, platform labels, keycaps, table headers/rows/highlight, formulas
and expected results. They illustrate results; they do not evaluate formulas.
Tables support up to five columns and six rows, with `|` between cells and `;`
between rows; shortcuts support up to four keycaps.

**Add scene** groups the project's snapshotted components by purpose.
**Add from installed library** offers definitions not yet in that snapshot.
Selecting one imports that exact reference, its required missing layouts and
nonconflicting assets, then appends a ready-to-use scene. Existing references,
shared CSS, theme and scenes are retained. A conflicting runtime or asset is
refused. Importable components carry their own scoped styles rather than
requiring a change to the saved foundation.

The collection contains the following components:

| Group | Components | Animation |
| --- | --- | --- |
| Essentials | Title/takeaway, spreadsheet, shortcut, formula/result | Entrances, keypress, formula wipe, result reveal |
| Data entry | Range selection, AutoFill | Cursor/click, range highlight, fill-handle drag |
| Clean & organize | Flash Fill, filter, sort, duplicates, Text to Columns | Before/after reveal, filter dropdown, row reorder, duplicate fade |
| Formulas | Lookup trace | Matching cells highlighted in order, result reveal |
| Visualize | Conditional formatting, column chart | Data bars, threshold emphasis, staggered chart growth |

Action/result components offer `wipe`, `slide` and `zoom` transitions as editable
fields. A shared action/result builder keeps their content schema and motion
consistent. Each of the ten detailed demos uses a centered full-width layout
and an eight-second default, so adding one cannot squeeze its tables into half
a frame. Before/after tables allow up to three columns and five rows; selection
allows four columns and six rows. Numeric series pair up to five labels with
finite percentages from 0–100; mismatched lists, invalid ranges and invalid
animation choices are refused. These are authored illustrations, not an Excel
calculation engine. Formula and before/after results must be supplied correctly.

Clicking a scene opens its fields in **Vars**; Enter or **Save** applies text, while **Reset**
removes the override. A scene's context menu duplicates, moves, removes, selects
a layout, adds a component or swaps two components. Adding a second component
chooses a layout with room; a full layout refuses another component. Duplicate,
move and remove actions arrange the sequence contiguously and clear timing
undo/redo. Duplication copies the scene's user overrides under its new ID.
Structural actions and field saves serialize with timeline writes. Scene timing
and lane drags update the JSON document and regenerate HTML; timing undo/redo
uses the same document. Component motion is edited in its definition, not by
rewriting generated clip text. A component scene with no editable timeline clips
points to **Vars**, rather than asking the agent to write clips into HTML.

A component project requires 1–100 scenes, valid unique instance IDs,
nonnegative start/track, and scene durations of 3–120 seconds. A schema other than
1 is refused. The supplied examples identify Excel for Windows; authors must
verify formulas, expected results and platform-specific shortcuts.

## The composition model

`parseComposition` reads a file with the package's own tolerant HTML scanner
(`html.go`), which keeps the byte span of every attribute value and of every
element. The spans are what lets an edit rewrite one value and leave every other
byte as the agent wrote it.

A composition's root is the outermost element with `data-composition-id`. Its
clips are the elements under it with `data-start`, `data-duration`,
`data-end` or `data-composition-src`, outside a nested composition, which keeps
its own local time. Reading follows HyperFrames' rules:

- `data-start` is seconds, a clip id, or a clip id with an offset. It resolves to
  the end of the clip it names, inside the same composition. The grammar is the
  platform's: a whole string of letters, digits, `_ . : -` is one id (so
  `scene-01` is an id, not `scene` minus 1), and an offset needs whitespace
  before its sign (`intro + 0.5`, `intro - 2`). A cycle, or a reference to a clip
  that is not there, stays unresolved and the check reports it. Negative starts
  clamp to 0.
- `data-duration` is the slot length; `data-end` is the legacy way to write
  where a clip stops and becomes a duration. A media clip without either has a
  length only its file gives: it is marked unknown, never guessed.
- `data-track-index` is the lane the timeline draws; `data-track-kind` names
  the lane (`video`, `audio`, `captions`, `graphics`), else the tag decides.
- A scene host's `data-playback-start` picks the moment of the scene shown
  first, and `data-playback-rate` scales it; both default to 0 and 1.

`Project.Duration` is the root's declared `data-duration`, else the end of the
last clip. `Project.Size` is the root's declared size, else 1920×1080. The scene
files are read when the project opens, so a missing scene still opens and the
check reports it.

## The check

`checkProject` runs the HyperFrames linter rules a timeline and a render depend
on, over the files as written. Findings carry the linter's code, severity, a
message, a fix and a line.

| Code | Severity | What it catches |
| --- | --- | --- |
| `root_missing_composition_id` | error | no element declares the composition |
| `root_missing_dimensions` | error | the root has no `data-width`/`data-height` |
| `root_composition_missing_duration_source` | error | no `data-duration` and no registered timeline |
| `root_composition_duration_derived` | warning | no `data-duration`: the length comes from the clips |
| `missing_timeline_registry` | error | the root registers no `window.__timelines` |
| `gsap_timeline_not_registered` | error | a scene builds a timeline but never registers it, so nothing in it animates |
| `timeline_element_missing_timing` | error | a timed row with no duration where the timeline can draw its end |
| `timed_element_missing_clip_class` | warning | timing attributes without `class="clip"` |
| `host_missing_composition_id` | error | a scene host without `data-composition-id` |
| `timeline_id_mismatch` | error | the host and the scene file name different ids |
| `missing_composition_file` | error | the file a host mounts has no composition |
| `unresolved_start_reference` | error | a start naming a clip this composition does not have |
| `invalid_data_start`, `invalid_data_duration` | error | values that are not seconds or references |
| `media_missing_id` | error | timed media the renderer cannot find |
| `media_missing_src`, `media_missing_data_start` | error | media the renderer cannot load or time |
| `video_missing_muted` | error | a timed video without `muted` or `data-has-audio` |
| `video_audio_double_source` | warning | `muted` and `data-has-audio="true"` together |
| `media_crossorigin_breaks_preview` | error | `crossorigin`, which the preview cannot always complete |
| `self_closing_media_tag` | error | `<video/>`, which leaves the tag open and swallows markup |
| `unbalanced_style_tags` | error | a `<style>` block never closed, or closed twice |
| `non_deterministic_code` | error | `Date.now`, `performance.now`, `new Date`, unseeded randomness, timers |
| `requestanimationframe_in_composition` | error | wall-clock animation the timeline does not drive |
| `gsap_infinite_repeat` | error | `repeat: -1` without a finite length; warning when the root bounds it |
| `imperative_media_control` | error | script that plays, pauses or seeks media |
| `negative_z_index` | warning | a negative level can hide an element behind its parent |
| `root_path_missing_in_preview` | warning | a path from the server root, which the project does not serve |
| `invalid_parent_traversal_in_asset_path` | error | a `../` path that leaves the project |
| `invalid_composition_variables_declaration` | error | the declarations are not JSON, or an entry has no id, type, label or matching default |
| `unloadable_media_variable_default` | error | a media variable defaults to a URL the runtime refuses |
| `unknown_variable_binding` | warning | `data-var-text`/`data-var-src` names a variable this file does not declare |
| `caption_track_kind_missing` | error | a legacy caption layer (`id="captions"`, `caption-*` class) that does not say `data-track-kind="captions"` |
| `multiple_caption_tracks` | error | captions spread across more than one timeline track |

**Full check** runs the official CLI over the project and shows everything it
found: its lint, runtime, layout, motion, contrast, craft and audio checks,
which open a browser and are the reference. The command is
`npx --yes hyperframes check --json`, run in the project folder; its findings
keep their code, severity, message and fix, their file is made relative to the
project, and a `#id` selector names the clip when the editor knows it. They are
marked **CLI** in the panel. **Quick check** puts the editor's own check back,
and opening the project again does too. The full check needs Node.js and, on
first use, the network while `npx` fetches the package.

The editor's check does not port the fonts, textures, shaders, slideshow and
layout groups, nor the project-level probes that open media files to read their
lengths: those need the linter's own parsers and media probing, which is what
the full check is for. A finding in a scene the editor cannot parse is reported
at that file's first line.

## Variables

A composition can expose values a template keeps stable while its content
changes. The plugin reads the declarations, edits the values, keeps them and
shows them in the preview:

- `data-composition-variables` on the `<html>` of a full document, or on a
  template's composition root, declares `id`, `type`, `label`, `default` and
  optional `options`, `min`, `max`, `unit`, `maxLength` and `description`. The
  types are `string`, `number`, `color`, `boolean`, `enum`, `font` and `image`;
  an entry whose default does not match its type is dropped, as the platform
  drops it, and the check reports it.
- `data-var-text="id"` replaces an element's own text; `data-var-src="id"` sets
  `src` on an image, video, audio or source element, for a safe URL only, and
  every scalar (and a font value's family) becomes the `--id` custom property on
  its composition root, so `var(--accent)` responds to a change.
- A scene's host can pin values with `data-variable-values`; they win over the
  user's, as they do in the platform.

The sidebar's **Vars** tab lists the focused composition's declarations with a
control per type: a color well, a switch, a select for an enum, and a text field
for text, numbers, fonts and image paths. A value the user changed is kept in the
plugin's `values-v1` setting by project folder for direct HTML projects (in
`video.json` overrides for component projects), so it survives a restart, and a
reset button puts a variable back to its declared default. A pinned variable says
so. Controls retain their identity and unsaved drafts across unchanged reloads,
and synchronize when the agent changes their saved value or declaration.
Component field writes are serialized and retain the latest queued value per
field while another write or project read is running.

The runtime merges a composition's defaults with the user's values, then the
scene's pinned values, writes the CSS custom properties, the bound text and the
bound sources, and seeks the frame again after a change. Browser updates carry
the entire override snapshot, so replacing a pending update cannot lose a different
field's preceding change. Reset removes the override, allowing each composition's
own default to apply. It also defines
`window.__hyperframes.getVariables()`, which a scene's scripts can read, and
`window.__hfVariables`. The root page's own scripts run before the runtime is
injected, so only scripts that run when a scene mounts can rely on
`getVariables()`.

**Rendering values.** `hyperframes render --variables` reaches a document's own
bindings, but a mounted scene reads its text and sources from the host's
`data-variable-values`, not from `--variables`. So when the user changed any
value, the render runs in a staging folder: a temporary folder that links every
file of the project and holds the project's `index.html` with the user's values
written onto each scene host (a value the host pinned still wins), and the CLI
command also passes `--variables` for the root document. The render therefore
shows what the preview showed, and the project's files are not touched. Without
values, the CLI runs in the project itself.

## The preview

`preview.js` is the editor's runtime, a port of the parts of HyperFrames'
runtime a frame needs. It is embedded in the binary and injected into the page
after the composition's own scripts have run.

The page is the project's `index.html`, loaded from its file, so its relative
assets and its own scripts work as they do in the browser editor. The Go side
reads the scene files and passes them to the runtime, because the page cannot
fetch them itself:

1. Each scene's `<template>` content is mounted into its host. Its scripts are
   taken out of the markup first and run once, each wrapped in a function, after
   the markup is in place: a script moved with its section would run unwrapped
   and collide with the root's own variables.
2. Every composition is found: the root, each scene, and inline nested
   compositions. Each keeps its clips and their windows, with start references
   resolved inside its own composition.
3. A seek shows frame `t`: each composition's paused timeline
   (`window.__timelines[id].seek(local)`) moves to its local time, a scene being
   local to its host window and shifted by `data-playback-start`, clips outside
   `[start, start + duration)` get `visibility: hidden !important`, and media
   elements are set to their own time and paused.

The transport may reach the exact end of the video or a focused scene. The
preview samples one frame before that boundary while keeping the transport at
the requested endpoint, so stopping playback or pressing End holds the last
frame instead of hiding every scene.

A reload can reuse the existing page when its files did not change. Before
mounting scenes again, the runtime disposes the old scene timelines, removes
only its mounted nodes, and restores every visibility property it masked,
including its original priority. Each seek removes timing masks before seeking
animations, then applies the new masks. This preserves authored visibility while
allowing animations such as GSAP `autoAlpha` to reveal a clip after its opening
frame. Repeated reloads neither duplicate scenes nor leave inactive scenes
permanently hidden. Timelines declared
by the source but missing at runtime are reported in the stage, independently
of the source check.

The preview never plays media: it scrubs. Sound is the render's. A composition
with no paused timeline still shows its clips, just without animation. The
runtime reports what it could not do; the stage shows the first such error.

The stage holds one web page, created with `shell.Host.NewWebView`. A changed
project gets a new native page after all consumers of the old page are cancelled.
Readiness requires the full project file path, so an old document at the same
URL cannot accept the new runtime before navigation finishes. A background
revision tracks every input's name, size and modification time, plus the contents
of HTML, CSS, JavaScript, JSON and SVG sources; additions and deletions count.
Hidden files, `node_modules` and `renders` are excluded. Binary assets use metadata
without rereading entire media files. Scene payloads use the source snapshot that
produced their parsed timing, with no file reads during mounting on the UI thread. The page is laid out at the
composition's own size and `SetZoom` fits it to the stage, so CSS pixels are the
composition's pixels. The cached fit belongs to the current native page: a
replacement page invalidates it, and a completed page load reapplies the latest
fit even when the stage has not resized. WebKit may not set text under its minimum font size, so
very small type can read larger in a reduced preview than in the render; the
render is the reference.

The page never plays itself and never reports time: Go drives the playhead. A
tick asks the page for the frame after a 33 ms wait, posts a draw with the time
it answered, and schedules the next tick; **Play** therefore costs one `Eval`
and one frame every 33 ms while it runs. The clock runs on wall time from where
playback started, not from the last tick, so a slow frame drops instead of
stalling the film; seeking while playing starts the clock again from the new
playhead. The transport's button, the timeline and
space, the arrow keys, Home and End move the playhead while paused.

## The timeline

The timeline draws one composition's clips, one row per `data-track-index`, in
time order, with a ruler that picks a tick step for the room, a playhead and each
clip's label. It is native: the ruler, playhead, rows and hover state are painted
into one box that takes the pointer.

It edits the root composition, or a scene the user opened from the sidebar. The
focused scene's own time is what the timeline draws: its playhead is the scene's
local time, the transport shows that time and the scene's length, and playback
stops at the scene's end. The header carries the scene's name with a button back
to the main composition, and a drag on the root timeline moves or trims a scene's
window there.

- A click on the ruler or empty space scrubs. A click on a clip selects it and
  opens its **Clip** panel.
- Dragging a clip's middle moves it, rounded to a frame; dropping it on another
  lane writes that lane's `data-track-index`. Dragging an edge trims that edge,
  keeping the other one still. A click that moves nothing writes nothing.
- Releasing writes the clip's `data-start` and `data-duration` into its own file,
  through `setClipTiming`, which replaces only those values. When a root clip now
  ends past the root's declared `data-duration`, the root's length grows to the
  clip's end, as the HyperFrames editor does; a clip inside a scene leaves the
  scene's own length alone, the timeline draws that length, and the scene's
  window is the host's timing.
- The project is read again after the write, so the timeline shows what the file
  says, not what the drag showed. The scene focus survives the reload while the
  scene is still there; its local playhead follows a host whose start or rate changed.
- Undo and redo put an edit back: the header's arrows, or ⌘Z and ⇧⌘Z while the
  timeline has the keyboard. They cover moves, trims, lane changes and a clip's
  words, not scene creation or variable values, and a new edit drops the redo
  steps. History changes only after a successful write; failed saves and failed
  undo/redo leave the stacks intact. Undoing a root move also restores the film
  length it extended. An agent refresh that changes project inputs clears old
  history so it cannot overwrite the agent's edits.

The timeline fits the composition at first: scrolling pans the film, a
horizontal gesture pans it too, and a pinch or ⌘-scroll zooms around the
pointer. When the tracks are taller than the timeline, a wheel scrolls them
instead and a thin scrollbar on the right says where the view is; ⇧-scroll
pans the film then. The transport's **−**, **Fit** and **+** buttons zoom from
1× to 20× (⌘=, ⌘− and ⌘0 while the timeline has the keyboard). The ruler picks
its tick step for the zoom, about seventy DIPs apart, and the view cannot leave
the composition.

The sidebar has four tabs: **Scenes**, the compositions the root mounts, in
order, with their windows; clicking one edits its clips on the timeline and
leaves nothing selected, and **Add scene** writes a four-second scene and mounts
it at the end; **Clip**, the selected clip's file, element, start, duration, end,
track and words, which opens when a clip is clicked on the timeline; **Vars**,
the focused composition's declarations, described above; and **Check**, the
findings, whose row opens the clip's composition, selects it and moves the
playhead to its start. A finding that names only a file leaves the check on show,
with its file and line, since there is no clip to open. In direct HTML projects, a clip whose words are
its own text (a title, a caption) can be rewritten there: Enter or **Save**
writes the element's text and reloads, and undo takes the words back. Save waits
for a change. An element whose words sit around other elements is read-only
there, since rewriting it would remove those elements.

## The storyboard

The header's grid button captures the film's frames for review. It asks the
preview page for nine evenly spaced frames across what the timeline draws (the
scene's own span when a scene is focused, the whole film otherwise), captures
each from the page and shows them over the window: one card per moment, with
its timecode. Choosing a card puts the playhead there, and Esc or **Close** puts
the storyboard away.

The frames are the preview's own, so they show the user's variable values and
the timeline's clip visibility without a render; playback pauses while they are
taken, and the playhead returns to where it was. A capture is a page `Eval`
seek and a `Capture`, both on a worker, and the PNG is decoded and built into a
bitmap there too. Closing the storyboard cancels a capture still in flight and restores the
playhead frame. Capture failures also restore that frame. Each sample uses the
focused scene's end boundary, so the final sample holds that scene's last frame.

## The full-window page

The drawer shares the page with the conversation, which leaves the stage and the
timeline narrow. The header drops the check's words and the transport the
composition's size when the editor is narrow, so every action stays reachable.
**Open Video Editor** (File) and the drawer header's expand button show the same
editor in `shell.Pages` as the `video.page` page: the header, the stage, the
transport, the timeline and the sidebar at the window's width. It shows the conversation's video; when the conversation on show is not a
video, or none is open, the page says so and offers the way back to the chat.
The page's header button and the empty state call `host.ShowPage(chat.PageID)`,
so the way back needs no tab.

## Rendering

**Render** runs the official CLI in the project folder:

```
npx --yes hyperframes render --output renders/<title>.mp4
```

The chevron beside it chooses the format (MP4, WebM, MOV, GIF) and the quality
(draft, standard, high), which the plugin remembers in `render-v1`; the CLI gets
`--format` and `--quality` only when they are not the defaults, and the output
file takes the format's extension. **Render** is disabled while a render runs.
With errors in the check it stays clickable and says what blocks the render, so
the button explains itself, and the header's check chip says the count and opens
the findings. The job runs
on a worker, reads the CLI's progress lines into a bar, keeps the last lines it
wrote, and opens the MP4 when it finishes; the sidebar says what happened,
**Show** opens the render's folder, and **Stop** cancels the job and the CLI
process with it. A failure shows the CLI's error and the last three lines it
wrote, without its progress lines, and **Copy** puts the whole log on the
clipboard. `npx` is looked up on the usual
places as well as `PATH`, since a window started from the Dock has a short one.
Rendering needs Node.js and the network on first use, when `npx` fetches the
package.

## Threading and lifetimes

The editor's state belongs to the UI thread. Work runs through `kernel.Go`,
tied to a navigation scope the plugin replaces when the open project changes:

| Work | Task | What it does |
| --- | --- | --- |
| `load` | `loading` | `openProject` and `checkProject` off the UI thread |
| `mount` | `mounting` | waits for the page, injects the runtime, reads its answer |
| `seek` | `seeking` | asks the page for a frame while paused |
| tick | `ticking` | one frame per turn while playing |
| timing write | `editing` | rewrites a clip's timing, then reloads |
| add scene | `adding` | writes a scene file and its host |
| render | `rendering` | stages the project with the user's values and runs the CLI |
| variable change | `apply` | sets one value in the page |
| discovery | `scanning` | lists the folder's projects for the header menu |

Closing the editor, changing conversation or switching project closes the web
page; the plugin stops the work that uses the page and closes it on dispose too.
The page is not created until a project is open, and no work runs for a
conversation that is not a video.

The editor follows the agent: after a tool that may change files (anything but a
read, search or fetch) and when a turn ends, it reads the project again and
replaces the native page if its inputs changed. Reload notifications are coalesced
while a project read or editor mutation runs; a notification arriving during a
read schedules one subsequent read. Project generation therefore never overlaps
another editor read or write. Loading pauses playback and cancels seeks and
storyboard capture. Invalid projects close the stale preview and recover on the
next successful refresh. Findings inside a scene seek its local clip time mapped
back to the film's clock.

## Tests

`lifecycle_test.go` covers completion-driven refreshes of root/scene HTML,
styles, scripts and media, preserved source timestamps, added/deleted inputs,
coalesced notifications, save/read serialization, failed writes, root-length
undo/redo, agent history invalidation, finding time mapping, storyboard failure,
closed-page cancellation, field refresh, queued component saves and invalid
project recovery. `render_unix_test.go` exercises the actual command invocation
with a local CLI double for success, failure logs and missing output.
`html_test.go` pins the scanner's offsets, comments, raw text and unclosed
markup. `composition_test.go` pins the start grammar and reference resolution.
`check_test.go` pins the check against a project that breaks each group of rules,
and that a new project is clean. `edit_test.go` pins surgical timing writes and
scene creation. `project_test.go` pins discovery, opening with a missing scene
and timing resolution. `preview_runtime_test.go` executes the embedded JavaScript against parsed project
trees using a small DOM double and Node.js, when available. It covers scene
visibility through a seven-scene sequence, terminal and focused end frames,
repeated mounts, authored visibility and missing animation timelines. It does
not replace browser rendering checks.
`excel_components_test.go` compiles and seeks every component, validates numeric
series and selection bounds, checks layout restrictions and animation options,
and imports a new demo into an older snapshot without changing its saved
components. `structured_test.go` verifies all starters against the quick check, stable
regeneration, escaped content, durable field overrides and reset, scene timing
undo/redo, validation failures preserving saved files, native component editing
and the starter chooser's lifetime. `video_test.go` also pins the storyboard's
nine frames and the overlay.
`cli_check_test.go` pins the CLI report's conversion and the panel's way to it.
`video_test.go` runs the plugin on `kerneltest` with a
fake page: it opens the editor beside a video conversation, draws it, closes it
on another conversation, seeks from the ruler, drags a root clip and a clip
inside a scene, starts a new video, supplies authoring context and survives a window with
no web pages. `variables_test.go` pins declaration parsing, the merge order, the
panel, keeping and resetting a value, the check's variable rules, the render
arguments, the CLI's progress lines and the staging folder. `ui_test.go` pins
the editor's edges: the ruler stays inside the timeline, a narrow header keeps
its actions, selecting a clip opens its panel, a scene row focuses without
selecting, the render explains check errors, the tracks scroll when they do not
fit, and a clip too short for its edges still moves.

## Not there yet

The timeline edits one composition at a time, so a scene's clips are reached by
opening the scene, not by expanding its host in place. A scene's scripts must be
inline: the root page loads its own scripts, but the runtime does not fetch a
`<script src>` inside a scene's template. The panel edits variable values, not
the declarations: changing a declaration is a source edit for the agent, and a
font or image declared as a typed object is edited by its family name or URL.
`--variables-file`, batch rows and strict variables are not used. Captions,
snapshot review, audio preview, linked clips, keyframes, and cloud or
distributed rendering are not ported. A render is local and synchronous from the
editor's point of view.

Component projects currently support landscape layouts only, flat CSS, built-in
collections, explicit imports of installed components and a text representation
of table data. There is no catalog download,
component upgrade UI, mixed custom-HTML scene inside a component document,
narration generation or automatic formula evaluation. Use a blank HTML project
for unrestricted custom compositions.
