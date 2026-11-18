// Kit boundary guard (V3 PLAN 7.7), a ratchet. The V3 kit (src/ds/) owns how a .tb-* element looks and moves; any other
// sheet may only place it. Counts per file may fall, never rise; a new file starts at zero (baseline in
// scripts/ds-boundary-baseline.json). Kinds:
//   tbStyle      a declaration, in an UNLAYERED rule of a sheet outside src/ds/, on a selector that names a .tb-* class,
//                other than layout: display, grid-*, flex*, position, inset*, margin*, padding*, gap, width, height, min-*,
//                max-*, order, align-*, justify-*, place-*, overflow*, and z-index: var(--z-*). The three owner-kept motion
//                sheets (boot-screen.css, permission-coach.css, copy-feedback.css) may also set transform, opacity and
//                animation* from tokens (every duration and easing a var()). Rules in a layer (legacy, skin, ...) lose to the
//                kit anyway and are not counted. Sheets in src/skin/ are layered; src/catalog/ is page chrome and is counted.
//   rootToken    a custom property set on :root or html in a sheet outside src/skin/
//   webFork      html:not([data-desktop]) (the Mac app is not a fork of the web)
//   aliasRule    a rule in src/ds/legacy-aliases.css (the V2 control aliases of PRs 2.1 and 2.2)
//   bridgeUse    a use, outside src/skin/, of a V2 palette name that the V3 bridge maps (var(--clay) ...; PLAN 7.6)
// Usage: node scripts/test-ds-boundary.mjs [--update | --init]
//   --update lowers the baseline to today's counts (it refuses a rise); --init writes today's counts as they are.
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const baselineFile = join(ui, "scripts", "ds-boundary-baseline.json");
const flag = name => process.argv.includes(name);

const MOTION_SHEETS = new Set(["boot-screen.css", "permission-coach.css", "copy-feedback.css"]);
const LAYOUT = /^(?:display|grid(?:-[\w-]+)?|flex(?:-[\w-]+)?|position|inset(?:-[\w-]+)?|top|right|bottom|left|margin(?:-[\w-]+)?|padding(?:-[\w-]+)?|gap|row-gap|column-gap|width|height|min-(?:width|height|block-size|inline-size)|max-(?:width|height|block-size|inline-size)|order|align-[\w-]+|justify-[\w-]+|place-[\w-]+|overflow(?:-[\w-]+)?|box-sizing)$/;
const MOTION = /^(?:transform|opacity|animation(?:-[\w-]+)?)$/;
const BRIDGE = /var\(\s*(--(?:clay|iris|lagoon|honey|green)(?:-text)?|--line-soft|--line-strong|--text(?:-[a-z-]+)?|--sidebar-bg|--paper(?:-[a-z-]+)?|--mail-[a-z-]+|--code-block-[a-z-]+|--code-inline-[a-z-]+|--display|--dots|--blur|--float-shadow|--card-elevation)\s*[,)]/g;

const walk = dir => readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? walk(join(dir, entry.name)) : [join(dir, entry.name)]);
const blank = css => css.replace(/\/\*[\s\S]*?\*\//g, match => match.replace(/[^\n]/g, " "));

/** Style rules with their at-rule stack: { selector, body, at, line }. Comments and strings are skipped. */
function rules(css) {
  const out = [], stack = [];
  let i = 0, start = 0;
  while (i < css.length) {
    const c = css[i];
    if (c === '"' || c === "'") { const q = c; i++; while (i < css.length && css[i] !== q) i += css[i] === "\\" ? 2 : 1; i++; continue; }
    if (c === "{") {
      const prelude = css.slice(start, i).trim();
      if (prelude.startsWith("@")) { stack.push(prelude); start = i + 1; i++; continue; }
      let depth = 1, j = i + 1;
      while (j < css.length && depth) { if (css[j] === "{") depth++; else if (css[j] === "}") depth--; j++; }
      out.push({ selector: prelude, body: css.slice(i + 1, j - 1), at: [...stack] });
      i = j; start = i; continue;
    }
    if (c === "}") { stack.pop(); start = i + 1; }
    if (c === ";") start = i + 1;
    i++;
  }
  return out;
}
const declarations = body => {
  const out = []; let depth = 0, start = 0, quote = "";
  for (let i = 0; i <= body.length; i += 1) {
    const c = body[i];
    if (quote) { if (c === quote) quote = ""; else if (c === "\\") i += 1; continue; }
    if (c === '"' || c === "'") quote = c;
    else if (c === "(") depth += 1;
    else if (c === ")") depth -= 1;
    else if ((c === ";" && !depth) || i === body.length) {
      const text = body.slice(start, i).trim(); start = i + 1;
      const colon = text.indexOf(":");
      if (colon > 0) out.push({ property: text.slice(0, colon).trim().toLowerCase(), value: text.slice(colon + 1).replace(/!\s*important/i, "").trim() });
    }
  }
  return out;
};
const splitList = text => { const out = []; let depth = 0, start = 0; for (let i = 0; i < text.length; i += 1) { const c = text[i]; if (c === "(" || c === "[") depth += 1; else if (c === ")" || c === "]") depth -= 1; else if (c === "," && !depth) { out.push(text.slice(start, i)); start = i + 1; } } out.push(text.slice(start)); return out.map(part => part.trim()).filter(Boolean); };
// :not() and :has() do not select the thing they hold; :is() and :where() do.
const selects = selector => selector.replace(/:(?:not|has)\((?:[^()]|\([^()]*\))*\)/g, "");
const namesKit = selector => /\.tb-[\w-]/.test(selects(selector));
// "From tokens": no literal duration; the time and easing come from var(--m-*) and var(--ease-*).
const noLiteralTime = value => !/(?:^|[\s,])\d*\.?\d+m?s\b|cubic-bezier\(|\bsteps\(/.test(value.replace(/var\([^)]*\)/g, ""));

/** Is this declaration allowed on a kit element in a sheet that is not the kit? */
function allowed(name, { property, value }) {
  if (LAYOUT.test(property)) return true;
  if (property === "z-index") return /^var\(\s*--z-[\w-]+\s*\)$/.test(value);
  if (MOTION_SHEETS.has(name) && MOTION.test(property)) return property.startsWith("animation") ? noLiteralTime(value) : true;
  return false;
}

const counts = {}, where = {};
const bump = (name, kind, note) => { (counts[name] ??= {})[kind] = (counts[name]?.[kind] ?? 0) + 1; (where[`${name}\0${kind}`] ??= []).push(note); };

for (const file of walk(src).filter(file => file.endsWith(".css")).sort()) {
  const name = relative(src, file), css = blank(readFileSync(file, "utf8"));
  const inSkin = name.startsWith("skin/"), inKit = name.startsWith("ds/");
  if (!inSkin) for (const match of css.matchAll(BRIDGE)) bump(name, "bridgeUse", `${match[1]} (line ${css.slice(0, match.index).split("\n").length})`);
  if (!inSkin) for (const match of css.matchAll(/html:not\(\[data-desktop\]\)/g)) bump(name, "webFork", `line ${css.slice(0, match.index).split("\n").length}`);
  for (const rule of rules(css)) {
    if (/^@(keyframes|-webkit-keyframes|font-face|property|page)\b/.test(rule.at.at(-1) ?? "")) continue;
    const parts = splitList(rule.selector);
    if (name === "ds/legacy-aliases.css") bump(name, "aliasRule", rule.selector.slice(0, 60));
    if (!inSkin && parts.some(part => /^(?::root|html)(?:\[[^\]]*\]|:where\([^)]*\))*$/.test(part)) && /(?:^|[;{\s])--[\w-]+\s*:/.test(rule.body)) bump(name, "rootToken", `${rule.selector.slice(0, 60)}`);
    if (inKit || inSkin || rule.at.some(prelude => prelude.startsWith("@layer"))) continue;
    if (!parts.some(namesKit)) continue;
    for (const declaration of declarations(rule.body)) {
      if (!allowed(name, declaration)) bump(name, "tbStyle", `"${rule.selector.slice(0, 70)}" sets ${declaration.property}`);
    }
  }
}

const total = current => Object.values(current).reduce((sum, kinds) => sum + Object.values(kinds).reduce((a, b) => a + b, 0), 0);
const sorted = object => Object.fromEntries(Object.entries(object).sort(([a], [b]) => a.localeCompare(b)).map(([key, kinds]) => [key, Object.fromEntries(Object.entries(kinds).sort(([a], [b]) => a.localeCompare(b)))]));
if (flag("--init")) {
  writeFileSync(baselineFile, `${JSON.stringify(sorted(counts), null, 2)}\n`);
  console.log(`ds-boundary: baseline written, ${total(counts)} in ${Object.keys(counts).length} files`);
  process.exit(0);
}
const baseline = existsSync(baselineFile) ? JSON.parse(readFileSync(baselineFile, "utf8")) : {};
const failures = [], lowered = [];
const advice = {
  tbStyle: "A sheet outside src/ds/ may only place a .tb-* element (layout properties). Change the look in the kit or in a skin role.",
  rootToken: "A token belongs in src/skin/ (a role) or the generator, not on :root in a component sheet.",
  webFork: "The Mac app is not a fork of the web: use a platform token block in the skin, not html:not([data-desktop]).",
  aliasRule: "The legacy aliases only retire; no new alias rule.",
  bridgeUse: "A V2 palette name is bridged to a V3 role; use the V3 role (var(--tomato), --ink, --line, ...) in new code.",
};
for (const [name, kinds] of Object.entries(counts)) for (const [kind, count] of Object.entries(kinds)) {
  const limit = baseline[name]?.[kind] ?? 0;
  if (count > limit) failures.push(`${name}: ${count} ${kind}, baseline ${limit}. ${advice[kind]}\n    ${where[`${name}\0${kind}`].slice(0, 6).join("\n    ")}`);
}
for (const [name, kinds] of Object.entries(baseline)) for (const [kind, limit] of Object.entries(kinds)) {
  const count = counts[name]?.[kind] ?? 0;
  if (count < limit) lowered.push(`${name} ${kind} ${limit} -> ${count}`);
}
if (failures.length) { console.log(`ds-boundary: FAIL\n  ${failures.join("\n  ")}`); process.exit(1); }
if (flag("--update")) {
  writeFileSync(baselineFile, `${JSON.stringify(sorted(counts), null, 2)}\n`);
  console.log(`ds-boundary: baseline lowered to ${total(counts)}`);
  process.exit(0);
}
if (lowered.length) { console.log(`ds-boundary: FAIL counts went down; lock the gain with --update:\n  ${lowered.join("\n  ")}`); process.exit(1); }
const sum = kind => Object.values(counts).reduce((n, kinds) => n + (kinds[kind] ?? 0), 0);
console.log(`ds-boundary: tbStyle ${sum("tbStyle")}, rootToken ${sum("rootToken")}, webFork ${sum("webFork")}, aliasRule ${sum("aliasRule")}, bridgeUse ${sum("bridgeUse")}; none above the baseline of ${total(baseline)} PASS`);
