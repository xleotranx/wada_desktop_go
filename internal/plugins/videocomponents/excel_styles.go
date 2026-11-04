package videocomponents

const excelDemoCSS = `
.demo-action {display:inline-block;padding:16px 28px;margin-bottom:28px;border:2px solid var(--line);border-radius:16px;background:var(--surface);color:var(--accent);font:700 32px var(--font)}
.demo-comparison {position:relative;display:grid;grid-template-columns:minmax(0,1fr) 64px minmax(0,1fr);gap:24px;align-items:center;text-align:left}
.demo-pane {position:relative;background:var(--surface);border:2px solid var(--line);border-radius:18px;overflow:hidden;min-width:0}
.demo-label {display:block;padding:16px 20px;color:var(--muted);font:600 28px var(--font);background:var(--background)}
.demo-pane table,.demo-grid table {width:100%;border-collapse:collapse;table-layout:fixed;font:28px var(--font);line-height:1.2}
.demo-pane th,.demo-pane td,.demo-grid th,.demo-grid td {padding:16px 14px;border:1px solid var(--line);height:32px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.demo-pane th,.demo-grid th {font-weight:600;color:var(--muted);background:var(--background)}
.demo-arrow {font:600 48px var(--font);color:var(--accent);text-align:center}
.demo-after {opacity:0}
.demo-fill-handle {position:absolute;right:2px;bottom:2px;width:14px;height:14px;background:var(--accent);border:2px solid var(--surface);z-index:2}
.demo-filter-menu {position:absolute;left:34%;top:12px;width:210px;background:var(--surface);border:2px solid var(--accent);border-radius:12px;box-shadow:0 12px 28px color-mix(in srgb,var(--foreground) 15%,transparent);z-index:3;opacity:0}
.demo-filter-menu div {padding:15px 20px;font:28px var(--font);border-bottom:1px solid var(--line)}
.demo-filter-menu div:first-child {background:var(--highlight);color:var(--accent);font-weight:700}
.demo-cursor {position:absolute;left:38%;top:38%;font-size:50px;line-height:1;color:var(--foreground);opacity:0;pointer-events:none;z-index:2}
.demo-click {position:absolute;left:0;top:0;width:30px;height:30px;border:4px solid var(--accent);border-radius:50%;opacity:0}
.demo-grid {position:relative;background:var(--surface);border:2px solid var(--line);border-radius:18px;overflow:hidden;text-align:left}
.demo-grid .demo-cursor {left:calc(60px + (100% - 60px) / var(--columns) * (var(--column) - 0.5));top:calc(64px + (var(--row) - 0.5) * 65px)}
.demo-row-number {width:60px;box-sizing:border-box;text-align:center}
.demo-selection {color:var(--accent);font-weight:700}
.demo-lookup {display:grid;grid-template-columns:minmax(0,0.8fr) minmax(0,1.2fr);gap:36px;align-items:center;text-align:left}
.demo-query {padding:20px;min-width:0}
.demo-query .demo-label {padding:0;background:transparent}
.demo-query strong {display:block;font:700 54px var(--mono);color:var(--accent);margin:20px 0}
.demo-query code {font:28px var(--mono);white-space:normal;overflow-wrap:anywhere}
.demo-result {opacity:0;font:700 64px var(--mono);color:var(--accent);margin-top:24px}
.demo-bars {display:flex;flex-direction:column;gap:30px;padding:36px;background:var(--surface);border:2px solid var(--line);border-radius:18px}
.demo-bar-row {display:grid;grid-template-columns:160px minmax(0,1fr) 100px;gap:24px;align-items:center;font:32px var(--font);text-align:left}
.demo-track {height:42px;background:var(--background);border-radius:8px;overflow:hidden}
.demo-bar {height:100%;background:var(--muted);transform:scaleX(0);transform-origin:left center;border-radius:8px}
.demo-pass {background:var(--accent)}
.demo-chart {display:flex;align-items:stretch;justify-content:center;gap:40px;padding:60px 40px 0;background:var(--surface);border:2px solid var(--line);border-radius:18px}
.demo-chart-column {flex:1;max-width:230px;min-width:0}
.demo-chart-plot {height:300px;display:flex;align-items:flex-end;border-bottom:2px solid var(--line)}
.demo-chart-bar {position:relative;min-height:2px;width:100%;background:var(--accent);border-radius:12px 12px 0 0;transform:scaleY(0);transform-origin:center bottom}
.demo-chart-bar strong {position:absolute;top:-42px;left:0;right:0;font:700 30px var(--mono);color:var(--foreground);opacity:0}
.demo-chart-column > span {display:block;padding:22px 0;font:32px var(--font);overflow-wrap:anywhere}
`
