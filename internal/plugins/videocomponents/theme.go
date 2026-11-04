package videocomponents

const primitivesCSS = `
.scene-heading {position:absolute;top:64px;left:100px;font:600 28px var(--font);color:var(--muted);letter-spacing:2px;text-transform:uppercase;max-width:1720px}
.parts {position:absolute;inset:170px 100px 110px;font-family:var(--font);color:var(--foreground)}
.part {min-width:0;overflow:hidden}
.eyebrow {font:700 28px var(--mono);letter-spacing:3px;color:var(--accent);margin-bottom:28px}
h1 {font-size:88px;line-height:1.08;letter-spacing:-3px;margin:0 0 32px;overflow-wrap:anywhere}
p {font-size:34px;line-height:1.5;color:var(--muted);margin:24px 0 0;overflow-wrap:anywhere}
.sheet {background:var(--surface);border:2px solid var(--line);border-radius:var(--radius);overflow:hidden;box-shadow:0 20px 60px color-mix(in srgb,var(--foreground) 8%,transparent);text-align:left}
.sheet-bar {padding:22px 28px;background:var(--accent);color:var(--surface);font:600 28px var(--font)}
table {width:100%;border-collapse:collapse;table-layout:fixed;font-size:28px}
th,td {padding:22px 18px;border-bottom:1px solid var(--line);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
th {color:var(--muted);font-size:28px;background:var(--background)}
.result-row {font-weight:700;color:var(--accent)}
.keys {display:flex;align-items:center;justify-content:center;flex-wrap:wrap;gap:24px;padding:24px 0}
kbd {display:inline-flex;align-items:center;justify-content:center;min-width:100px;max-width:320px;padding:25px;font:700 48px var(--mono);background:var(--surface);border:2px solid var(--line);border-radius:var(--radius);box-shadow:0 10px 0 var(--line)}
.formula {display:flex;align-items:center;gap:24px;padding:28px;background:var(--surface);border:2px solid var(--line);border-radius:var(--radius);text-align:left}
.fx {font:italic 32px var(--mono);color:var(--muted)}
code {display:block;font:500 34px var(--mono);overflow-wrap:anywhere}
.answer {font:700 80px var(--mono);color:var(--accent);margin-top:36px;overflow-wrap:anywhere}
`
