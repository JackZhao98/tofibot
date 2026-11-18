// Palette guard (V3 PLAN 7.7), a ratchet: outside src/skin/, a literal colour, font family or backdrop blur is a hard-coded
// skin value, because a second skin cannot change it. This replaces test:v2-palette. Today's offenders are the baseline
// (scripts/skin-palette-baseline.json, per file and per kind); a count may fall, never rise, and a new file starts at zero.
//
// Kinds (what a file is charged for):
//   color     #hex, rgb[a]( hsl[a]( hwb( lab( lch( oklab( oklch( color( and named colours (not transparent, currentColor,
//             inherit) in a colour property or a custom property of a stylesheet; the same literals in a string of
//             .ts/.tsx/.js/.mjs. This includes literals inside color-mix(), box-shadow and filter, and TSX inline styles.
//   font      a literal font-family (CSS, or fontFamily in script), or a font shorthand with a literal family
//   blur      a backdrop-filter whose blur() takes a literal length instead of var()
//   svgPaint  an SVG fill or stroke other than currentColor, none or var()
// Exempt: src/skin/, lib/tofi-avatar/ (artwork), locales, fonts.css (the @font-face rules), mask-image values and anything
// inside url().
// Usage: node scripts/test-skin-palette.mjs [--update]   (--update only lowers counts; --init writes today's counts)
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const baselineFile = join(ui, "scripts", "skin-palette-baseline.json");
const flag = name => process.argv.includes(name);

const NAMED = new Set(`aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon darkseagreen darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink deepskyblue dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen linen magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream mistyrose moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow yellowgreen`.split(/\s+/));
const COLOR_PROPS = /^(?:color|background|background-color|background-image|border|border-(?:top|right|bottom|left|block|inline)(?:-(?:start|end))?(?:-color)?|border-color|outline|outline-color|fill|stroke|stop-color|flood-color|lighting-color|box-shadow|text-shadow|filter|caret-color|accent-color|text-decoration|text-decoration-color|text-emphasis-color|column-rule|column-rule-color|scrollbar-color|-webkit-text-fill-color|-webkit-text-stroke|-webkit-tap-highlight-color|--[\w-]+)$/;
const FUNCTIONS = /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch)\(|\bcolor\(\s*(?:srgb|srgb-linear|display-p3|a98-rgb|prophoto-rgb|rec2020|xyz)/g;
const HEX = /#[0-9a-fA-F]{3,8}\b/g;
const strip = text => text.replace(/url\((?:"[^"]*"|'[^']*'|[^)]*)\)/g, "url()");

function namedColors(value) {
  let count = 0;
  for (const [word] of strip(value).replace(/var\([^)]*\)/g, "").matchAll(/(?<![\w#.-])[a-z]+(?![\w-]*\()(?![\w-])/gi)) if (NAMED.has(word.toLowerCase())) count++;
  return count;
}
const literalColors = (value, named) => (strip(value).match(HEX) ?? []).length + (strip(value).match(FUNCTIONS) ?? []).length + (named ? namedColors(value) : 0);

/** Offences in a stylesheet: [{ kind, line }]. */
function cssOffences(css) {
  const out = [];
  const text = css.replace(/\/\*[\s\S]*?\*\//g, match => match.replace(/[^\n]/g, " "));
  for (const match of text.matchAll(/(?:^|[;{}\s])((?:--)?[\w-]+)\s*:\s*([^;{}]*)/g)) {
    const [, property, value] = match;
    const line = text.slice(0, match.index + match[0].indexOf(property)).split("\n").length;
    if (/^(?:-webkit-)?mask(?:-image)?$/.test(property)) continue;
    if (COLOR_PROPS.test(property)) for (let i = literalColors(value, true); i > 0; i--) out.push({ kind: "color", line });
    if (property === "font-family" && !/^\s*(?:var\(|inherit|initial|unset)/.test(value)) out.push({ kind: "font", line });
    if (property === "font" && !/var\(/.test(value) && /["']|sans-serif|system-ui|monospace|serif/.test(value)) out.push({ kind: "font", line });
    if (/^(?:-webkit-)?backdrop-filter$/.test(property) && /blur\(\s*(?!var\()/.test(value)) out.push({ kind: "blur", line });
    if ((property === "fill" || property === "stroke") && !/^\s*(?:currentColor|none|inherit|transparent|context-(?:fill|stroke)|var\(|url\(|color-mix\()/i.test(value)) out.push({ kind: "svgPaint", line });
  }
  return out;
}

// Also the animation fill modes: { fill: "both" } in a Web Animations call is not a paint.
const PAINT_OK = /^(?:currentColor|none|inherit|transparent|var\(|url\(#|both|forwards|backwards|auto)/i;
/** Offences in script (.ts, .tsx, .js, .mjs). */
function scriptOffences(code) {
  const out = [];
  const text = code.replace(/\/\*[\s\S]*?\*\//g, match => match.replace(/[^\n]/g, " ")).replace(/(^|[^:"'`\\])\/\/[^\n]*/g, "$1");
  const lineOf = index => text.slice(0, index).split("\n").length;
  for (const match of text.matchAll(/(?<=["'`(\s:,{[])#[0-9a-fA-F]{3,8}\b/g)) out.push({ kind: "color", line: lineOf(match.index) });
  for (const match of text.matchAll(FUNCTIONS)) out.push({ kind: "color", line: lineOf(match.index) });
  // Named colours and fills in an inline style or a paint attribute: key: "value", key={"value"}, key="value".
  for (const match of text.matchAll(/\b(color|background|backgroundColor|borderColor|outlineColor|boxShadow|filter|textShadow|caretColor|accentColor|fill|stroke)\s*[:=]\s*\{?\s*(["'`])([^"'`]*)\2/g)) {
    const [, key, , value] = match;
    if (namedColors(value) > 0 && key !== "fill" && key !== "stroke") out.push({ kind: "color", line: lineOf(match.index) });
    if ((key === "fill" || key === "stroke") && value.trim() && !PAINT_OK.test(value.trim())) out.push({ kind: "svgPaint", line: lineOf(match.index) });
  }
  for (const match of text.matchAll(/\bfontFamily\s*:\s*(["'`])(?!var\()/g)) out.push({ kind: "font", line: lineOf(match.index) });
  for (const match of text.matchAll(/\bbackdropFilter\s*:\s*(["'`])[^"'`]*blur\(\s*(?!var\()/g)) out.push({ kind: "blur", line: lineOf(match.index) });
  return out;
}

const walk = dir => readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? walk(join(dir, entry.name)) : [join(dir, entry.name)]);
// fonts.css declares the font faces, so its font-family lines are definitions, not uses.
const skipped = name => name.startsWith("skin/") || name.startsWith("lib/tofi-avatar/") || name.startsWith("locales/") || name === "fonts.css";
const counts = {}, where = {};
for (const file of walk(src)) {
  const name = relative(src, file);
  if (skipped(name) || !/\.(?:css|tsx?|js|mjs)$/.test(name)) continue;
  const text = readFileSync(file, "utf8");
  const offences = name.endsWith(".css") ? cssOffences(text) : scriptOffences(text);
  for (const { kind, line } of offences) {
    counts[name] ??= {}; counts[name][kind] = (counts[name][kind] ?? 0) + 1;
    (where[`${name}\0${kind}`] ??= []).push(line);
  }
}
const sorted = value => Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([key, inner]) => [key, Object.fromEntries(Object.entries(inner).sort(([a], [b]) => a.localeCompare(b)))]));

const total = current => Object.values(current).reduce((sum, kinds) => sum + Object.values(kinds).reduce((a, b) => a + b, 0), 0);
if (flag("--init")) {
  writeFileSync(baselineFile, JSON.stringify(sorted(counts), null, 2) + "\n");
  console.log(`skin palette: baseline written, ${total(counts)} offences in ${Object.keys(counts).length} files`);
  process.exit(0);
}
const baseline = existsSync(baselineFile) ? JSON.parse(readFileSync(baselineFile, "utf8")) : {};
const failures = [], lowered = {};
for (const [name, kinds] of Object.entries(counts)) for (const [kind, count] of Object.entries(kinds)) {
  const allowed = baseline[name]?.[kind] ?? 0;
  if (count > allowed) failures.push(`${name}: ${count} ${kind} offence(s), baseline ${allowed} (line ${where[`${name}\0${kind}`].join(", ")}). Use a skin role (var(--...)); a second skin cannot change a literal.`);
}
for (const [name, kinds] of Object.entries(baseline)) for (const [kind, allowed] of Object.entries(kinds)) {
  const count = counts[name]?.[kind] ?? 0;
  if (count < allowed) (lowered[name] ??= {})[kind] = count;
}
if (failures.length) {
  console.error(`skin palette: FAIL\n  ${failures.join("\n  ")}`);
  process.exit(1);
}
if (flag("--update")) {
  const next = {};
  for (const [name, kinds] of Object.entries(counts)) next[name] = { ...kinds };
  writeFileSync(baselineFile, JSON.stringify(sorted(next), null, 2) + "\n");
  console.log(`skin palette: baseline lowered to ${total(counts)} offences`);
  process.exit(0);
}
const fell = Object.keys(lowered).length;
console.log(`skin palette: ${total(counts)} offences in ${Object.keys(counts).length} files, none above the baseline of ${total(baseline)}${fell ? ` (${fell} file(s) are below it: run with --update to lock the gain)` : ""} PASS`);
