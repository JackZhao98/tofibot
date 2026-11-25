// Scrollbar-gutter guard (owner 2026-11-19). Every view that scrolls vertically must reserve its scrollbar at all times, so the inside
// width never changes when a scrollbar appears: the leaf scrollers are listed once in src/skin/base.css (`:where(...) { scrollbar-gutter:
// stable }`). This fails when a stylesheet rule sets `overflow` / `overflow-y` to `auto` or `scroll` (vertical, or both axes) on a selector
// that list does not cover and that is not in ALLOWED below.
// Why an allowlist and not a pure scan: CSS cannot select "elements that scroll", so the list is the contract, and the scan only checks that
// a new scroller joined it (or `.tb-scroll`). The allowlist holds the scrollers that draw no scrollbar or are not vertical scrollers.
// Usage: node scripts/test-scroll-gutter.mjs
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const walk = dir => readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? (entry.name === "vendor" || entry.name === "node_modules" ? [] : walk(join(dir, entry.name))) : entry.name.endsWith(".css") ? [join(dir, entry.name)] : []);
const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
const squash = value => value.replace(/\s+/g, " ").trim();

// Scrollers that need no gutter, each with the reason.
const ALLOWED = new Map([
  [".workspace .detail-pane.widget-column", "draws no scrollbar (scrollbar-width none)"],
  [".workspace .detail-pane.widget-column:has(.computer-detail.is-expanded)", "draws no scrollbar (scrollbar-width none)"],
  [".workspace .usage-agents", "horizontal row of agent buttons"],
  [".dev-tune-panel", "dev-build Tune panel, not shipped"],
  [".tb-settings-rail", "mobile variant is a horizontal strip with its scrollbar hidden"],
  [".bb-tabs .tb-tablist", "horizontal tab strip"],
  [".terminal-tabs", "horizontal tab strip"],
  [".detail-pane.context-panel", "scrolls through .detail-pane, which is listed"],
  [".workspace .detail-pane.context-panel", "scrolls through .detail-pane, which is listed"],
  [".desktop-floating-shell .desktop-presence-canvas>.computer-detail.is-expanded", "scrolls through .computer-detail, which is listed"],
]);

// The covered list: the selectors inside the :where(...) that carries `scrollbar-gutter: stable` in base.css.
const base = strip(readFileSync(join(src, "skin/base.css"), "utf8"));
const block = /:where\(([\s\S]*?)\)\s*\{\s*scrollbar-gutter:\s*stable/.exec(base);
if (!block) { console.error("scroll-gutter: FAIL (no `:where(...) { scrollbar-gutter: stable }` list in src/skin/base.css)"); process.exit(1); }
// Splits at the commas that are not inside (...) or [...].
const splitTop = text => { const out = []; let depth = 0, cur = ""; for (const ch of text) { if ("([".includes(ch)) depth++; if (")]".includes(ch)) depth--; if (ch === "," && depth === 0) { out.push(squash(cur)); cur = ""; } else cur += ch; } if (squash(cur)) out.push(squash(cur)); return out; };
const covered = splitTop(block[1]);

// The subject of a selector: its last compound, without pseudo-classes and pseudo-elements that do not change what it is.
const subject = selector => {
  let s = squash(selector).replace(/^:where\(([^()]*)\)$/, "$1").replace(/\s*([>+~])\s*/g, " $1 ");
  s = s.replace(/:(?:not|has|is|where)\((?:[^()]|\([^()]*\))*\)/g, "").replace(/::?[\w-]+/g, "");
  const parts = s.split(/\s+/).filter(part => part && !/^[>+~]$/.test(part));
  return parts[parts.length - 1] ?? "";
};
const coveredSubjects = new Set(covered.map(subject));
const allowedSubjects = new Set([...ALLOWED.keys()].map(key => squash(key)));

const offences = [];
for (const file of walk(src)) {
  const css = strip(readFileSync(file, "utf8"));
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const body = match[2];
    const declaration = /(?<![-\w])overflow(-y)?\s*:\s*([^;}]+)/.exec(body);
    if (!declaration) continue;
    const parts = declaration[2].trim().split(/\s+/);
    const scrolls = declaration[1] ? ["auto", "scroll"].includes(parts[0]) : parts.length === 1 ? ["auto", "scroll"].includes(parts[0]) : ["auto", "scroll"].includes(parts[1]);
    if (!scrolls) continue;
    for (const selector of splitTop(match[1])) {
      const text = squash(selector);
      if (!text || text.startsWith("@")) continue;
      const clean = text.replace(/^.*\{\s*/, "");
      if (allowedSubjects.has(clean) || coveredSubjects.has(subject(clean))) continue;
      offences.push(`${relative(src, file)}: ${clean}`);
    }
  }
}
if (offences.length) {
  console.error(`scroll-gutter: FAIL (${offences.length} scroller(s) the gutter list in src/skin/base.css does not cover; add the selector there, give it \`.tb-scroll\`, or allow it with a reason in scripts/test-scroll-gutter.mjs)`);
  for (const line of offences) console.error(`  ${line}`);
  process.exit(1);
}
console.log(`scroll-gutter: PASS (${covered.length} listed scroller selectors, ${ALLOWED.size} allowed without a gutter)`);
