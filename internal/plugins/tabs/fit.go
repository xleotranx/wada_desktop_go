package tabs

import (
	"slices"

	"wada-desktop-go/internal/contract/tabs"
)

const overflowWidth = 44 // the overflow button and its gap

// fit splits the tabs the strip shows into those drawn and those only the
// overflow menu reaches. Tabs shrink to tabMinWidth; past that, tabs move
// into the menu from the end, never the active one. Without a width, every
// tab is drawn.
func fit(list []tabs.Tab, active string, width float32) (shown, hidden []tabs.Tab) {
	if width <= 0 || float32(len(list))*tabMinWidth <= width {
		return list, nil
	}
	shown = slices.Clone(list)
	for i := len(shown) - 1; i >= 0 && float32(len(shown))*tabMinWidth+overflowWidth > width; i-- {
		if shown[i].ID == active {
			continue
		}
		hidden = slices.Insert(hidden, 0, shown[i])
		shown = slices.Delete(shown, i, i+1)
	}
	return shown, hidden
}
