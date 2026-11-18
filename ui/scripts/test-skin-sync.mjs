// Skin parity guard (V3 PLAN 7.3 and 7.7). Fails when:
//  1. the source is inconsistent (light/dark role sets differ, a reference does not resolve, a primitive is unused);
//  2. a generated file differs from a fresh build (someone edited it, or forgot `npm --prefix ui run build:skin`);
//  3. a value drifts from the vendored V3.0 values (test-fixtures/design/v3.0) without a changes[] entry naming it;
//  4. a structure token that exists today (test-fixtures/design/legacy-structure.json) changes value before the flip;
//  5. a probe skin leaves a role at its V3 value;
//  6. motion.ts or layers.ts drift from the generated tokens.ts;
//  7. a var(--x) in a stylesheet has no definition anywhere in ui/src.
// Run: npm --prefix ui run test:skin-sync
import { readFileSync, readdirSync, existsSync } from "node:fs";
import { join, relative } from "node:path";
import { PROBES, buildAll, loadSource, probeValue, resolveSource, structureTokens, ui, validate } from "./skin-lib.mjs";

const src = join(ui, "src");
const failures = [];
const fail = message => failures.push(message);
const same = (a, b) => String(a).replace(/\s+/g, "").toLowerCase() === String(b).replace(/\s+/g, "").toLowerCase();
const json = path => JSON.parse(readFileSync(join(ui, "test-fixtures", "design", path), "utf8"));

const source = loadSource();
// ---- 1. the source itself
for (const problem of validate(source)) fail(`source: ${problem}`);
let built = {};
if (!failures.length) built = buildAll(source);

// ---- 2. generated files equal a fresh build
for (const [file, text] of Object.entries(built)) {
  const path = join(src, file);
  const disk = existsSync(path) ? readFileSync(path, "utf8") : null;
  if (disk === null) fail(`${file} is missing; run npm --prefix ui run build:skin`);
  else if (disk !== text) {
    const a = disk.split("\n"), b = text.split("\n");
    const line = a.findIndex((row, index) => row !== b[index]);
    fail(`${file} differs from a fresh build at line ${line + 1} (disk: ${JSON.stringify(a[line] ?? "")}, built: ${JSON.stringify(b[line] ?? "")}); run npm --prefix ui run build:skin`);
  }
}

// ---- 3. V3.0 values
const v3 = json("v3.0/values.json");
const resolved = resolveSource(source);
const named = new Set(source.changes.flatMap(change => change.names));
function compare(scope, actual, expected, label) {
  for (const [name, value] of Object.entries(actual)) {
    if (!(name in expected)) { if (!named.has(name)) fail(`${scope}: --${name} is not in V3.0 and has no changes[] entry`); continue; }
    if (!same(value, expected[name]) && !named.has(name)) fail(`${scope}: --${name} is ${value}, V3.0 says ${expected[name]}; fix the value or name it in changes[]`);
  }
  for (const name of Object.keys(expected)) if (!(name in actual) && !label.skip?.has(name)) fail(`${scope}: V3.0 --${name} is missing from the source`);
}
compare("light", resolved.light, v3.light, {});
compare("dark", resolved.dark, v3.dark, {});
compare("letter scope", resolved.letter, v3.letter, {});
const structure = structureTokens(source);
// common.json holds roles and V3.0 structure together; each side is compared against its own part.
const structureV3 = Object.fromEntries(Object.entries(v3.common).filter(([name]) => name in structure));
compare("common roles", resolved.common, Object.fromEntries(Object.entries(v3.common).filter(([name]) => !(name in structure))), {});
const legacy = json("legacy-structure.json").values;
for (const [name, value] of Object.entries(structure)) {
  if (name in structureV3 && !same(value, structureV3[name]) && !named.has(name)) fail(`structure: --${name} is ${value}, V3.0 says ${structureV3[name]}; fix the value or name it in changes[]`);
  if (!(name in structureV3) && !(name in legacy)) fail(`structure: --${name} is in neither V3.0 nor today's structure.css, so nothing vouches for it`);
}
// ---- 4. today's structure keeps its values until the flip
for (const [name, value] of Object.entries(legacy)) {
  if (!(name in structure)) fail(`structure: --${name} exists today and disappeared`);
  else if (!same(structure[name], value)) fail(`structure: --${name} is ${structure[name]}, today it is ${value}; the legacy skin must not move`);
}
for (const change of source.changes) {
  // A token V3.0 lacks is a new one and does need its entry (compare() above asks for it); a V3.0 token needs one only when it moved.
  for (const name of change.names) {
    const was = v3.light[name] ?? v3.common[name];
    const now = resolved.light[name] ?? resolved.common[name] ?? structure[name];
    if (was !== undefined && now !== undefined && same(now, was)) fail(`changes[]: --${name} still has its V3.0 value, so it needs no entry`);
  }
}

// ---- 5. probes
for (const [id, probe] of Object.entries(PROBES)) {
  for (const [theme, roles] of Object.entries({ light: resolved.light, dark: resolved.dark, common: resolved.common })) {
    for (const [name, value] of Object.entries(roles)) {
      // A bare alias (--accent: var(--tomato)) follows the role it names, so it differs when that role does.
      if (!/^var\(--[\w-]+\)$/.test(value) && same(probeValue(value, probe), value)) fail(`${id}: --${name} (${theme}) keeps its V3 value ${value.slice(0, 40)}`);
    }
  }
}

// ---- 6. JS mirrors
const tokensTs = built["skin/tokens.ts"] ?? "";
const object = (text, name) => Object.fromEntries([...(new RegExp(`export const ${name} = \\{([\\s\\S]*?)\\n\\}`).exec(text)?.[1] ?? "").matchAll(/(\w+):\s*("[^"]*"|-?\d+)/g)].map(([, key, value]) => [key, value.replace(/^"|"$/g, "")]));
for (const [file, exports] of [["motion.ts", ["MOTION", "EASE"]], ["layers.ts", ["Z"]]]) {
  const mirror = readFileSync(join(src, file), "utf8");
  for (const name of exports) {
    const generated = object(tokensTs, name);
    // motion.ts writes inline objects on one line; read its keys with the same pattern.
    const body = new RegExp(`export const ${name} = \\{([\\s\\S]*?)\\} as const`).exec(mirror)?.[1] ?? "";
    const mirrored = Object.fromEntries([...body.matchAll(/(\w+):\s*("[^"]*"|-?\d+)/g)].map(([, key, value]) => [key, value.replace(/^"|"$/g, "")]));
    if (!Object.keys(mirrored).length) fail(`${file}: ${name} was not found`);
    for (const [key, value] of Object.entries(mirrored)) {
      if (!same(generated[key], value)) fail(`${file}: ${name}.${key} is ${value}, skin/tokens.ts says ${generated[key]}`);
    }
  }
}

// ---- 7. var() without a definition
const walk = dir => readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? walk(join(dir, entry.name)) : [join(dir, entry.name)]);
const files = walk(src).filter(file => /\.(css|tsx?|js|mjs)$/.test(file));
const defined = new Set();
for (const file of files) {
  const text = readFileSync(file, "utf8");
  for (const [, name] of text.matchAll(/(--[\w-]+)\s*:/g)) defined.add(name);
  for (const [, name] of text.matchAll(/["'`](--[\w-]+)["'`]/g)) defined.add(name); // style={{ "--x": ... }}, setProperty("--x")
}
// Set at run time by a library or from a template string, so no literal definition exists. --m-fast is a real gap
// (web-tools-settings.css), left as it is because naming it a duration would change today's transitions.
const RUNTIME = [/^--radix-/, /^--head-[lts]$/, /^--rail-[lts]$/];
const KNOWN_UNDEFINED = new Set(["--m-fast"]);
const undefinedUses = new Map();
for (const file of files.filter(file => file.endsWith(".css"))) {
  const css = readFileSync(file, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  for (const [, name, comma] of css.matchAll(/var\(\s*(--[\w-]+)\s*(,)?/g)) {
    if (comma || defined.has(name) || RUNTIME.some(pattern => pattern.test(name)) || KNOWN_UNDEFINED.has(name)) continue;
    undefinedUses.set(name, [...(undefinedUses.get(name) ?? []), relative(src, file)]);
  }
}
for (const [name, where] of undefinedUses) fail(`${name} is used without a fallback in ${[...new Set(where)].join(", ")} and defined nowhere in ui/src`);
// The skin sheets must resolve their own references: every var() in a skin sheet is a role or a structure token.
const known = new Set([...Object.keys(resolved.light), ...Object.keys(resolved.common), ...Object.keys(structure)].map(name => `--${name}`));
for (const file of ["skin/v3.css", "skin/roles.css"]) {
  const path = join(src, file);
  if (!existsSync(path)) continue;
  for (const [, name] of readFileSync(path, "utf8").replace(/\/\*[\s\S]*?\*\//g, "").matchAll(/var\(\s*(--[\w-]+)/g)) {
    if (!known.has(name)) fail(`${file}: var(${name}) names no role or structure token`);
  }
}

if (failures.length) {
  console.error(`skin sync: FAIL\n  ${failures.join("\n  ")}`);
  process.exit(1);
}
const roles = Object.keys(resolved.light).length + Object.keys(resolved.common).length;
console.log(`skin sync: ${roles} roles, ${Object.keys(structure).length} structure tokens, ${Object.keys(built).length} generated files match the source, V3.0 and today's structure PASS`);
