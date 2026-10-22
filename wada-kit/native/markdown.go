package native

import "github.com/egoist/mygo/ui/markdown"

// MarkdownTheme dresses rendered markdown in the palette's colors.
func MarkdownTheme(pal Palette) markdown.Theme {
	return markdown.Theme{
		FontSize: 14, Text: pal.Text, Muted: pal.Muted, Link: pal.Link, Strong: pal.Text, Code: pal.Text,
		CodeBackground: pal.DetailBg, CodeBlockBackground: pal.DetailBg, CodeBlockBorder: pal.Hairline,
		QuoteBar: pal.Hairline, QuoteText: pal.Muted, Rule: pal.Hairline, TableBorder: pal.Hairline,
	}
}
