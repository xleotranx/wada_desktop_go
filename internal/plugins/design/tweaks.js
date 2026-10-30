// Runs after `const file` as the body of an async function in an artboard:
// null until the page shows file and has loaded, then the tweaks it
// declares in <script type="application/json" id="tweaks">, each with its
// value now.
if (document.readyState !== "complete" || !decodeURIComponent(location.pathname).endsWith("/" + file)) return null;
const source = document.querySelector('script#tweaks[type="application/json"]');
if (!source) return [];
let list;
try { list = JSON.parse(source.textContent); } catch { return []; }
if (!Array.isArray(list)) return [];
const style = getComputedStyle(document.documentElement);
return list
  .filter((t) => t && typeof t.var === "string" && t.var.startsWith("--"))
  .map((t) => ({
    ...t,
    type: ["range", "color", "toggle", "select"].includes(t.type) ? t.type : "range",
    label: t.label || t.var.slice(2),
    value: style.getPropertyValue(t.var).trim() || String(t.value ?? ""),
  }));
