package video

import (
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
)

// brief tells the agent how the editor reads the project: the HyperFrames
// rules a render depends on.
func brief(dir string) string {
	var b strings.Builder
	b.WriteString(`# Video editor

The user edits a HyperFrames video beside this chat. The project is ` + "`" + dir + "/`" + `: ` + "`index.html`" + ` is the root composition and ` + "`compositions/*.html`" + ` are the scenes it mounts. The editor on the right shows the frame at the playhead and draws the timeline from the files; the user's drags write ` + "`data-start`" + ` and ` + "`data-duration`" + ` into them. Read a file before you change it, and keep its existing timing attributes.

## The project

- ` + "`index.html`" + `: one element with ` + "`data-composition-id`" + `, ` + "`data-width`" + `, ` + "`data-height`" + `, ` + "`data-fps`" + ` and ` + "`data-duration`" + `: the film's length.
- One scene per file in ` + "`compositions/`" + `, its markup inside a ` + "`<template>`" + ` and ` + "`data-composition-id`" + ` on its root. Mount it from the root with ` + "`data-composition-src=\"compositions/scene-01.html\"`" + ` and its own ` + "`data-start`" + ` and ` + "`data-duration`" + `.
- ` + "`assets/`" + ` holds media and fonts. Write media paths ` + "`assets/...`" + `, never from the server root.
- A new video needs one scene per beat of the story, short and readable ids.

## Timing

- Every timed element needs ` + "`class=\"clip\"`" + `, a ` + "`data-start`" + ` and, unless it is media, a ` + "`data-duration`" + `.
- ` + "`data-start`" + ` is seconds, or a clip id: ` + "`intro`" + `, ` + "`intro + 0.5`" + `, ` + "`intro - 0.5`" + `. It resolves to the end of the clip it names, inside the same composition.
- ` + "`data-track-index`" + ` is the lane the editor draws; paint order is CSS ` + "`z-index`" + `.
- Timing inside a scene is local to that scene. The scene's window in the root is the host's ` + "`data-start`" + ` and ` + "`data-duration`" + `.
- A clip shows over ` + "`[start, start + duration)`" + `: land its end state just before its end.
- Media: give a ` + "`<video>`" + ` or ` + "`<audio>`" + ` an ` + "`id`" + `, a ` + "`data-start`" + `, and ` + "`muted`" + ` or ` + "`data-has-audio=\"true\"`" + ` on video.

## Animation

- Each composition builds one paused timeline and registers it after its tweens: ` + "`window.__timelines[\"<data-composition-id>\"] = tl`" + `.
- Everything visible must be a function of timeline time. No ` + "`Date.now()`" + `, ` + "`Math.random()`" + ` without a seed, timers, ` + "`requestAnimationFrame`" + `, ` + "`repeat: -1`" + `, or a fetch after load.
- Never ` + "`play()`" + `, ` + "`pause()`" + ` or seek media in script: time it with data attributes so preview and render agree.
- Keep the first frame readable. Drive opacity and visibility through the paused timeline; timing controls the clip window.

## Checking

The editor's Check panel lists what breaks a render, with a fix. Fix the errors before rendering; a render runs the HyperFrames CLI and writes an MP4 in ` + "`renders/`" + `.
`)
	return b.String()
}

// outgoing supplies current authoring context on every video turn and records
// the new video when its first message starts a session.
func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	conv := p.chat.Current()
	if !p.open || d.Directory != p.folder || d.Session != conv.Session {
		return d, nil, nil
	}
	d.Project = &agent.ProjectScope{Kind: "video", Directory: p.dir}
	instructions := brief(p.dir)
	if p.project != nil && p.project.Document != nil {
		instructions = componentBrief(p.dir)
	}
	if d.Instructions != "" {
		d.Instructions += "\n"
	}
	d.Instructions += videoTaskScope + "\n" + instructions
	if d.Session != "" && p.videoIndex(d.Session) >= 0 {
		// The conversation already knows how the editor reads its project.
		return d, nil, nil
	}
	start := video{Origin: p.agent.Status().Origin, Dir: p.draftDir}
	return d, func() {
		if d.Session == "" {
			p.starts[d.DraftID] = start
		}
	}, nil
}

func componentBrief(dir string) string {
	return "# Component video project\n\nProject: `" + dir + "/`. Use project inspect first. `video.json` is the source of truth; the editor regenerates index.html and compositions/*.html. Do not edit generated HTML.\n\nReuse definitions from snapshot.components, snapshot.layouts and snapshot.themes, referenced by exact id and version. Each scene has a unique lowercase id, title, layout, start, duration (3–120 seconds), track and parts. Each part has a unique id, component reference and inputs matching that component's fields. Omitted inputs use the field default. Respect field limits, component.layouts and layout capacity; the collection supports 1920×1080 only. Detailed Excel demos require a full-width centered scene. Use defaultDuration when choosing a scene length. Prefer the specialized range, AutoFill, Flash Fill, filter, sort, duplicates, text-columns, lookup, conditional-formatting and chart components when their exact IDs exist in the snapshot. Supply before/after data and verified results; the components illustrate actions without running Excel. Use a component's transition field to choose wipe, slide or zoom when available, rather than rewriting its animation.\n\nChange content through scene parts' inputs; preserve reusable markup, CSS and motion. The overrides map contains user edits under scene--part--field keys; these win over inputs. Update or remove a conflicting override when explicitly asked to change that field. Duplicate or reorder scenes by editing their IDs and starts, and keep IDs unique. When removing a scene or part, remove its overrides too.\n\nThe editor validates on reload. Fix any reported document errors. Excel examples are illustrations, not a spreadsheet engine: verify formulas, displayed results, and platform-specific shortcuts. Narration is optional and absent from these starters; do not generate audio unless asked.\n"
}

const videoTaskScope = `You are creating or editing the user's active video. Stay focused on the requested video content.
Use the project tool: inspect returns the current scenes and exact reusable component fields; update saves and validates a component video; read/write edit an HTML video's source; check reports editor findings.
The host has already selected the project. Do not search for it, inspect sibling projects, read chat transcripts or session metadata, inspect app databases or source code, search the user's home folder, or infer requirements from previous conversations.
Use only the current request, conversation, supplied files and the active project's content. When a creative detail is unspecified, choose a reasonable default and make the video.
The app owns preview refresh, HTML generation for component videos, and rendering. Do not debug the editor, install CLIs, make separate preview projects, poll file timestamps, or run background discovery. Report a project error from the tool if it blocks the task.
After saving, use check to verify the video and give a short summary of what you created. Do not claim an MP4 was rendered unless the app actually rendered one.`
