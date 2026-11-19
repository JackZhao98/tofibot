// CSS layer guard (docs/design/interaction-libraries.md, Batch 1). Global defaults live in cascade layers, so they can
// never override a component; component styles stay outside layers and always win over them.
//  1. src/cascade.css declares the layer order, and every entry imports it before anything else.
//  2. A ratchet over every stylesheet in src/: per file, these counts may go down, never up (baseline in
//     scripts/css-layers-baseline.json; a new file starts at zero):
//       global      a rule whose selector names no class or id (element, :root, html, body, attribute, pseudo) outside
//                   the reset, tokens, skin and base layers
//       rootBump    a component selector raised with :root (":root .x", ":root :is(", ":root[data-theme] .x")
//       webFork     html:not([data-desktop]): the Mac app is not a fork of the web
//       important   !important
//       tokenDef    a custom property defined on :root or html outside the skin files (src/skin/)
//  3. V3 PLAN 7.5: every product rule is in legacy or a global layer (only UNLAYERED sheets may sit outside), layers do
//     not nest, third-party CSS reaches the page only through src/vendor/ in @layer vendor, entry HTML has no <style>,
//     the long-lived sheets have no !important in legacy, and no legacy !important can reach a kit element (.tb-*, a class in ds/aliases.json, a bare element).
// Usage: node scripts/test-css-layers.mjs [--update]   (--update writes the current counts as the new baseline)
import { readFileSync, readdirSync, statSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const baselineFile = join(ui, "scripts", "css-layers-baseline.json");
const failures = [];
const fail = message => { failures.push(message); console.log(`FAIL ${message}`); };
const ORDER = "@layer reset, tokens, skin, base, vendor, legacy, platform, utilities;";
const DEFAULT_LAYERS = new Set(["reset", "tokens", "skin", "base"]);

// ---- 1. the order line comes first everywhere
const cascade = join(src, "cascade.css");
if (!existsSync(cascade) || !readFileSync(cascade, "utf8").replace(/\/\*[\s\S]*?\*\//g, "").includes(ORDER)) fail(`src/cascade.css must declare: ${ORDER}`);
const walk = dir => readdirSync(dir).flatMap(name => { const path = join(dir, name); return statSync(path).isDirectory() ? walk(path) : [path]; });
const entries = walk(src).filter(file => /(^|\/)main\.tsx$/.test(file));
for (const entry of entries) {
  // First of all imports, not only of the stylesheets: a component imported earlier would bring its CSS, and its layers, first.
  const first = readFileSync(entry, "utf8").match(/^import\s+(?:[^"']+from\s+)?["']([^"']+)["']/m)?.[1] ?? "";
  if (!/(^|\/)cascade\.css$/.test(first)) fail(`${relative(ui, entry)}: the first import must be cascade.css (found ${first})`);
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
    if (!file.includes("/skin/") && parts.some(part => /^(:root|html)(\[[^\]]*\])*$/.test(part)) && /--[\w-]+\s*:/.test(rule.body)) tally.tokenDef++;
  }
  return tally;
}

const sheets = walk(src).filter(file => file.endsWith(".css") && !/\/catalog\/|\/acceptance\//.test(file)).sort();
const current = Object.fromEntries(sheets.map(file => [relative(src, file), count(file)]).filter(([, tally]) => Object.values(tally).some(Boolean)));
if (process.argv.includes("--update")) {
  writeFileSync(baselineFile, `${JSON.stringify(current, null, 2)}\n`);
  console.log(`wrote ${relative(ui, baselineFile)}`);
}
// ---- 3. legacy and vendor
const UNLAYERED = new Set(["cascade.css", "forced.css", "forced-focus.css", "oauth/connection.css"]);
const LONG_LIVED = new Set(["styles.css", "settings-system.css", "interaction-system.css", "v2-app.css", "onboarding.css", "context-card.css", "bot-settings.css"]);
const GLOBAL_LAYERS = new Set([...DEFAULT_LAYERS, "legacy"]);
const splitList = text => { const out = []; let depth = 0, start = 0; for (let i = 0; i < text.length; i += 1) { const c = text[i]; if (c === "(" || c === "[") depth += 1; else if (c === ")" || c === "]") depth -= 1; else if (c === "," && !depth) { out.push(text.slice(start, i)); start = i + 1; } } out.push(text.slice(start)); return out.map(part => part.trim()).filter(Boolean); };
// Does the selector end in a bare element or *? Such a rule could land on a kit element.
function endsBare(selector) {
  let depth = 0, start = 0;
  for (let i = 0; i < selector.length; i += 1) { const c = selector[i]; if (c === "(" || c === "[") depth += 1; else if (c === ")" || c === "]") depth -= 1; else if (!depth && /[\s>+~]/.test(c)) start = i + 1; }
  const compound = selector.slice(start).replace(/::[a-z-]+(\([^)]*\))?/g, ""), inner = [];
  let rest = "";
  for (let i = 0; i < compound.length;) {
    const open = compound.slice(i).match(/^:([a-z-]+)\(/);
    if (!open) { rest += compound[i]; i += 1; continue; }
    let level = 1, j = i + open[0].length; const from = j;
    while (j < compound.length && level) { if (compound[j] === "(") level += 1; else if (compound[j] === ")") level -= 1; j += 1; }
    if (open[1] === "is" || open[1] === "where") inner.push(compound.slice(from, j - 1));
    i = j;
  }
  if (/[.#[]/.test(rest)) return false;
  return inner.length ? inner.some(list => splitList(list).some(endsBare)) : true;
}
// The V2 button classes that src/ds/legacy-aliases.css draws with the kit (PR 2.1): a legacy !important on one would beat the kit.
const ALIASED = new RegExp(`\\.(?:${JSON.parse(readFileSync(join(src, "ds/aliases.json"), "utf8")).classes.join("|")})(?![\\w-])`);
let legacyImportant = 0;
for (const file of sheets) {
  const name = relative(src, file);
  for (const rule of rules(readFileSync(file, "utf8"))) {
    if (skipped(rule.at)) continue;
    const layers = layersOf(rule.at);
    if (layers.length > 1) fail(`src/${name}: "${rule.selector}" is in nested layers (${layers.join(" > ")})`);
    // The V3 kit (src/ds/) is unlayered on purpose: it must beat every layer (PLAN 1, 7.5).
    if (!UNLAYERED.has(name) && !name.startsWith("ds/") && !layers.some(layer => GLOBAL_LAYERS.has(layer))) fail(`src/${name}: "${rule.selector}" must be in @layer legacy or a global layer`);
    if (!layers.includes("legacy") || !/!\s*important/.test(rule.body)) continue;
    legacyImportant += (rule.body.match(/!\s*important/g) || []).length;
    if (LONG_LIVED.has(name)) fail(`src/${name}: "${rule.selector}" uses !important in a long-lived sheet; use :is(#_, ...) or forced.css`);
    const parts = splitList(rule.selector);
    if (/\.tb-/.test(rule.selector) || ALIASED.test(rule.selector) || parts.some(endsBare)) fail(`src/${name}: "${rule.selector}" has a legacy !important that can reach a kit element (a .tb-* class, an aliased control class from ds/aliases.json, or a bare element)`);
  }
}
const code = walk(src).filter(file => /\.(ts|tsx|js|jsx)$/.test(file));
for (const file of code) for (const [, spec] of readFileSync(file, "utf8").matchAll(/^import\s+(?:[^"']+from\s+)?["']([^"'.][^"']*\.css)["']/gm)) fail(`${relative(ui, file)}: import ${spec} through src/vendor/, in @layer vendor`);
for (const file of sheets.filter(file => relative(src, file).startsWith("vendor/"))) {
  const body = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "").trim();
  if (!/^(@import "[^"]+" layer\(vendor\);\s*)+$/.test(body)) fail(`${relative(ui, file)}: may only import third-party CSS with layer(vendor)`);
}
for (const name of readdirSync(ui).filter(entry => entry.endsWith(".html"))) if (/<style[\s>]/i.test(readFileSync(join(ui, name), "utf8"))) fail(`${name}: no inline <style> in entry HTML; unlayered CSS would beat legacy`);

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
console.log(`totals: ${Object.entries(totals).map(([key, value]) => `${key} ${value}`).join(", ")} across ${Object.keys(current).length} of ${sheets.length} stylesheets; legacyImportant ${legacyImportant}`);
if (failures.length) { console.log(`css-layers: ${failures.length} failure(s)`); process.exit(1); }
console.log("css-layers: PASS");
