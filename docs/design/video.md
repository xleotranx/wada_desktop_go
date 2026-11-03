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
the user picks; a worker makes the first `videos/video-N` not there yet with a
scene and an empty root timeline, and a new chat opens in the conversation's
folder beside it. The first message records the video; a new video left before
its first message is a plain new chat. The conversation's chat history stays in
the transcript; the video is files in the folder.

The brief `video-editor.md` goes with the first message: it tells the agent the
timing attributes, the scene layout, the deterministic-animation rules and that
the editor writes `data-start` and `data-duration` into the files it reads.

A conversation opens the editor for the project recorded for it. The project
menu in the header lists the folder's other projects (the folder itself when its
`index.html` is a composition, and each folder under `videos/`) and switches to
one; the choice is remembered for that conversation.

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
| `timeline_element_missing_timing` | error | a top-level element with no duration where the timeline can draw its end |
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

The check does not port the variables, captions, fonts, textures, shaders,
slideshow, contrast and layout groups, nor the project-level probes that open
media files to read their lengths: those need the linter's own parsers and
media probing. A finding in a scene the editor cannot parse is reported at
that file's first line.

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
plugin's `values-v1` setting by project folder, so it survives a restart, and a
reset button puts a variable back to its declared default. A pinned variable says
so.

The runtime merges a composition's defaults with the user's values, then the
scene's pinned values, writes the CSS custom properties, the bound text and the
bound sources, and seeks the frame again after a change. It also defines
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

The preview never plays media: it scrubs. Sound is the render's. A composition
with no paused timeline still shows its clips, just without animation. The
runtime reports what it could not do; the stage shows the first such error.

The stage holds one web page, created with `shell.Host.NewWebView`. It is loaded
again when the project folder changes or when any of its files changed since it
loaded, and the runtime is injected again. The page is laid out at the
composition's own size and `SetZoom` fits it to the stage, so CSS pixels are the
composition's pixels. WebKit may not set text under its minimum font size, so
very small type can read larger in a reduced preview than in the render; the
render is the reference.

The page never plays itself and never reports time: Go drives the playhead. A
tick asks the page for the frame after a 33 ms wait, posts a draw with the time
it answered, and schedules the next tick; **Play** therefore costs one `Eval`
and one frame every 33 ms while it runs. The transport's button, the timeline and
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

- A click on the ruler or empty space scrubs. A click on a clip selects it.
- Dragging a clip's middle moves it, rounded to a frame. Dragging an edge trims
  that edge, keeping the other one still.
- Releasing writes the clip's `data-start` and `data-duration` into its own file,
  through `setClipTiming`, which replaces only those values. When a root clip now
  ends past the root's declared `data-duration`, the root's length grows to the
  clip's end, as the HyperFrames editor does; a clip inside a scene is bounded by
  the scene's own length, and the scene's window by the host's timing.
- The project is read again after the write, so the timeline shows what the file
  says, not what the drag showed. The scene focus survives the reload while the
  scene is still there.

The sidebar has three tabs: **Scenes**, the compositions the root mounts, in
order, with their windows; clicking one opens its clips on the timeline, and
**Add scene** (in the root's view) writes a four-second scene and mounts it at the
end; **Clip**, the selected clip's file, element, start, duration, end and track;
and **Check**, the findings, whose row selects the clip it names.

## Rendering

**Render** runs the official CLI in the project folder:

```
npx --yes hyperframes render --output renders/<title>.mp4
```

It is disabled while the check has errors, and while a render runs. The job runs
on a worker, keeps the last lines the CLI wrote, and opens the MP4 when it
finishes; the sidebar says what happened. `npx` is looked up on the usual
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
page; the plugin closes it on dispose too. The page is not created until a
project is open, and no work runs for a conversation that is not a video.

The editor follows the agent: after a tool that may change files (anything but a
read, search or fetch) and when a turn ends, it reads the project again and
reloads the page if its files changed.

## Tests

`html_test.go` pins the scanner's offsets, comments, raw text and unclosed
markup. `composition_test.go` pins the start grammar and reference resolution.
`check_test.go` pins the check against a project that breaks each group of rules,
and that a new project is clean. `edit_test.go` pins surgical timing writes and
scene creation. `project_test.go` pins discovery, opening with a missing scene
and timing resolution. `video_test.go` runs the plugin on `kerneltest` with a
fake page: it opens the editor beside a video conversation, draws it, closes it
on another conversation, seeks from the ruler, drags a root clip and a clip
inside a scene, starts a new video, sends the brief and survives a window with
no web pages. `variables_test.go` pins declaration parsing, the merge order, the
panel, keeping and resetting a value, the check's variable rules, the render
arguments, the CLI's progress lines and the staging folder.

## Not there yet

The timeline edits one composition at a time, so a scene's clips are reached by
opening the scene, not by expanding its host in place. A scene's scripts must be
inline: the root page loads its own scripts, but the runtime does not fetch a
`<script src>` inside a scene's template. The panel edits variable values, not
the declarations: changing a declaration is a source edit for the agent, and a
font or image declared as a typed object is edited by its family name or URL.
`--variables-file`, batch rows and strict variables are not used. Captions,
snapshot review, audio preview, linked clips, keyframes, undo, and cloud or
distributed rendering are not ported. A render is local and synchronous from the
editor's point of view.
