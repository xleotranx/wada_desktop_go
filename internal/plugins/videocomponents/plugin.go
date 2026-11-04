// Package videocomponents contributes the first reusable video design system:
// shared typography, surfaces and motion, with an Excel tutorial collection.
package videocomponents

import (
	_ "embed"
	"github.com/xleotranx/wada-kit/kernel"
	kit "wada-desktop-go/internal/contract/videokit"
)

//go:embed assets/gsap-3.15.0.min.js
var gsapJS string

//go:embed GSAP-LICENSE
var gsapNotice string

type Plugin struct{}

func New() kernel.Plugin { return &Plugin{} }
func (*Plugin) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "video-components", Title: "Video components", Description: "Reusable scenes and Excel tutorial starters."}
}
func (*Plugin) Setup(c *kernel.Context) error {
	all := excelCollection()
	foundation := kit.Collection{Ref: ref("wada.foundation"), Title: "Video foundations", CSS: primitivesCSS, RuntimeURL: "assets/gsap-3.15.0.min.js", Assets: map[string]string{"assets/gsap-3.15.0.min.js": gsapJS, "assets/GSAP-LICENSE.txt": gsapNotice}, Themes: all.Themes, Layouts: all.Layouts, Components: all.Components[:1]}
	all.Themes = nil
	all.Layouts = nil
	all.Components = all.Components[1:]
	if err := kernel.Contribute(c, kit.Collections, "video-components.foundation", 0, foundation); err != nil {
		return err
	}
	if err := kernel.Contribute(c, kit.Collections, "video-components.excel", 10, all); err != nil {
		return err
	}
	return kernel.Contribute(c, kit.Collections, "video-components.excel-demos", 20, excelDemos())
}

func ref(id string) kit.Ref { return kit.Ref{ID: id, Version: 1} }
func text(id, label, value string, limit int) kit.Field {
	return kit.Field{ID: id, Label: label, Type: "string", Default: value, MaxLength: limit}
}
func part(id, component string, inputs map[string]any) kit.Part {
	return kit.Part{ID: id, Component: ref(component), Inputs: inputs}
}
func scene(id, title, layout string, start, duration float64, parts ...kit.Part) kit.Scene {
	return kit.Scene{ID: id, Title: title, Layout: ref(layout), Start: start, Duration: duration, Parts: parts}
}

func excelCollection() kit.Collection {
	c := kit.Collection{Ref: ref("wada.excel"), Title: "Excel essentials"}
	c.Themes = []kit.Theme{{Ref: ref("wada.sheet"), Title: "Sheet green", Tokens: map[string]string{
		"background": "#f5f8f6", "surface": "#ffffff", "foreground": "#183628", "muted": "#61776a", "accent": "#217346", "line": "#dce8df", "highlight": "#d9efdf",
		"font": "Arial, sans-serif", "mono": "Menlo, Consolas, monospace", "radius": "24px", "space": "32px",
	}}}
	c.Layouts = []kit.Layout{
		{Ref: ref("wada.center"), Title: "Centered", Width: 1920, Height: 1080, MaxParts: 1, CSS: ".parts { display:grid; place-items:center; } .part { width:100%; max-width:1500px; text-align:center; }"},
		{Ref: ref("wada.split"), Title: "Side by side", Width: 1920, Height: 1080, MaxParts: 2, CSS: ".parts { display:grid; grid-template-columns:minmax(0,0.85fr) minmax(0,1.15fr); align-items:center; gap:64px; } .part h1 { font-size:64px; letter-spacing:-2px; } .part p { font-size:28px; }"},
	}
	enter := `tl.fromTo(box, {opacity:0,y:28}, {opacity:1,y:0,duration:0.5,ease:"power3.out",immediateRender:false}, cues.enter);`
	c.Components = []kit.Component{
		{Ref: ref("wada.title"), Title: "Title / takeaway", Description: "A headline, supporting explanation and small label.", Fields: []kit.Field{
			text("label", "Label", "EXCEL QUICK TIP", 40), text("headline", "Headline", "Work smarter in Excel", 90), text("body", "Explanation", "One useful shortcut. A clear example. A result you can use.", 180),
		}, Markup: `<div class="eyebrow" data-var-text="{{binding "label"}}">{{value "label"}}</div><h1 data-var-text="{{binding "headline"}}">{{value "headline"}}</h1><p data-var-text="{{binding "body"}}">{{value "body"}}</p>`, Motion: enter},
		{Ref: ref("excel.sheet"), Title: "Spreadsheet + highlight", Description: "A small spreadsheet with a highlighted result row. Separate cells with | and rows with ;.", Fields: []kit.Field{
			text("columns", "Column headers", "Region | Units | Revenue", 150), text("rows", "Rows (cells: |, rows: ;)", "East | 120 | $3,600;West | 245 | $7,350;North | 310 | $9,300;Total | 675 | $20,250", 700),
			{ID: "highlight", Label: "Highlight row (0 = none)", Type: "number", Default: float64(4), Min: number(0), Max: number(6)},
		}, Markup: `<div class="sheet"><div class="sheet-bar">▦ &nbsp; Worksheet</div><table><thead><tr>{{range split "columns" "|" 5 28}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range $i, $row := grid "rows" "columns" ";" "|" 6 5 28}}<tr class="{{if selected "highlight" $i}}result-row{{end}}">{{range $row}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table></div>`, Motion: enter + `tl.fromTo(box.querySelectorAll("tbody tr"), {opacity:0,x:20}, {opacity:1,x:0,duration:0.35,stagger:0.12,immediateRender:false}, cues.action);tl.fromTo(box.querySelectorAll(".result-row"), {backgroundColor:"transparent"}, {backgroundColor:getComputedStyle(root).getPropertyValue("--highlight").trim(),duration:0.4,immediateRender:false}, cues.reveal);`},
		{Ref: ref("excel.shortcut"), Title: "Keyboard shortcut", Description: "Keycaps with a deterministic keypress and explanation. Use the platform label to distinguish Windows and Mac.", Fields: []kit.Field{
			text("platform", "Platform", "Excel · Windows", 50), text("keys", "Keys (separate with +)", "Alt + =", 60), text("caption", "Explanation", "Sum the cells above", 100),
		}, Markup: `<div class="eyebrow" data-var-text="{{binding "platform"}}">{{value "platform"}}</div><div class="keys">{{range split "keys" "+" 4 12}}<kbd>{{.}}</kbd>{{end}}</div><p data-var-text="{{binding "caption"}}">{{value "caption"}}</p>`, Motion: enter + `tl.fromTo(box.querySelectorAll("kbd"), {y:0}, {y:12,duration:0.12,yoyo:true,repeat:1,stagger:0.08,immediateRender:false}, cues.action);`},
		{Ref: ref("excel.formula"), Title: "Formula + result", Description: "A formula reveal followed by its result. Supply a verified formula and expected result.", Fields: []kit.Field{
			text("label", "Label", "FORMULA", 40), text("formula", "Formula", "=SUM(A1:A5)", 100), text("result", "Expected result", "1,270", 80), text("caption", "Explanation", "Add a column in one step", 140),
		}, Markup: `<div class="eyebrow" data-var-text="{{binding "label"}}">{{value "label"}}</div><div class="formula"><span class="fx">fx</span><code data-var-text="{{binding "formula"}}">{{value "formula"}}</code></div><div class="answer" data-var-text="{{binding "result"}}">{{value "result"}}</div><p data-var-text="{{binding "caption"}}">{{value "caption"}}</p>`, Motion: enter + `tl.fromTo(box.querySelector("code"), {clipPath:"inset(0 100% 0 0)"}, {clipPath:"inset(0 0% 0 0)",duration:1,ease:"steps(16)",immediateRender:false}, cues.action);tl.fromTo(box.querySelector(".answer"), {opacity:0,scale:0.94}, {opacity:1,scale:1,duration:0.4,immediateRender:false}, cues.reveal);`},
	}
	c.Templates = []kit.Template{
		{Ref: ref("excel.autosum"), Title: "One Excel tip · AutoSum", Description: "A shortcut, a five-row example and a formula result. 20 seconds · landscape.", Theme: ref("wada.sheet"), Scenes: []kit.Scene{
			scene("intro", "Sum a column instantly", "wada.center", 0, 3, part("title", "wada.title", map[string]any{"headline": "Sum a column instantly", "body": "An Excel shortcut you can use today."})),
			scene("demo", "Press Alt + =", "wada.split", 3, 7, part("shortcut", "excel.shortcut", nil), part("sheet", "excel.sheet", map[string]any{"columns": "Row | Amount", "rows": "1 | 120;2 | 245;3 | 310;4 | 415;5 | 180;6 | 1,270", "highlight": float64(6)})),
			scene("formula", "See the formula", "wada.center", 10, 6, part("formula", "excel.formula", nil)),
			scene("outro", "Try it yourself", "wada.center", 16, 4, part("title", "wada.title", map[string]any{"label": "YOUR NEXT STEP", "headline": "Select the cell below your numbers", "body": "Press Alt + =, check the range, then press Enter. Excel for Windows."})),
		}},
		{Ref: ref("excel.roundup"), Title: "Excel tips roundup", Description: "A table shortcut and a formula tip, assembled from the same components. 24 seconds · landscape.", Theme: ref("wada.sheet"), Scenes: []kit.Scene{
			scene("intro", "Two Excel time savers", "wada.center", 0, 3, part("title", "wada.title", map[string]any{"headline": "Two Excel time savers", "body": "Turn data into a table, then calculate a total."})),
			scene("table", "Turn a range into a table", "wada.split", 3, 8, part("shortcut", "excel.shortcut", map[string]any{"keys": "Ctrl + T", "caption": "Select your data, press Ctrl + T, confirm the range and headers."}), part("sheet", "excel.sheet", nil)),
			scene("total", "Add up the numbers", "wada.split", 11, 8, part("title", "wada.title", map[string]any{"label": "TIP 02", "headline": "Let a formula do the work", "body": "SUM adds the values in the selected range."}), part("formula", "excel.formula", nil)),
			scene("outro", "Keep learning", "wada.center", 19, 5, part("title", "wada.title", map[string]any{"label": "SAVE THESE TIPS", "headline": "Less repetition. More progress.", "body": "Try these examples in Excel for Windows."})),
		}},
	}
	return c
}
func number(v float64) *float64 { return &v }
