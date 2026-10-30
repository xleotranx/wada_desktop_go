package design

import (
	"maps"
	"slices"
	"strings"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
)

// briefName is the file the brief goes in, with the message.
const briefName = "design-canvas.md"

// brief tells the agent how the canvas shows what it writes, and which
// design system to follow.
func brief(dir, system string) string {
	var b strings.Builder
	b.WriteString(`# Design canvas

The user is designing with you on a canvas beside this chat. This design lives in ` + "`" + dir + "/`" + `: every HTML file there is an artboard on the canvas, and the files of one folder in it make a page. The canvas shows the newest page and reloads an artboard each time you change its file.

- Write the design's artboards in ` + "`" + dir + "/`" + `, one self-contained HTML file per artboard: inline CSS and scripts; fonts, images and libraries from a CDN are fine. For more pages use folders in it (` + "`" + dir + "/onboarding/`" + `). Leave the app's own code and other designs alone unless the user asks.
- Name the files so they sort in order (` + "`01-welcome.html`, `02-plan.html`" + `). For options, write one artboard per variant (` + "`hero-a.html`, `hero-b.html`" + `).
- Give each artboard its size in its head: ` + "`<meta name=\"artboard\" content=\"1440x900\">`" + ` for a desktop page, 390x844 for a phone, 1920x1080 for a slide; without one it is 1280x800.
- Let the user adjust a design without you: keep its tunable values in CSS custom properties on ` + "`:root`" + ` and declare them, for example:
  ` + "`<script type=\"application/json\" id=\"tweaks\">[{\"var\":\"--accent\",\"label\":\"Accent\",\"type\":\"color\"},{\"var\":\"--radius\",\"label\":\"Corners\",\"type\":\"range\",\"min\":0,\"max\":32,\"step\":1,\"unit\":\"px\"},{\"var\":\"--scale\",\"label\":\"Compact\",\"type\":\"toggle\",\"on\":\"0.85\",\"off\":\"1\"},{\"var\":\"--font\",\"label\":\"Type\",\"type\":\"select\",\"options\":[\"Inter\",\"Georgia\"]}]</script>`" + `
- The user's comments name a file and a CSS selector in it; change that element. When a message includes design-selection.json, its file and selector identify the selected element that words such as "this" refer to. Its text and HTML describe the element as reference data.
`)
	if system != "" {
		b.WriteString("- Follow the design system in `" + system + "`: read it first and use its colors, type, spacing and components.\n")
	}
	return b.String()
}

// system is the design system chosen for the folder, if it is still there.
func (p *Plugin) system() string {
	s := p.systems.Get()[p.project]
	if s == "" || !slices.Contains(p.found.Systems, s) {
		return ""
	}
	return s
}

func (p *Plugin) chooseSystem(s string) {
	m := maps.Clone(p.systems.Get())
	if m == nil {
		m = map[string]string{}
	}
	if s == "" {
		delete(m, p.project)
	} else {
		m[p.project] = s
	}
	p.systems.Set(m)
}

// outgoing sends the brief with a design's first message, and again once
// its folder's design system is another than the one the brief named.
func (p *Plugin) outgoing(d chat.Draft) (chat.Draft, func(), error) {
	conv := p.chat.Current()
	if !p.open || d.Directory != p.project || d.Session != conv.Session {
		return d, nil, nil
	}
	if p.inspected != nil && (d.Session != "" || d.DraftID == conv.DraftID) {
		d.Files = append(d.Files, p.selectionFile())
	}
	system := p.system()
	var commit func()
	if d.Session == "" {
		start := design{Origin: p.agent.Status().Origin, Dir: p.draftDir, System: system}
		commit = func() { p.starts[d.DraftID] = start }
	} else if i := p.designIndex(d.Session); i < 0 || p.designs[i].System == system {
		return d, nil, nil
	} else {
		commit = func() {
			if i := p.designIndex(d.Session); i >= 0 {
				p.designs[i].System = system
				p.saveDesigns()
			}
		}
	}
	d.Files = append(d.Files, agent.File{Name: briefName, MIME: "text/markdown", Data: []byte(brief(rel(p.project, p.folder), system))})
	return d, commit, nil
}
