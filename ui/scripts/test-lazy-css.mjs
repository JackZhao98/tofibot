// Lazy chunk CSS must not restyle the whole page when it arrives. The browser restyles only the elements a new rule can
// match, so a class-scoped rule costs almost nothing while its panel is closed; but an @property registration or a custom
// property defined on :root/html restyles every element (2026-11-04: 6610 elements, 23-26 ms on a Mac, about 85 ms at 4x
// CPU, measured in the sidebar with 61 rows). The warm-up in lazyPanels.tsx loads these chunks while the person works, so
// such a rule there is a dropped frame mid-drag or mid-scroll. Put it in design-tokens.css or another eager sheet instead.
// Reads ui/dist: run `npm run build` first. Run: npm run test:lazy-css
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dist = join(dirname(dirname(fileURLToPath(import.meta.url))), "dist");
if (!existsSync(join(dist, "index.html"))) { console.error("lazy css: ui/dist/index.html not found; run `npm run build` first"); process.exit(2); }

// Eager sheets: the ones an entry page links. Everything else under assets/ is loaded with a chunk.
const eager = new Set();
for (const page of readdirSync(dist).filter(name => name.endsWith(".html"))) {
  for (const [, href] of readFileSync(join(dist, page), "utf8").matchAll(/<link[^>]+href="\/?assets\/([^"]+\.css)"/g)) eager.add(href);
}
const lazy = readdirSync(join(dist, "assets")).filter(name => name.endsWith(".css") && !eager.has(name));

/** Style rules as [selector, body] at any depth (inside @media, @supports, @layer), plus the at-rule preludes. */
function rules(css) {
  const out = [], atRules = [];
  css = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const walk = (text) => {
    let i = 0;
    while (i < text.length) {
      const open = text.indexOf("{", i);
      if (open < 0) break;
      const prelude = text.slice(i, open).replace(/^[\s;}]+/, "").trim();
      let depth = 1, j = open + 1;
      for (; j < text.length && depth; j++) { if (text[j] === "{") depth++; else if (text[j] === "}") depth--; }
      const body = text.slice(open + 1, j - 1);
      if (prelude.startsWith("@")) { atRules.push(prelude); if (/^@(media|supports|layer|container|scope)\b/.test(prelude)) walk(body); }
      else out.push([prelude, body]);
      i = j;
    }
  };
  walk(css);
  return { out, atRules };
}
const splitSelectors = selector => { const parts = []; let depth = 0, start = 0; for (let i = 0; i < selector.length; i++) { const c = selector[i]; if (c === "(" || c === "[") depth++; else if (c === ")" || c === "]") depth--; else if (c === "," && !depth) { parts.push(selector.slice(start, i)); start = i + 1; } } parts.push(selector.slice(start)); return parts.map(s => s.trim()); };
// The root element alone, however qualified: :root, html, :root[data-theme=dark], html:not([data-desktop]), :root:root.
const ROOT_ONLY = /^(?::root|html)(?:\[[^\]]*\]|:root|:not\([^)]*\)|:where\([^)]*\)|:is\([^)]*\))*$/;

const problems = [];
for (const file of lazy) {
  const { out, atRules } = rules(readFileSync(join(dist, "assets", file), "utf8"));
  for (const at of atRules) if (/^@property\b/.test(at)) problems.push(`${file}: ${at}`);
  for (const [selector, body] of out) {
    if (/(^|[;{\s])--[\w-]+\s*:/.test(body) && splitSelectors(selector).every(s => ROOT_ONLY.test(s))) problems.push(`${file}: ${selector} { ${body.slice(0, 60)}... }`);
  }
}
if (eager.size === 0) problems.push("no eager stylesheet found in the entry pages; the scan cannot tell lazy CSS apart");
if (problems.length) {
  console.error(`lazy css: FAIL. These rules restyle the whole page when their chunk loads; move them to design-tokens.css:\n  ${problems.join("\n  ")}`);
  process.exit(1);
}
console.log(`lazy css: ${lazy.length} lazy stylesheets, ${eager.size} eager; no @property or :root custom properties in lazy CSS PASS`);
