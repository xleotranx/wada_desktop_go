package main

import (
	"github.com/egoist/mygo/ui"
)

// stroke wraps inline SVG paths in a 24x24 Lucide-style icon document.
func stroke(body string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`)
}

// filled wraps single-color filled shapes.
func filled(body string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none">` + body + `</svg>`)
}

// MyGo re-colors every icon by the surrounding text color, so each file
// stays single-color.
var (
	icBack      = ui.MustParseSVG(stroke(`<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>`))
	icForward   = ui.MustParseSVG(stroke(`<path d="M5 12h14"/><path d="m12 5 7 7-7 7"/>`))
	icPanel     = ui.MustParseSVG(stroke(`<rect width="18" height="18" x="3" y="3" rx="2.5"/><path d="M9.5 3v18"/>`))
	icHome      = ui.MustParseSVG(stroke(`<path d="m3 10 9-7 9 7"/><path d="M5 9v11h14V9"/><path d="M9 20v-6h6v6"/>`))
	icStack     = ui.MustParseSVG(stroke(`<path d="m12 3 9 5-9 5-9-5 9-5Z"/><path d="m3 12 9 5 9-5"/><path d="m3 16 9 5 9-5"/>`))
	icHistory   = ui.MustParseSVG(stroke(`<path d="M3 12a9 9 0 1 0 2.64-6.36L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l3 2"/>`))
	icAgent     = ui.MustParseSVG(stroke(`<path d="M12 3 13.8 9.2 20 11l-6.2 1.8L12 19l-1.8-6.2L4 11l6.2-1.8L12 3Z"/><path d="m19 16 .8 2.2L22 19l-2.2.8L19 22l-.8-2.2L16 19l2.2-.8L19 16Z"/>`))
	icGitBranch = ui.MustParseSVG(stroke(`<circle cx="6" cy="4" r="2"/><circle cx="6" cy="20" r="2"/><circle cx="18" cy="6" r="2"/><path d="M6 6v12"/><path d="M18 8a6 6 0 0 1-6 6H6"/>`))
	icGrid      = ui.MustParseSVG(stroke(`<rect x="3" y="3" width="8" height="8" rx="2"/><rect x="13" y="3" width="8" height="5" rx="2"/><rect x="13" y="10" width="8" height="11" rx="2"/><rect x="3" y="13" width="8" height="8" rx="2"/>`))
	icClose     = ui.MustParseSVG(stroke(`<path d="m18 6-12 12M6 6l12 12"/>`))
	icGlobe     = ui.MustParseSVG(stroke(`<circle cx="12" cy="12" r="10"/><path d="M2 12h20"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10Z"/>`))
	icMessage   = ui.MustParseSVG(stroke(`<path d="M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8v.5Z"/>`))

	icBell   = ui.MustParseSVG(stroke(`<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>`))
	icSearch = ui.MustParseSVG(stroke(`<circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/>`))
	icPen    = ui.MustParseSVG(stroke(`<path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.4 2.6a2.1 2.1 0 0 1 3 3L12 15l-4 1 1-4Z"/>`))

	icFolder  = ui.MustParseSVG(filled(`<path fill="currentColor" d="M2.5 6.6c0-1.2.97-2.17 2.17-2.17h3.9c.63 0 1.22.27 1.63.74l.9 1.03c.22.25.54.4.87.4h5.36c1.2 0 2.17.97 2.17 2.17v8.86a2.42 2.42 0 0 1-2.42 2.42H4.92a2.42 2.42 0 0 1-2.42-2.42z"/>`))
	icFileDoc = ui.MustParseSVG(stroke(`<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/>`))

	icTerminal = ui.MustParseSVG(stroke(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="m7 11 2-2-2-2"/><path d="M11 13h6"/>`))
	icBook     = ui.MustParseSVG(stroke(`<path d="M2 3.5h6a4 4 0 0 1 4 4V21a3 3 0 0 0-3-3H2Z"/><path d="M22 3.5h-6a4 4 0 0 0-4 4V21a3 3 0 0 1 3-3h7Z"/>`))
	icImage    = ui.MustParseSVG(stroke(`<rect width="18" height="18" x="3" y="3" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.1-3.1a2 2 0 0 0-2.8 0L6 21"/>`))
	icPlug     = ui.MustParseSVG(stroke(`<path d="M12 22v-5"/><path d="M9 8V2"/><path d="M15 8V2"/><path d="M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z"/>`))

	icChevRight = ui.MustParseSVG(stroke(`<path d="m9 18 6-6-6-6"/>`))
	icChevDown  = ui.MustParseSVG(stroke(`<path d="m6 9 6 6 6-6"/>`))
	icChevUD    = ui.MustParseSVG(stroke(`<path d="m7 15 5 5 5-5"/><path d="m7 9 5-5 5 5"/>`))

	icEllipsis = ui.MustParseSVG(stroke(`<circle cx="5" cy="12" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="19" cy="12" r="1.6"/>`))
	icTrash    = ui.MustParseSVG(stroke(`<path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M10 11v6"/><path d="M14 11v6"/>`))
	icCheck    = ui.MustParseSVG(stroke(`<path d="m5 13 4 4 10-10"/>`))
	icTasks    = ui.MustParseSVG(stroke(`<path d="m3 17 2 2 4-4"/><path d="m3 7 2 2 4-4"/><path d="M13 6h8"/><path d="M13 12h8"/><path d="M13 18h8"/>`))
	icLayout   = ui.MustParseSVG(stroke(`<rect width="18" height="18" x="3" y="3" rx="2.5"/><path d="M12 3v18"/><path d="M15 12h6"/>`))
	icPlus     = ui.MustParseSVG(stroke(`<path d="M5 12h14"/><path d="M12 5v14"/>`))

	// The check is a hole in the filled shield (fill-rule evenodd).
	icShield = ui.MustParseSVG(filled(`<path fill-rule="evenodd" clip-rule="evenodd" fill="currentColor" d="M12 2.8 18.6 5.3v5.2c0 4.5-3 7.5-6.6 9-3.6-1.5-6.6-4.5-6.6-9V5.3ZM11.2 17 16.3 11.9 14.8 10.4 11.2 14 9.3 12.1 7.8 13.6Z"/>`))
	icMic    = ui.MustParseSVG(stroke(`<path d="M12 2a3 3 0 0 1 3 3v7a3 3 0 0 1-6 0V5a3 3 0 0 1 3-3Z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><path d="M12 19v3"/>`))
	icWaves  = ui.MustParseSVG(stroke(`<path d="M2 10v4"/><path d="M6 6v12"/><path d="M10 3v18"/><path d="M14 8v8"/><path d="M18 5v14"/><path d="M22 10v4"/>`))
	icLoad   = ui.MustParseSVG(stroke(`<path d="M3 12a9 9 0 1 0 2.6-6.4L3 8"/><path d="M3 3v5h5"/>`))
	icSend   = ui.MustParseSVG(stroke(`<path d="m5 12 7-7 7 7"/><path d="M12 19V5"/>`))
	icStop   = ui.MustParseSVG(filled(`<rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor"/>`))
)

// icBooks is the colorful stack of books beside Homework. It keeps its own
// colors, so it shows with Image, not Icon.
var icBooks = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">
  <path d="M2 6.4c0-1 .8-1.8 1.8-1.8h4.4c1 0 1.8.8 1.8 1.8v12.2c0 .7-.6 1.3-1.3 1.3H3.3C2.6 19.9 2 19.3 2 18.6z" fill="#f0b23e"/>
  <rect x="3.4" y="7.4" width="5.2" height="1.7" rx="0.85" fill="#ffffff" opacity="0.75"/>
  <path d="M10 5c0-1 .8-1.8 1.8-1.8h4.6c1 0 1.8.8 1.8 1.8v13.6c0 .7-.6 1.3-1.3 1.3h-5.6c-.7 0-1.3-.6-1.3-1.3z" fill="#3f83f8"/>
  <rect x="11.5" y="6.2" width="5.2" height="1.7" rx="0.85" fill="#ffffff" opacity="0.7"/>
  <g transform="rotate(9 19.6 12)">
    <rect x="16.9" y="3.4" width="3.4" height="16.6" rx="1" fill="#e9564f"/>
    <rect x="17.6" y="5.2" width="2" height="1.4" rx="0.7" fill="#ffffff" opacity="0.7"/>
  </g>
</svg>`))
