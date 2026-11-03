package video

import (
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
)

// briefName is the file the brief goes in, with the first message.
const briefName = "video-editor.md"

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
- Animating a ` + "`.clip`" + ` element's ` + "`display`" + `, ` + "`visibility`" + ` or ` + "`autoAlpha`" + ` hides it from the editor: animate a child instead.

## Checking

The editor's Check panel lists what breaks a render, with a fix. Fix the errors before rendering; a render runs the HyperFrames CLI and writes an MP4 in ` + "`renders/`" + `.
`)
	return b.String()
}

// outgoing sends the brief with a video's first message.
func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	conv := p.chat.Current()
	if !p.open || d.Directory != p.folder || d.Session != conv.Session {
		return d, nil, nil
	}
	if d.Session != "" && p.videoIndex(d.Session) >= 0 {
		// The conversation already knows how the editor reads its project.
		return d, nil, nil
	}
	start := video{Origin: p.agent.Status().Origin, Dir: p.draftDir}
	d.Files = append(d.Files, agent.File{Name: briefName, MIME: "text/markdown", Data: []byte(brief(rel(p.folder, p.draftDir)))})
	return d, func() {
		if d.Session == "" {
			p.starts[d.DraftID] = start
		}
	}, nil
}
