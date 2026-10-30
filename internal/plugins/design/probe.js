// Runs after `const x, y` as the body of an async function in an artboard:
// describes the element at x, y of the page, in its own pixels.
const el = document.elementFromPoint(x, y);
if (!el || el === document.documentElement) return null;

// selector names el from its nearest ancestor with an ID, or from <body>.
const parts = [];
for (let n = el; n && n.nodeType === 1 && n !== document.documentElement; n = n.parentElement) {
  if (n.id) { parts.unshift("#" + CSS.escape(n.id)); break; }
  let part = n.localName;
  const same = n.parentElement ? [...n.parentElement.children].filter((s) => s.localName === n.localName) : [];
  if (same.length > 1) part += ":nth-of-type(" + (same.indexOf(n) + 1) + ")";
  parts.unshift(part);
  if (n.localName === "body") break;
}
const cut = (s, n) => (s.length > n ? s.slice(0, n) + "…" : s);
const bounds = el.getBoundingClientRect();
return {
  x: bounds.x, y: bounds.y, w: bounds.width, h: bounds.height,
  selector: parts.join(" > "),
  text: cut((el.innerText || "").replace(/\s+/g, " ").trim(), 80),
  html: cut(el.outerHTML.replace(/\s+/g, " "), 600),
};
