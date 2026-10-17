package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// contentPane draws the right column: the title bar of the chat, its
// transcript and the composer.
func (a *app) contentPane(c *ui.Context, p palette, headerH float32) {
	ui.Column(c).Grow(1).Background(p.bg).Children(func() {
		a.titleBar(c, p, headerH)
		ui.Divider(c)

		// The transcript with a fade at its foot, so the newest words slip
		// under the composer as they stream in.
		area := ui.Box(c).Grow(1)
		area.Children(func() {
			ui.Scroll(c).Fill().TrackScroll(&a.feed).Selectable().
				Padding(26, 24, 18, 24).Gap(13).Children(func() {
				if a.selected == "" {
					ui.Column(c).Fill().Center().Gap(8).Children(func() {
						ui.Text(c, "What should we work on?").FontSize(26).FontWeight(600).TextColor(p.text)
						ui.Text(c, "Work with your coding agent").FontSize(14).TextColor(p.muted)
					})
					return
				}
				for i := range a.blocks {
					a.blockRow(c, p, a.blocks[i])
				}
			})
			ui.Box(c).Absolute().Start(0).End(0).Bottom(0).Height(26).
				Gradient(p.bg.Alpha(0), p.bg, 180)
		})

		a.composer(c, p)
	})
}

// titleBar shows the chat's name between the window's controls.
func (a *app) titleBar(c *ui.Context, p palette, headerH float32) {
	title := a.selected
	if title == "" {
		title = "New chat"
	}
	icon := icFileDoc
	if title == "New chat" {
		icon = icPen
	}

	ui.Row(c).Height(headerH).DragWindow().Padding(0, 12).Children(func() {
		name := ui.ButtonBase(c).Label(title).Height(32).Radius(9).Padding(0, 10).
			Justify(ui.Start).Cursor(ui.CursorPointer).Tooltip("Switch chat").
			Menu(func(m *ui.Menu) {
				m.Item("Rename…")
				m.Item("Share")
				m.Separator()
				m.Item("Move to project…")
				m.Item("Pin")
				m.Separator()
				m.Item("Archive")
				m.Item("Delete")
			})
		if name.Hovered() {
			name.Background(p.hover)
		}
		name.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(0).Children(func() {
				ui.Icon(c, icon).FontSize(15).TextColor(p.muted)
				ui.Text(c, title).MarginStart(8).FontSize(13).FontWeight(500).SingleLine()
				// where the chat works, small beside its name
				if dir := a.chatDir(); dir != "" {
					ui.Text(c, dirBase(dir)).MarginStart(8).FontSize(12).TextColor(p.muted).SingleLine()
				}
			})
		})

		ui.Spacer(c)

		ui.Row(c).AlignItems(ui.Center).Gap(2).Children(func() {
			a.navButton(c, p, "Chat options", icEllipsis, nil, p.muted)
			a.navButton(c, p, "Chat tasks", icTasks, nil, p.muted)
			a.navButton(c, p, "Layout", icLayout, nil, p.muted)
			ui.Box(c).Width(1).Height(18).MarginX(6).Background(p.hairline)
			a.navButton(c, p, "New chat", icPlus, a.startNewChat, ui.Hex("#3572e3"))
		})
	})
}

// blockRow draws one block of the transcript: a paragraph, what the user
// said, or a line of what the agent ran.
func (a *app) blockRow(c *ui.Context, p palette, b block) {
	ui.Column(c).FillWidth().MaxWidth(760).Margin(0, ui.Auto).Gap(10).Children(func() {
		switch b.kind {
		case "user":
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				ui.Box(c).MaxWidthPercent(76).Radius(18).Padding(9, 14).
					Background(p.bubble).Children(func() {
					ui.Text(c, b.text).FontSize(14).LineHeight(1.45)
				})
			})
		case "activity":
			a.activityRow(c, p, b)
		case "permission":
			a.permissionRow(c, p, b)
		default: // "text", "stream"
			t := ui.Text(c, b.text).FontSize(14).LineHeight(1.45).TextColor(p.text)
			if b.kind == "stream" {
				t.TextColor(p.faint).SingleLine().Ellipsis("…")
			}
		}
	})
}

// activityRow is one line the agent ran, opening its detail below it.
func (a *app) activityRow(c *ui.Context, p palette, b block) {
	open := a.expanded[b.key]
	chev := icChevRight
	if open {
		chev = icChevDown
	}
	row := ui.ButtonBase(c).Label(b.text).Height(28).Radius(8).Padding(0, 6).
		FillWidth().Justify(ui.Start).Cursor(ui.CursorPointer).Tooltip(b.text)
	if row.Hovered() {
		row.Background(p.hover)
	}
	row.OnClick(func() { a.expanded[b.key] = !a.expanded[b.key] })
	row.Children(func() {
		ui.Icon(c, b.icon).FontSize(15).TextColor(p.muted)
		ui.Text(c, b.text).MarginStart(9).FontSize(13).TextColor(p.muted).SingleLine()
		ui.Icon(c, chev).FontSize(12).MarginStart(6).TextColor(p.muted)
	})
	if open && b.detail != nil {
		ui.Row(c).FillWidth().Children(func() { b.detail(c, p) })
	}
}

// permissionRow is one approval the agent raised mid-turn: what it wants
// to do, with the three decisions the server takes. It stays until the
// runner's reply lands, then the card leaves the transcript.
func (a *app) permissionRow(c *ui.Context, p palette, b block) {
	r := b.req
	if r == nil {
		return
	}
	ui.Box(c).FillWidth().Radius(10).Padding(10, 12).Margin(4, 0, 0, 0).
		Background(p.detailBg).Border(1, p.hairline).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icShield).FontSize(15).TextColor(p.orange)
				ui.Text(c, "Approval needed — "+permAsk(r.action)).
					FontSize(13).FontWeight(500).SingleLine()
			})
			for _, res := range r.resources {
				ui.Box(c).FillWidth().Radius(8).Padding(5, 10).
					Background(p.bg).Border(1, p.hairline).Children(func() {
					ui.Text(c, "$ "+res).Font("monospace").FontSize(12).LineHeight(1.5).
						TextColor(p.text).SingleLine()
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				a.permButton(c, p, true, ui.Hex("#3572e3"), ui.Hex("#ffffff"),
					"Allow once", "Let it happen this turn.", func() { a.answerPerm(r, "once") })
				a.permButton(c, p, false, p.hover, p.text,
					"Always", "Keep allowing this pattern.", func() { a.answerPerm(r, "always") })
				a.permButton(c, p, false, p.hover, p.text,
					"Deny", "Refuse and let the agent carry on.", func() { a.answerPerm(r, "reject") })
			})
			if len(r.save) > 0 {
				ui.Text(c, "Always would remember "+strings.Join(r.save, ", ")+".").
					FontSize(12).TextColor(p.muted).SingleLine()
			}
		})
	})
}

// permButton is one small pill of the approval card's decision row; a
// solid button keeps its tint, the rest pick it up while hovered.
func (a *app) permButton(c *ui.Context, p palette, solid bool, bg, tint ui.Color, label, tip string, on func()) {
	b := ui.ButtonBase(c).Label(label).Height(26).Radius(999).PaddingX(12).
		Cursor(ui.CursorPointer).Tooltip(tip)
	if solid {
		b.Background(bg)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(tint).SingleLine()
	})
	b.OnClick(on)
}

// permAsk words what a permission action would do, as the card's header.
func permAsk(action string) string {
	switch action {
	case "bash", "shell":
		return "run a shell command"
	case "edit":
		return "edit files in the workspace"
	case "webfetch":
		return "fetch from the web"
	}
	return "use " + action
}

// memoTranscript is the chat the interface starts on.
func memoTranscript() []block {
	return []block{
		{kind: "text", text: "The full test suite and production build passed: 552 tests passed, with one optional Linux-only check skipped on this Mac. The catalog, migration-history check, and architecture check also passed."},
		{kind: "text", text: "The full gate is now running the browser journeys."},
		{kind: "activity", text: "Ran command", icon: icTerminal, key: "activity:ran-command",
			detail: commandDetail(
				"$ go tool mygo test ./...",
				"ok  wada-desktop-go  2.414s",
				"$ go tool mygo build .",
				"build ok, one binary ready.")},
		{kind: "text", text: "I found two more edge cases during review: the instruction editor could adopt a newer revision while someone was still drafting, and recovery still depended on workspace files after evidence had already been saved."},
		{kind: "text", text: "I'll fix both, add regression coverage, and restart the full gate against the final changes."},
		{kind: "activity", text: "Read files, ran commands", icon: icBook, key: "activity:read-1",
			detail: commandDetail(
				"$ read main.go provider.go",
				"2 files, 214 lines",
				"$ grep -rn \"revision\" internal/ | wc -l",
				"42")},
		{kind: "activity", text: "Viewed an image", icon: icImage, key: "activity:viewed-image",
			detail: imageDetail},
		{kind: "activity", text: "Read files, ran commands", icon: icBook, key: "activity:read-2",
			detail: commandDetail(
				"$ read evidence/summary.md",
				"$ go tool mygo test ./internal/work -run TestRecovery",
				"ok  wada-desktop-go/internal/work  0.831s")},
		{kind: "text", text: "The updated suite passed: 557 tests, with one optional Linux-only skip. The browser check also exposed an accessibility-label issue in the revision editor, which I corrected."},
		{kind: "text", text: "I found one final integration detail: captured files reach the worker, but its prompt doesn't list their names. I'll correct that so the worker can discover the sealed inputs."},
		{kind: "activity", text: "Loaded a tool, read files, ran commands", icon: icPlug, key: "activity:tool",
			detail: commandDetail(
				"$ tool debugging-and-error-recovery",
				"$ read worker/prompt.go",
				"$ go tool mygo test ./worker -run TestSealedInputs")},
		{kind: "text", text: "The latest full run stopped on a channel-test teardown error: a background callback tried to use the test database after it closed. The Work tests passed."},
		{kind: "text", text: "I'm using the debugging-and-error-recovery skill to check that failure in isolation before rerunning acceptance."},
		{kind: "activity", text: "Ran commands", icon: icTerminal, key: "activity:ran-commands",
			detail: commandDetail(
				"$ go tool mygo test -run TestChannelTeardown ./internal/hub",
				"ok  wada-desktop-go/internal/hub  0.512s")},
		{kind: "text", text: "The channel failure did not reproduce in either the isolated test or the full channel test file. I've preserved the failed run for reference and left that unrelated code unchanged."},
		{kind: "text", text: "The acceptance rerun has passed tests, typecheck, and build. Browser verification is continuing."},
		{kind: "stream", text: "The UI replacement for the \"Current revision form\" appears successful. I need to clarify the useState(item.currentRevision) update path before handing the diff back for review."},
	}
}

// commandDetail is the expanded detail of an activity: what ran, written
// as a terminal keeps it.
func commandDetail(lines ...string) func(c *ui.Context, p palette) {
	return func(c *ui.Context, p palette) {
		ui.Box(c).FillWidth().Radius(10).Padding(10, 12).Margin(4, 0, 0, 0).
			Background(p.detailBg).Border(1, p.hairline).Children(func() {
			ui.Column(c).Gap(3).Children(func() {
				for _, l := range lines {
					ui.Text(c, l).Font("monospace").FontSize(12).LineHeight(1.55).
						TextColor(p.text).SingleLine()
				}
			})
		})
	}
}

// imageDetail is the expanded detail of "Viewed an image": a small mock
// of the screenshot the agent read.
func imageDetail(c *ui.Context, p palette) {
	ui.Box(c).Width(112).Margin(6, 0, 0, 0).Padding(12).
		Background(p.bg).Border(1, p.hairline).Radius(10).Children(func() {
		ui.Column(c).Gap(5).Children(func() {
			ui.Box(c).Size(66, 4).Radius(2).Background(p.thumbLine)
			ui.Box(c).Size(44, 4).Radius(2).MarginStart(10).Background(p.thumbLine)
			ui.Box(c).Size(74, 4).Radius(2).Background(ui.Hex("#6b6b6b"))
			ui.Box(c).Size(30, 4).Radius(2).MarginStart(10).Background(ui.Hex("#6b6b6b"))
			ui.Box(c).Size(58, 4).Radius(2).Background(p.thumbLine)
		})
	})
}
