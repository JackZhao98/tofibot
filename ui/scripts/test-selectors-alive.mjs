// Dead-selector guard (V3 PLAN 7.7). A browser test that looks for `.old-class` keeps passing after the class is gone:
// the query finds nothing, and the test treats "nothing" as fine. This reads every selector the scripts hand to the page
// and fails when a class, id or data attribute in it no longer appears anywhere in src/.
//
// What counts as a selector: the first string literal argument of querySelector(All), locator, $, $$, closest, matches,
// waitForSelector, and of Playwright page actions that take one (click, fill, hover, focus, press, check, isVisible,
// textContent, innerText, getAttribute, count, ...); and the value of a `selector:` or `probe:` key. A template literal
// with ${} is cut at the first hole (only the static head is judged). Strings in other places are never judged.
// What counts as a name: `.class`, `#id` and `[data-attr]` / `[data-attr=...]`. Tags, aria-*, pseudo-classes and attribute
// values are not judged.
// Alive means: the name appears in a file under src/ (a stylesheet, a component, a catalog state) as a whole word, or it is
// the static head of a name src/ builds with a template (`tb-btn--${variant}`), or the script itself builds the markup
// that carries it (the name appears in the script outside its selector literals), or it belongs to a third-party library
// (xterm, Radix).
// Ratchet: the names already dead when this guard landed are in scripts/selectors-alive-baseline.json, per script. A new
// dead name fails; a name that came back to life or left the scripts must be removed from the baseline (--update).
// Usage: node scripts/test-selectors-alive.mjs [--update | --init] [--list]
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src"), scripts = join(ui, "scripts");
const baselineFile = join(scripts, "selectors-alive-baseline.json");
const flag = name => process.argv.includes(name);
const walk = dir => readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.name === "node_modules" ? [] : entry.isDirectory() ? walk(join(dir, entry.name)) : [join(dir, entry.name)]);

const THIRD_PARTY = /^(?:xterm|radix|react-|ProseMirror|katex|hljs|language-|sr-only$|data-radix-|data-aria-hidden$)/;
const CALLS = "querySelector|querySelectorAll|locator|\\$|\\$\\$|\\$eval|\\$\\$eval|closest|matches|waitForSelector|click|dblclick|fill|hover|focus|blur|press|check|uncheck|tap|isVisible|isHidden|isEnabled|isDisabled|isChecked|textContent|innerText|innerHTML|getAttribute|count|selectOption|setInputFiles|dispatchEvent|scrollIntoViewIfNeeded|boundingBox|screenshot";
const CALL = new RegExp(`(?:\\.|\\b)(?:${CALLS})\\s*\\(\\s*(["'\`])((?:\\\\.|(?!\\1)[^\\\\\\n])*)\\1`, "g");
const KEYED = /\b(?:selector|probe|sel)\s*:\s*(["'`])((?:\\.|(?!\1)[^\\\n])*)\1/g;
const NAME = /(?<![\w-])\.(-?[A-Za-z_][\w-]*)|#([A-Za-z_][\w-]*)|\[\s*(data-[\w-]+)/g;

/** Names in a selector string: ".class", "#id", "[data-x]" (as "data-x"). */
function names(selector) {
  const head = selector.split("${")[0].replace(/\\(["'])/g, "$1");
  const clean = head.replace(/\[[^\]]*=\s*(?:"[^"]*"|'[^']*'|[^\]\s]*)\s*\]/g, match => `[${match.slice(1).split("=")[0].trim()}]`).replace(/"[^"]*"|'[^']*'/g, "");
  const out = new Set();
  for (const [, cls, id, attr] of clean.matchAll(NAME)) {
    if (cls) { if (!/^\d/.test(cls) && !/[.]$/.test(cls)) out.add(`.${cls}`); }
    else if (id) out.add(`#${id}`);
    else if (attr) out.add(`[${attr}]`);
  }
  // A cut template leaves a partial last name ("tb-btn--"): not judged.
  if (head !== selector) for (const name of [...out]) if (head.endsWith(name.replace(/^\[|\]$/g, "")) || head.endsWith(name)) out.delete(name);
  return out;
}

// ---- what src/ has
const srcText = walk(src).filter(file => /\.(css|tsx?|jsx?|mjs|html|json|svg)$/.test(file)).map(file => readFileSync(file, "utf8")).join("\n");
const heads = new Set([...srcText.matchAll(/([A-Za-z][\w-]{2,})\$\{/g)].map(match => match[1]));
const words = new Set(srcText.match(/[A-Za-z_][\w-]*/g));
const aliveInSrc = name => {
  const bare = name.replace(/^[.#]/, "").replace(/^\[|\]$/g, "");
  if (THIRD_PARTY.test(bare)) return true;
  if (words.has(bare)) return true;
  // data-dnd-ready is set as dataset.dndReady.
  if (bare.startsWith("data-") && words.has(bare.slice(5).replace(/-(\w)/g, (_, c) => c.toUpperCase()))) return true;
  // The static head of a built name: src has `tb-btn--${variant}`, the script asks for tb-btn--primary.
  // Only for a class or id, and only a head with a real prefix of its own ("tb-btn--", not "data-").
  if (/^\[/.test(name)) return false;
  return [...heads].some(head => /^[a-z][\w]*-[\w-]*[-_]$/i.test(head) && !/^(?:data|aria)-/.test(head) && bare.startsWith(head));
};

// ---- what the scripts use
const files = walk(scripts).filter(file => /\.(mjs|tsx?|js|html)$/.test(file) && !/\/test-selectors-alive\.mjs$/.test(file)).sort();
const used = {}; // script -> Map(name -> first line)
for (const file of files) {
  const text = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, match => match.replace(/[^\n]/g, " "));
  const literals = [];
  for (const regex of [CALL, KEYED]) for (const match of text.matchAll(regex)) {
    const value = match[2];
    if (!/[.#\[]/.test(value)) continue;
    literals.push({ value, index: match.index + match[0].indexOf(value) });
  }
  if (!literals.length) continue;
  // The text of the script without its selector literals: a name found here is built by the script itself.
  let rest = text;
  for (const { value } of [...literals].sort((a, b) => b.index - a.index)) rest = rest.replace(value, " ");
  const own = new Set(rest.match(/[A-Za-z_][\w-]*/g));
  const map = new Map();
  for (const { value, index } of literals) {
    for (const name of names(value)) {
      const bare = name.replace(/^[.#]/, "").replace(/^\[|\]$/g, "");
      if (own.has(bare)) continue;
      if (!map.has(name)) map.set(name, text.slice(0, index).split("\n").length);
    }
  }
  if (map.size) used[relative(scripts, file)] = map;
}

const dead = {};
let checked = 0;
for (const [script, map] of Object.entries(used)) for (const [name, line] of map) {
  checked += 1;
  if (!aliveInSrc(name)) (dead[script] ??= {})[name] = line;
}
const flat = object => Object.entries(object).flatMap(([script, list]) => (Array.isArray(list) ? list : Object.keys(list)).map(name => `${script} ${name}`));
const total = flat(dead).length;
if (flag("--list")) { for (const entry of flat(dead)) console.log(entry); }
const write = () => writeFileSync(baselineFile, `${JSON.stringify(Object.fromEntries(Object.entries(dead).sort(([a], [b]) => a.localeCompare(b)).map(([script, list]) => [script, Object.keys(list).sort()])), null, 2)}\n`);
if (flag("--init")) { write(); console.log(`selectors-alive: baseline written, ${total} dead name(s) in ${Object.keys(dead).length} script(s)`); process.exit(0); }
const baseline = existsSync(baselineFile) ? JSON.parse(readFileSync(baselineFile, "utf8")) : {};
const known = new Set(flat(baseline)), now = new Set(flat(dead));
const fresh = [...now].filter(entry => !known.has(entry)), gone = [...known].filter(entry => !now.has(entry));
if (fresh.length) {
  console.log("selectors-alive: FAIL a script uses a selector that src/ no longer has (the test would pass on nothing):");
  for (const entry of fresh) { const [script, name] = entry.split(" "); console.log(`  ${script}:${dead[script][name]} ${name}`); }
  console.log("Update the selector to the class the component has now, or delete the dead check.");
  process.exit(1);
}
if (flag("--update")) { write(); console.log(`selectors-alive: baseline lowered to ${total}`); process.exit(0); }
if (gone.length) { console.log(`selectors-alive: FAIL ${gone.length} baseline name(s) are alive or unused now; lock the gain with --update:\n  ${gone.join("\n  ")}`); process.exit(1); }
console.log(`selectors-alive: ${checked} selector name(s) in ${Object.keys(used).length} script(s); ${total} dead, all in the baseline PASS`);
