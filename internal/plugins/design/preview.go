package design

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/xleotranx/wada-kit/native"
)

// A zero previewSize uses the artboard's declared dimensions. Overrides belong
// to the open design and never alter the HTML or reload its WebView.
type previewSize struct {
	W float32 `json:"width"`
	H float32 `json:"height"`
}

func (s previewSize) label() string { return fmt.Sprintf("%g × %g", s.W, s.H) }

var previewPresets = []struct {
	name string
	size previewSize
}{
	{"Phone", previewSize{390, 844}},
	{"Tablet", previewSize{768, 1024}},
	{"Desktop", previewSize{1440, 900}},
}

// previewPage derives the visible geometry without changing the scanner's
// declaration, so Original always uses the latest size from the file.
func (p *Plugin) previewPage(pg page) page {
	if len(p.previews) == 0 {
		return pg
	}
	pg.Boards = slices.Clone(pg.Boards)
	for i := range pg.Boards {
		a := &pg.Boards[i]
		if size, ok := p.previews[a.Path]; ok {
			a.W, a.H = size.W, size.H
		}
	}
	pg.layout()
	return pg
}

func (p *Plugin) setPreview(path string, size previewSize) {
	_, ok := p.current().board(path)
	if !ok || p.boards[path] == nil || p.previews[path] == size {
		return
	}
	if size == (previewSize{}) {
		delete(p.previews, path)
	} else {
		if size.W < minSide || size.W > maxSide || size.H < minSide || size.H > maxSide {
			return
		}
		if p.previews == nil {
			p.previews = map[string]previewSize{}
		}
		p.previews[path] = size
	}
	p.probing.Cancel()
	p.clearInspection()
	p.closeCard()
	a, _ := p.current().board(path)
	p.frameBoard(a)
	p.refreshAnchors(path)
}

func (p *Plugin) prunePreviews(l listing) {
	for path := range p.previews {
		found := false
		for _, pg := range l.Pages {
			if _, ok := pg.board(path); ok {
				found = true
				break
			}
		}
		if !found {
			delete(p.previews, path)
		}
	}
}

func (p *Plugin) previewMenu(c *ui.Context, pal native.Palette, a artboard) {
	original, _ := p.found.page(p.page)
	source, _ := original.board(a.Path)
	declared := previewSize{source.W, source.H}
	chosen := p.previews[a.Path]
	label := "Preview size for " + a.name()
	b := ui.ButtonBase(c.Key("preview-size")).Label(label).Tooltip(label+" · "+(previewSize{a.W, a.H}).label()).
		Height(28).Padding(0, 8).Radius(7).Gap(5).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Menu(func(m *ui.Menu) {
		if m.Item("Original — " + declared.label()).Checked(chosen == (previewSize{})).Chosen() {
			p.navigation.Post(func() { p.setPreview(a.Path, previewSize{}) })
		}
		m.Separator()
		for _, preset := range previewPresets {
			if m.Item(preset.name + " — " + preset.size.label()).Checked(chosen == preset.size).Chosen() {
				p.navigation.Post(func() { p.setPreview(a.Path, preset.size) })
			}
		}
	})
	b.Children(func() {
		ui.Icon(c, lucide.Monitor()).FontSize(13).TextColor(pal.Muted)
		ui.Text(c, (previewSize{a.W, a.H}).label()).FontSize(12).TextColor(pal.Text).FontFeatures("tnum").SingleLine()
		ui.Icon(c, lucide.ChevronDown()).FontSize(11).TextColor(pal.Muted)
	})
}
