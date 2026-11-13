// CSS layer guard (docs/design/interaction-libraries.md, Batch 1). Global defaults live in cascade layers, so they can
// never override a component; component styles stay outside layers and always win over them.
//  1. src/cascade.css declares the layer order, and every entry imports it before any other stylesheet.
//  2. A ratchet over every stylesheet in src/: per file, these counts may go down, never up (baseline in
//     scripts/css-layers-baseline.json; a new file starts at zero):
//       global      a rule whose selector names no class or id (element, :root, html, body, attribute, pseudo) outside
//                   the reset, tokens and base layers
//       rootBump    a component selector raised with :root (":root .x", ":root :is(", ":root[data-theme] .x")
//       webFork     html:not([data-desktop]): the Mac app is not a fork of the web
//       important   !important
//       tokenDef    a custom property defined on :root or html outside design-tokens.css
// Usage: node scripts/test-css-layers.mjs [--update]   (--update writes the current counts as the new baseline)
import { readFileSync, readdirSync, statSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const baselineFile = join(ui, "scripts", "css-layers-baseline.json");
const failures = [];
const fail = message => { failures.push(message); console.log(`FAIL ${message}`); };
const ORDER = "@layer reset, tokens, base, components, platform, utilities;";
const DEFAULT_LAYERS = new Set(["reset", "tokens", "base"]);

// ---- 1. the order line comes first everywhere
const cascade = join(src, "cascade.css");
if (!existsSync(cascade) || !readFileSync(cascade, "utf8").replace(/\/\*[\s\S]*?\*\//g, "").includes(ORDER)) fail(`src/cascade.css must declare: ${ORDER}`);
const walk = dir => readdirSync(dir).flatMap(name => { const path = join(dir, name); return statSync(path).isDirectory() ? walk(path) : [path]; });
const entries = walk(src).filter(file => /(^|\/)main\.tsx$/.test(file));
for (const entry of entries) {
  const imports = [...readFileSync(entry, "utf8").matchAll(/^import "([^"]+\.css)";$/gm)].map(m => m[1]);
  if (imports.length && !/(^|\/)cascade\.css$/.test(imports[0])) fail(`${relative(ui, entry)}: the first stylesheet must be cascade.css (found ${imports[0]})`);
}

// ---- 2. counts per stylesheet
// A small block walker: comments and strings are skipped, @-rule preludes are tracked so a rule knows its layers.
function rules(css) {
  const out = [], stack = [];
  let i = 0, start = 0;
  while (i < css.length) {
    const c = css[i];
    if (c === "/" && css[i + 1] === "*") { const end = css.indexOf("*/", i + 2); i = end < 0 ? css.length : end + 2; continue; }
    if (c === '"' || c === "'") { const q = c; i++; while (i < css.length && css[i] !== q) i += css[i] === "\\" ? 2 : 1; i++; continue; }
    if (c === "{") {
      const prelude = css.slice(start, i).replace(/\/\*[\s\S]*?\*\//g, "").trim();
      if (prelude.startsWith("@")) { stack.push(prelude); start = i + 1; i++; continue; }
      // A style rule: its body runs to the matching brace (nested rules inside it are rare and counted with it).
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
const layersOf = at => at.filter(p => p.startsWith("@layer")).map(p => p.slice(6).trim());
const skipped = at => at.some(p => /^@(keyframes|-webkit-keyframes|font-face|property|page)\b/.test(p));
// What a selector targets: :not() and :has() select nothing themselves; :is() and :where() target their contents.
const strip = selector => selector.replace(/:(?:not|has)\((?:[^()]|\([^()]*\))*\)/g, "").replace(/:(?:is|where)\(((?:[^()]|\([^()]*\))*)\)/g, " $1 ").replace(/\[[^\]]*\]/g, "").replace(/::?[a-z-]+(\([^)]*\))?/g, "");

function count(file) {
  const css = readFileSync(file, "utf8");
  const tally = { global: 0, rootBump: 0, webFork: 0, important: 0, tokenDef: 0 };
  tally.important = (css.replace(/\/\*[\s\S]*?\*\//g, "").match(/!\s*important/g) || []).length;
  tally.webFork = (css.replace(/\/\*[\s\S]*?\*\//g, "").match(/html:not\(\[data-desktop\]\)/g) || []).length;
  for (const rule of rules(css)) {
    if (skipped(rule.at)) continue;
    const parts = rule.selector.split(/,(?![^(]*\))/).map(part => part.trim()).filter(Boolean);
    const inDefaults = layersOf(rule.at).some(name => DEFAULT_LAYERS.has(name));
    if (!inDefaults && parts.some(part => !/[.#]/.test(strip(part)))) tally.global++;
    tally.rootBump += parts.filter(part => /^:root(\[[^\]]*\])*\s+(\.|:is\(|:where\()/.test(part)).length;
    if (!file.endsWith("design-tokens.css") && parts.some(part => /^(:root|html)(\[[^\]]*\])*$/.test(part)) && /--[\w-]+\s*:/.test(rule.body)) tally.tokenDef++;
  }
  return tally;
}

const sheets = walk(src).filter(file => file.endsWith(".css") && !/\/(catalog|motion-lab)\/|\/acceptance\//.test(file)).sort();
const current = Object.fromEntries(sheets.map(file => [relative(src, file), count(file)]).filter(([, tally]) => Object.values(tally).some(Boolean)));
if (process.argv.includes("--update")) {
  writeFileSync(baselineFile, `${JSON.stringify(current, null, 2)}\n`);
  console.log(`wrote ${relative(ui, baselineFile)}`);
}
const baseline = existsSync(baselineFile) ? JSON.parse(readFileSync(baselineFile, "utf8")) : {};
const totals = { global: 0, rootBump: 0, webFork: 0, important: 0, tokenDef: 0 };
const lower = [];
for (const [file, tally] of Object.entries(current)) {
  for (const [key, value] of Object.entries(tally)) {
    totals[key] += value;
    const allowed = baseline[file]?.[key] ?? 0;
    if (value > allowed) fail(`src/${file}: ${key} ${value} > ${allowed}. Put a global default in @layer base (or reset/tokens), keep component rules in their own file without :root bumps, html:not([data-desktop]) or !important.`);
    else if (value < allowed) lower.push(`src/${file} ${key} ${allowed} -> ${value}`);
  }
}
for (const [file, tally] of Object.entries(baseline)) if (!current[file]) for (const [key, value] of Object.entries(tally)) if (value) lower.push(`src/${file} ${key} ${value} -> 0`);
if (lower.length && !process.argv.includes("--update")) fail(`counts went down; lock the progress in with --update:\n  ${lower.join("\n  ")}`);
console.log(`totals: ${Object.entries(totals).map(([key, value]) => `${key} ${value}`).join(", ")} across ${Object.keys(current).length} of ${sheets.length} stylesheets`);
if (failures.length) { console.log(`css-layers: ${failures.length} failure(s)`); process.exit(1); }
console.log("css-layers: PASS");
