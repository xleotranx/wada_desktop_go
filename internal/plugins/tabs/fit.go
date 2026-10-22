package tabs

import (
	"path/filepath"
	"slices"
	"strconv"
	"unicode/utf8"

	"wada-desktop-go/internal/contract/tabs"
)

// How the strip fits its tabs into its width. Tabs shrink to tabMinWidth;
// past that, the groups the active tab is not in collapse to their labels,
// farthest first, and then tabs move into the overflow menu, from the end,
// never the active one.

const (
	groupGap      = 10 // between groups
	overflowWidth = 44 // the overflow button and its gap
	labelChar     = 6.4
	labelPadding  = 20 // the label's own and its row's
)

// stripGroup is one folder's run of tabs as the strip draws it.
type stripGroup struct {
	folder    string
	tabs      []tabs.Tab // all of the run
	shown     []tabs.Tab // drawn after the label
	collapsed bool       // shows its label with a count, by the user or to fit
	manual    bool       // the user collapsed it
}

// stripPlan is what the strip draws: its groups, and the tabs only the
// overflow menu reaches.
type stripPlan struct {
	groups []stripGroup
	hidden []tabs.Tab
}

// fit lays the tabs out for width; without a width, as if it were endless.
func fit(list []tabs.Tab, active string, manual map[string]bool, width float32) stripPlan {
	var plan stripPlan
	activeGroup := -1
	for start := 0; start < len(list); {
		end := start + 1
		for end < len(list) && list[end].Folder == list[start].Folder {
			end++
		}
		run := list[start:end]
		g := stripGroup{folder: run[0].Folder, tabs: run, manual: manual[run[0].Folder] && run[0].Folder != ""}
		g.collapsed = g.manual
		if slices.ContainsFunc(run, func(t tabs.Tab) bool { return t.ID == active }) {
			activeGroup = len(plan.groups)
		}
		plan.groups = append(plan.groups, g)
		start = end
	}
	for i := range plan.groups {
		plan.groups[i].shown = shownOf(plan.groups[i], active)
	}
	if width <= 0 || plan.need() <= width {
		return plan
	}
	// Collapse the other groups, farthest from the active one first.
	order := make([]int, 0, len(plan.groups))
	for i, g := range plan.groups {
		if i != activeGroup && g.folder != "" && !g.collapsed {
			order = append(order, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return distance(b, activeGroup) - distance(a, activeGroup) })
	for _, i := range order {
		plan.groups[i].collapsed = true
		plan.groups[i].shown = shownOf(plan.groups[i], active)
		if plan.need() <= width {
			return plan
		}
	}
	// Then move tabs into the overflow menu, from the end.
	for gi := len(plan.groups) - 1; gi >= 0 && plan.need() > width; gi-- {
		g := &plan.groups[gi]
		for ti := len(g.shown) - 1; ti >= 0 && plan.need() > width; ti-- {
			if g.shown[ti].ID == active {
				continue
			}
			plan.hidden = slices.Insert(plan.hidden, 0, g.shown[ti])
			g.shown = slices.Delete(slices.Clone(g.shown), ti, ti+1)
		}
	}
	return plan
}

// shownOf is what a group draws after its label: everything, or once
// collapsed only the active tab.
func shownOf(g stripGroup, active string) []tabs.Tab {
	if !g.collapsed {
		return g.tabs
	}
	var out []tabs.Tab
	for _, t := range g.tabs {
		if t.ID == active {
			out = append(out, t)
		}
	}
	return out
}

// need is the width the plan takes at its narrowest.
func (s stripPlan) need() float32 {
	var w float32
	drawn := 0
	for _, g := range s.groups {
		if g.folder == "" && len(g.shown) == 0 {
			continue
		}
		drawn++
		if g.folder != "" {
			w += labelWidth(g)
		}
		w += float32(len(g.shown)) * tabMinWidth
	}
	if drawn > 1 {
		w += float32(drawn-1) * groupGap
	}
	if len(s.hidden) > 0 {
		w += overflowWidth
	}
	return w
}

// labelText is a group's label: its folder's name, with its count while
// some of its tabs are not drawn.
func labelText(g stripGroup) string {
	name := filepath.Base(g.folder)
	if len(g.shown) < len(g.tabs) {
		name += " · " + strconv.Itoa(len(g.tabs))
	}
	return name
}

func labelWidth(g stripGroup) float32 {
	return labelPadding + labelChar*float32(utf8.RuneCountInString(labelText(g)))
}

func distance(i, from int) int {
	if from < 0 {
		return i
	}
	return max(i-from, from-i)
}
