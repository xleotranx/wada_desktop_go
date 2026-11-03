package documents

import (
	"fmt"
	"path/filepath"

	"wada-desktop-go/internal/contract/documents"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/native"
)

// sources lists what the last folder read found, one row per file: its name,
// then its format and size or why it was not read.
func (p *Plugin) sources(c *ui.Context) {
	pal := native.PaletteFor(c)
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Text(c, summary(p.last)).FontSize(11).TextColor(pal.Faint).Padding(0, 0, 4, 0)
		for _, src := range p.last.Sources {
			ui.Column(c.Key(src.Path)).FillWidth().Gap(1).Padding(6, 4, 2, 4).Children(func() {
				ui.Text(c, filepath.Base(src.Path)).FontSize(12).TextColor(pal.Text)
				ui.Text(c, detail(src)).FontSize(11).TextColor(pal.Faint)
			})
		}
	})
}

// summary says how much of the folder was read.
func summary(res documents.Result) string {
	if len(res.Sources) == 0 {
		return "No documents in this folder"
	}
	return fmt.Sprintf("%d of %d files read · %s", res.Read, len(res.Sources), size(res.Chars))
}

// detail says what a row read, or why it did not.
func detail(src documents.Source) string {
	if src.Skip != "" {
		return src.Skip
	}
	if src.Format != "" {
		return fmt.Sprintf("%s · %s", src.Format, size(src.Chars))
	}
	return size(src.Chars)
}

// size writes a length in characters the short way.
func size(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM chars", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk chars", float64(n)/1000)
	default:
		return fmt.Sprintf("%d chars", n)
	}
}
