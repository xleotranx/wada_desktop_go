package videocomponents

import (
	"strings"
	kit "wada-desktop-go/internal/contract/videokit"
)

// New demonstrations carry their own styles and motion. Importing one into an
// older project does not replace that project's foundation, theme or definitions.
const demoEnter = `tl.fromTo(box, {opacity:0,y:24}, {opacity:1,y:0,duration:0.45,ease:"power3.out",immediateRender:false}, cues.enter);`
const clickMotion = `tl.fromTo(box.querySelector(".demo-cursor"), {opacity:0,x:-60,y:-40}, {opacity:1,x:0,y:0,duration:0.6,ease:"power2.out",immediateRender:false}, cues.action);tl.fromTo(box.querySelector(".demo-click"), {scale:0.3,opacity:0}, {scale:1.8,opacity:0.6,duration:0.3,yoyo:true,repeat:1,immediateRender:false}, cues.action+0.5);`
const cursorMarkup = `<div class="demo-cursor" aria-hidden="true"><svg viewBox="0 0 34 46" width="40" height="58" fill="currentColor"><path d="M2 2v34l9-9 8 16 7-4-8-15h13z" stroke="var(--surface)" stroke-width="2"/></svg><span class="demo-click"></span></div>`

func bounded(id, label string, value, min, max, step float64) kit.Field {
	return kit.Field{ID: id, Label: label, Type: "number", Default: value, Min: number(min), Max: number(max), Step: step}
}
func demo(id, title, group, description string, fields []kit.Field, markup, motion string) kit.Component {
	return kit.Component{Ref: ref(id), Title: title, Group: group, Description: description, Layouts: []kit.Ref{ref("wada.center")}, DefaultDuration: 8, Fields: fields, Markup: markup, CSS: excelDemoCSS, Motion: demoEnter + motion}
}

// transitionDemo is a reusable action/result primitive. Both states are authored
// data, never a simulated spreadsheet engine. Content is available before seeking.
func transitionDemo(id, title, action, columns, before, after, caption, motion string) kit.Component {
	fields := []kit.Field{
		text("action", "Action label", action, 65), text("columns", "Headers (cells: |)", columns, 120),
		text("before", "Before (cells: |, rows: ;)", before, 650), text("after", "After (cells: |, rows: ;)", after, 650),
		text("caption", "Explanation", caption, 160),
		{ID: "transition", Label: "Result animation", Type: "enum", Default: "wipe", Options: []string{"wipe", "slide", "zoom"}},
	}
	for i := range fields {
		if fields[i].ID == "transition" {
			if id == "excel.sort" || id == "excel.autofill" {
				fields[i].Default = "slide"
			}
			if id == "excel.duplicates" {
				fields[i].Default = "zoom"
			}
		}
	}
	markup := `<div class="demo-action">{{value "action"}}</div><div class="demo-comparison" data-effect="{{value "transition"}}"><div class="demo-pane demo-before"><div class="demo-label">Before</div><table><thead><tr>{{range split "columns" "|" 3 20}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range grid "before" "columns" ";" "|" 5 3 20}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table></div><div class="demo-arrow">→</div><div class="demo-pane demo-after"><div class="demo-label">After</div><table><thead><tr>{{range split "columns" "|" 3 20}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range grid "after" "columns" ";" "|" 5 3 20}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table></div>` + cursorMarkup + `</div><p>{{value "caption"}}</p>`
	return demo(id, title, "Clean & organize", "An animated before/action/after example. Supply both states; the component does not execute Excel commands.", fields, markup, clickMotion+motion+`{const result=box.querySelector(".demo-after");const effect=box.querySelector(".demo-comparison").getAttribute("data-effect");const from={opacity:0,x:0,scale:1,clipPath:"inset(0 0% 0 0)"};if(effect==="wipe")from.clipPath="inset(0 100% 0 0)";if(effect==="slide")from.x=45;if(effect==="zoom")from.scale=0.9;tl.fromTo(result,from,{opacity:1,x:0,scale:1,clipPath:"inset(0 0% 0 0)",duration:0.65,ease:"power2.out",immediateRender:false},cues.reveal);}tl.fromTo(box.querySelectorAll(".demo-after tbody tr"), {opacity:0,x:18}, {opacity:1,x:0,duration:0.35,stagger:0.09,immediateRender:false}, cues.reveal+0.1);`)
}

func excelDemos() kit.Collection {
	c := kit.Collection{Ref: ref("wada.excel-demos"), Title: "Excel actions & animation"}
	rangeFields := []kit.Field{text("columns", "Column labels", "A | B | C", 100), text("rows", "Rows (cells: |, rows: ;)", "Item | Units | Total;Pens | 12 | 36;Books | 8 | 64;Folders | 15 | 45", 600), bounded("row-from", "First row", 2, 1, 6, 1), bounded("row-to", "Last row", 4, 1, 6, 1), bounded("column-from", "First column", 2, 1, 4, 1), bounded("column-to", "Last column", 3, 1, 4, 1), text("caption", "Explanation", "Select a cell, a row, or a rectangular range before applying an action.", 160)}
	rangeMarkup := `{{$columns:=split "columns" "|" 4 20}}{{$rows:=grid "rows" "columns" ";" "|" 6 4 20}}<div class="demo-grid" style="--column:{{number "column-from" 1 4}};--columns:{{len $columns}};--row:{{number "row-from" 1 6}}"><table><thead><tr><th class="demo-row-number">#</th>{{range $columns}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range $r,$row := $rows}}<tr><th class="demo-row-number">{{inc $r}}</th>{{range $col,$cell := $row}}<td class="{{if and (within "row-from" "row-to" $r (len $rows)) (within "column-from" "column-to" $col (len $columns))}}demo-selection{{end}}">{{$cell}}</td>{{end}}</tr>{{end}}</tbody></table>` + cursorMarkup + `</div><p>{{value "caption"}}</p>`
	c.Components = append(c.Components, demo("excel.range", "Cell / range selection", "Data entry", "A cursor click followed by a highlighted rectangular range. Row and column indices start at 1.", rangeFields, rangeMarkup, clickMotion+`tl.fromTo(box.querySelectorAll(".demo-selection"), {backgroundColor:"transparent"}, {backgroundColor:getComputedStyle(root).getPropertyValue("--highlight").trim(),duration:0.4,stagger:0.025,immediateRender:false}, cues.reveal);`))
	fill := transitionDemo("excel.autofill", "AutoFill / fill handle", "Drag the fill handle to extend a sequence", "Month | Target", "Jan | 100;Feb | 110;Mar | ;Apr | ", "Jan | 100;Feb | 110;Mar | 120;Apr | 130", "Select the starting values, then drag the fill handle down. The example uses a step of 10.", `tl.fromTo(box.querySelector(".demo-cursor"), {y:0}, {y:160,duration:1,ease:"power1.inOut",immediateRender:false}, cues.action+0.65);`)
	fill.Markup = strings.Replace(fill.Markup, `<div class="demo-label">Before</div>`, `<span class="demo-fill-handle" aria-hidden="true"></span><div class="demo-label">Before</div>`, 1)
	fill.Motion += `tl.fromTo(box.querySelector(".demo-fill-handle"),{scale:1},{scale:1.5,duration:0.2,yoyo:true,repeat:1,immediateRender:false},cues.action+0.5);`
	fill.Group = "Data entry"
	c.Components = append(c.Components, fill)
	c.Components = append(c.Components, transitionDemo("excel.flashfill", "Flash Fill / text pattern", "Give one example, then use Flash Fill", "Name | First name", "Ada Lovelace | Ada;Grace Hopper | ;Alan Turing | ", "Ada Lovelace | Ada;Grace Hopper | Grace;Alan Turing | Alan", "Enter the first name as an example. Flash Fill follows the pattern; review its suggestions.", ""))
	filter := transitionDemo("excel.filter", "Filter / keep matching rows", "Filter Region to East", "Region | Units", "East | 12;West | 8;East | 15;North | 6", "East | 12;East | 15", "Open the column filter and choose the value to keep. Other rows are hidden.", `tl.fromTo(box.querySelector(".demo-action"), {scale:1}, {scale:1.03,duration:0.2,yoyo:true,repeat:1,immediateRender:false}, cues.action+0.5);`)
	filter.CSS += `.demo-comparison {padding-top:170px} .demo-filter-menu {top:8px;left:calc(50% - 105px)} .demo-filter-menu div {padding:8px 20px}`
	filter.Fields = append(filter.Fields, text("choices", "Filter choices (separate with |)", "East | West | North", 100))
	filter.Markup = strings.Replace(filter.Markup, cursorMarkup, `<div class="demo-filter-menu">{{range split "choices" "|" 4 18}}<div>{{.}}</div>{{end}}</div>`+cursorMarkup, 1)
	filter.Motion += `tl.fromTo(box.querySelector(".demo-filter-menu"),{opacity:0,y:-12},{opacity:1,y:0,duration:0.25,immediateRender:false},cues.action+0.25);tl.fromTo(box.querySelector(".demo-filter-menu"),{opacity:1},{opacity:0,duration:0.2,immediateRender:false},cues.reveal-0.3);`
	c.Components = append(c.Components, filter)
	c.Components = append(c.Components, transitionDemo("excel.sort", "Sort / reorder rows", "Sort Amount from smallest to largest", "Item | Amount", "Pens | 36;Books | 64;Folders | 45", "Pens | 36;Folders | 45;Books | 64", "Keep each row together when sorting. The item labels move with their amounts.", `{const before=Array.from(box.querySelectorAll(".demo-before tbody tr"));const after=Array.from(box.querySelectorAll(".demo-after tbody tr"));const key=row=>JSON.stringify(Array.from(row.querySelectorAll("td")).map(cell=>cell.textContent.trim()));before.forEach((row,i)=>{const target=after.findIndex(item=>key(item)===key(row));if(target>=0)tl.fromTo(row,{y:0},{y:(target-i)*(row.offsetHeight||64),duration:0.8,ease:"power2.inOut",immediateRender:false},cues.action+0.6);if(target>=0)tl.fromTo(row,{y:(target-i)*(row.offsetHeight||64)},{y:0,duration:0.3,immediateRender:false},cues.reveal+0.7);});}`))
	duplicates := transitionDemo("excel.duplicates", "Remove duplicates", "Remove repeated email addresses", "Name | Email", "Ada | ada@example.test;Grace | grace@example.test;Ada | ada@example.test;Alan | alan@example.test", "Ada | ada@example.test;Grace | grace@example.test;Alan | alan@example.test", "Choose which columns define a duplicate. Keep a copy of the source before removing rows.", `{const seen=new Set();const duplicates=Array.from(box.querySelectorAll(".demo-before tbody tr")).filter(row=>{const key=JSON.stringify(Array.from(row.querySelectorAll("td")).map(cell=>cell.textContent.trim()));if(seen.has(key))return true;seen.add(key);return false;});tl.fromTo(duplicates,{opacity:1,x:0},{opacity:0.15,x:0,duration:0.6,immediateRender:false},cues.action+0.6);tl.fromTo(duplicates,{opacity:0.15,x:0},{opacity:1,x:0,duration:0.3,immediateRender:false},cues.reveal+0.7);}`)
	c.Components = append(c.Components, duplicates)
	c.Components = append(c.Components, transitionDemo("excel.text-columns", "Text to Columns / split", "Split each code at the hyphen", "Code | Prefix | Number", "AB-101 |  | ;CD-202 |  | ;EF-303 |  | ", "AB-101 | AB | 101;CD-202 | CD | 202;EF-303 | EF | 303", "Choose the delimiter and a destination with empty columns so existing data is not overwritten.", ""))
	lookupFields := []kit.Field{text("columns", "Lookup headers", "SKU | Product | Price", 120), text("rows", "Lookup rows", "P-101 | Notebook | $12;P-102 | Pen set | $8;P-103 | Desk lamp | $20", 600), bounded("match", "Matched row", 2, 1, 6, 1), text("query", "Lookup value", "P-102", 45), text("formula", "Formula", "=XLOOKUP(E2,A2:A4,C2:C4)", 110), text("result", "Expected result", "$8", 60), text("caption", "Explanation", "Find the exact SKU and return the price from the same row.", 160)}
	lookupMarkup := `<div class="demo-lookup"><div class="demo-query"><span class="demo-label">Lookup value</span><strong>{{value "query"}}</strong><code>{{value "formula"}}</code></div><div class="demo-pane"><table><thead><tr>{{range split "columns" "|" 3 20}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range $i,$row:=grid "rows" "columns" ";" "|" 6 3 20}}<tr class="{{if selected "match" $i}}demo-match{{end}}">{{range $row}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table></div></div><div class="demo-result">{{value "result"}}</div><p>{{value "caption"}}</p>`
	c.Components = append(c.Components, demo("excel.lookup", "Lookup / trace a match", "Formulas", "Follow a lookup value to a matching row and reveal the authored result. Formula text is illustrative, not evaluated.", lookupFields, lookupMarkup, `tl.fromTo(box.querySelectorAll(".demo-match td"), {backgroundColor:"transparent"}, {backgroundColor:getComputedStyle(root).getPropertyValue("--highlight").trim(),duration:0.35,stagger:0.15,immediateRender:false}, cues.action);tl.fromTo(box.querySelector(".demo-result"), {opacity:0,scale:0.9}, {opacity:1,scale:1,duration:0.4,immediateRender:false}, cues.reveal);`))
	seriesFields := []kit.Field{text("labels", "Labels (separate with |)", "East | West | North | South", 130), text("values", "Percentages (separate with |)", "36 | 82 | 64 | 95", 120), bounded("threshold", "Highlight at or above (%)", 75, 0, 100, 1), text("caption", "Explanation", "Data bars show progress; the rule emphasizes values meeting the target.", 160)}
	conditionalMarkup := `<div class="demo-action">Target: {{number "threshold" 0 100}}% or above</div><div class="demo-bars">{{range series "labels" "values" "|" 5 0 100}}<div class="demo-bar-row"><span>{{.Label}}</span><div class="demo-track"><div class="demo-bar {{if ge .Value (number "threshold" 0 100)}}demo-pass{{end}}" style="width:{{.Value}}%"></div></div><strong>{{.Value}}%</strong></div>{{end}}</div><p>{{value "caption"}}</p>`
	c.Components = append(c.Components, demo("excel.conditional", "Conditional formatting / data bars", "Visualize", "Reveal bounded percentage data bars, then emphasize values meeting the chosen threshold.", seriesFields, conditionalMarkup, `tl.fromTo(box.querySelectorAll(".demo-bar"), {scaleX:0}, {scaleX:1,duration:0.8,stagger:0.12,ease:"power2.out",immediateRender:false}, cues.action);tl.fromTo(box.querySelectorAll(".demo-pass"), {opacity:0.45}, {opacity:1,duration:0.3,yoyo:true,repeat:2,immediateRender:false}, cues.reveal);`))
	chartFields := []kit.Field{text("labels", "Category labels", "Jan | Feb | Mar | Apr", 130), text("values", "Percentages (0–100)", "35 | 50 | 68 | 85", 120), text("caption", "Explanation", "Progress toward the monthly target (%)", 160)}
	chartMarkup := `<div class="demo-chart">{{range series "labels" "values" "|" 5 0 100}}<div class="demo-chart-column"><div class="demo-chart-plot"><div class="demo-chart-bar" style="height:{{.Value}}%"><strong>{{.Value}}%</strong></div></div><span>{{.Label}}</span></div>{{end}}</div><p>{{value "caption"}}</p>`
	c.Components = append(c.Components, demo("excel.chart", "Column chart / grow bars", "Visualize", "A small percentage chart with staggered growth and data labels. All percentages are bounded to 0–100.", chartFields, chartMarkup, `tl.fromTo(box.querySelectorAll(".demo-chart-bar"), {scaleY:0}, {scaleY:1,duration:1,stagger:0.14,ease:"power3.out",immediateRender:false}, cues.action);tl.fromTo(box.querySelectorAll(".demo-chart-bar strong"), {opacity:0}, {opacity:1,duration:0.3,stagger:0.1,immediateRender:false}, cues.reveal);`))
	c.Templates = []kit.Template{
		{Ref: ref("excel.daily-actions"), Title: "Four daily Excel actions", Description: "Select a range, extend a sequence, filter rows and remove duplicates. 42 seconds · landscape.", Theme: ref("wada.sheet"), Scenes: []kit.Scene{
			scene("intro", "Four daily Excel actions", "wada.center", 0, 3, part("title", "wada.title", map[string]any{"headline": "Four everyday Excel actions", "body": "See the selection, action and result."})),
			scene("select", "1 · Select the data", "wada.center", 3, 8, part("demo", "excel.range", nil)),
			scene("fill", "2 · Extend a sequence", "wada.center", 11, 8, part("demo", "excel.autofill", nil)),
			scene("filter", "3 · Keep matching rows", "wada.center", 19, 9, part("demo", "excel.filter", nil)),
			scene("clean", "4 · Remove duplicates", "wada.center", 28, 9, part("demo", "excel.duplicates", nil)),
			scene("outro", "Try one on your next worksheet", "wada.center", 37, 5, part("title", "wada.title", map[string]any{"label": "PRACTICE", "headline": "Try one action at a time", "body": "Check the source data and review the result before applying a change."})),
		}},
		{Ref: ref("excel.lookup-visualize"), Title: "Lookup, highlight and chart", Description: "Trace a lookup, highlight target percentages and build a chart. 32 seconds · landscape.", Theme: ref("wada.sheet"), Scenes: []kit.Scene{
			scene("intro", "From data to an answer", "wada.center", 0, 3, part("title", "wada.title", map[string]any{"headline": "Make your spreadsheet explain itself", "body": "A lookup, conditional formatting and a simple chart."})),
			scene("lookup", "1 · Follow the matching row", "wada.center", 3, 9, part("demo", "excel.lookup", nil)),
			scene("highlight", "2 · Highlight the target", "wada.center", 12, 8, part("demo", "excel.conditional", nil)),
			scene("chart", "3 · Show progress", "wada.center", 20, 8, part("demo", "excel.chart", nil)),
			scene("outro", "Read the result", "wada.center", 28, 4, part("title", "wada.title", map[string]any{"label": "TAKEAWAY", "headline": "Show how you reached the answer", "body": "Use a clear match, a meaningful threshold and labelled chart values."})),
		}},
	}
	return c
}
