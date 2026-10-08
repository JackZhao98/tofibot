#!/usr/bin/env node
// i18n gate for the Web UI. See src/locales/README.md.
//
//   npm run test:i18n                      catalogs + hardcoded-text ratchet
//   npm run test:i18n -- --untranslated ja list ja keys still missing (English shows)
//   npm run test:i18n -- --update-baseline lower the ratchet after converting files
//   npm run test:i18n -- --strict          also fail on untranslated keys in any language
//
// 1. Catalogs: every namespace file exists for every language; no key outside
//    en; plural forms match the language's CLDR categories; {{variables}} and
//    <tags> match en; en holds no CJK text. Languages in COMPLETE must have
//    every en key (COMPLETE_LANGUAGES in src/i18n/languages.ts); the others
//    may lag (i18next falls back to en) and are
//    reported as untranslated.
// 2. Source: counts CJK text runs and obvious English UI prose per file in
//    src (excluding locales/, motion-lab/ and i18n/languages.ts). A file may not
//    exceed its count in scripts/i18n-baseline.json; files not listed must be 0.
import { readFileSync, readdirSync, statSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const localesDir = join(src, "locales");
const baselinePath = join(ui, "scripts", "i18n-baseline.json");
const SOURCE = "en";

const args = process.argv.slice(2);
const flag = name => args.includes(name);
const option = name => { const index = args.indexOf(name); return index >= 0 ? args[index + 1] : undefined; };
const errors = [];
const notes = [];

// ---- Language and namespace lists come from the runtime source of truth.
const languagesSource = readFileSync(join(src, "i18n", "languages.ts"), "utf8");
const listOf = name => {
  const match = new RegExp(`export const ${name} = \\[([^\\]]*)\\] as const`).exec(languagesSource);
  if (!match) throw new Error(`cannot read ${name} from src/i18n/languages.ts`);
  return [...match[1].matchAll(/"([^"]+)"/g)].map(item => item[1]);
};
const LANGUAGES = listOf("LANGUAGES");
const NAMESPACES = listOf("NAMESPACES");
/** Languages that must be complete (COMPLETE_LANGUAGES in languages.ts). */
const COMPLETE = new Set(listOf("COMPLETE_LANGUAGES"));

// ---- Catalogs
const PLURAL = /_(zero|one|two|few|many|other)$/;
function flatten(value, prefix, out, file) {
  if (typeof value === "string") { out.set(prefix, value); return out; }
  if (!value || typeof value !== "object" || Array.isArray(value)) { errors.push(`${file}: "${prefix}" must be a string or an object of strings`); return out; }
  for (const [key, child] of Object.entries(value)) {
    if (key.includes(".") || key.includes(":")) errors.push(`${file}: key segment "${key}" must not contain "." or ":"`);
    flatten(child, prefix ? `${prefix}.${key}` : key, out, file);
  }
  return out;
}
function readCatalog(language, namespace) {
  const file = join(localesDir, language, `${namespace}.json`);
  const name = relative(ui, file);
  if (!existsSync(file)) { errors.push(`${name}: missing (every language has every namespace; use {} when empty)`); return new Map(); }
  try { return flatten(JSON.parse(readFileSync(file, "utf8")), "", new Map(), name); }
  catch (cause) { errors.push(`${name}: invalid JSON (${cause.message})`); return new Map(); }
}
const variables = text => [...text.matchAll(/\{\{\s*([^,}\s]+)[^}]*\}\}/g)].map(match => match[1]).sort();
const tags = text => [...text.matchAll(/<\/?([A-Za-z0-9_]+)\s*\/?>/g)].map(match => match[1]).sort();
const nesting = text => [...text.matchAll(/\$t\(([^),]+)/g)].map(match => match[1].trim()).sort();
const same = (a, b) => a.length === b.length && a.every((value, index) => value === b[index]);
const uniq = list => [...new Set(list)].sort();
const CJK = /[\p{sc=Han}\p{sc=Hiragana}\p{sc=Katakana}\p{sc=Hangul}]/u;

/** Group plural forms under their base key: base -> {form -> text}. */
function grouped(catalog) {
  const groups = new Map();
  for (const [key, text] of catalog) {
    const plural = PLURAL.exec(key);
    const base = plural ? key.slice(0, -plural[0].length) : key;
    if (!groups.has(base)) groups.set(base, new Map());
    groups.get(base).set(plural ? plural[1] : "", text);
  }
  return groups;
}

for (const namespace of NAMESPACES) if (!existsSync(join(localesDir, SOURCE, `${namespace}.json`))) errors.push(`src/locales/${SOURCE}/${namespace}.json: missing`);
for (const entry of readdirSync(localesDir)) {
  const path = join(localesDir, entry);
  if (statSync(path).isDirectory() && !LANGUAGES.includes(entry)) errors.push(`src/locales/${entry}: not a language in src/i18n/languages.ts`);
  if (statSync(path).isDirectory()) for (const file of readdirSync(path)) if (file.endsWith(".json") && !NAMESPACES.includes(file.slice(0, -5))) errors.push(`src/locales/${entry}/${file}: not a namespace in src/i18n/languages.ts`);
}

const english = new Map(NAMESPACES.map(namespace => [namespace, grouped(readCatalog(SOURCE, namespace))]));
for (const [namespace, groups] of english) for (const [base, forms] of groups) {
  for (const text of forms.values()) if (CJK.test(text)) errors.push(`en/${namespace}.json: "${base}" contains CJK text; en is the English source`);
  if (forms.has("") && forms.size > 1) errors.push(`en/${namespace}.json: "${base}" is both a plain key and a plural`);
  if (!forms.has("") && !forms.has("other")) errors.push(`en/${namespace}.json: plural "${base}" needs an _other form`);
}

const untranslated = new Map();
for (const language of LANGUAGES.filter(language => language !== SOURCE)) {
  const categories = new Intl.PluralRules(language).resolvedOptions().pluralCategories;
  const missing = [];
  for (const namespace of NAMESPACES) {
    const file = `${language}/${namespace}.json`;
    const source = english.get(namespace), target = grouped(readCatalog(language, namespace));
    for (const [base, forms] of target) {
      const reference = source.get(base);
      if (!reference) { errors.push(`${file}: "${[...forms.keys()].map(form => form ? `${base}_${form}` : base).join(", ")}" is not in en`); continue; }
      const plural = !reference.has("");
      if (plural) {
        if (forms.has("")) errors.push(`${file}: "${base}" must use plural forms (${categories.map(form => `_${form}`).join(", ")})`);
        for (const form of forms.keys()) if (form && !categories.includes(form)) errors.push(`${file}: "${base}_${form}" is not a ${language} plural category (${categories.join(", ")})`);
        const absent = categories.filter(form => !forms.has(form));
        if (absent.length && forms.size) errors.push(`${file}: "${base}" lacks plural forms ${absent.map(form => `_${form}`).join(", ")}`);
      } else if (!forms.has("")) errors.push(`${file}: "${base}" is a plain key in en; drop the plural suffixes`);
      const want = [...reference.values()].join("\n"), have = [...forms.values()].join("\n");
      // Plural forms may omit {{count}} (e.g. "one item"); every other variable must match.
      const vars = list => uniq(list).filter(name => !(plural && name === "count"));
      if (!same(vars(variables(want)), vars(variables(have)))) errors.push(`${file}: "${base}" variables ${JSON.stringify(uniq(variables(have)))} differ from en ${JSON.stringify(uniq(variables(want)))}`);
      if (!same(uniq(tags(want)), uniq(tags(have)))) errors.push(`${file}: "${base}" markup tags ${JSON.stringify(uniq(tags(have)))} differ from en ${JSON.stringify(uniq(tags(want)))}`);
      if (!same(uniq(nesting(want)), uniq(nesting(have)))) errors.push(`${file}: "${base}" $t() references differ from en`);
      for (const text of forms.values()) if (!text.trim()) errors.push(`${file}: "${base}" is empty; delete the key to fall back to en instead`);
    }
    for (const [base, forms] of source) if (!target.has(base)) missing.push({ key: `${namespace}:${base}`, text: forms.get("") ?? forms.get("other") });
  }
  untranslated.set(language, missing);
  if (missing.length && (COMPLETE.has(language) || flag("--strict"))) {
    errors.push(`${language}: ${missing.length} key(s) missing: ${missing.slice(0, 12).map(item => item.key).join(", ")}${missing.length > 12 ? ", …" : ""}`);
  }
}

const listLanguage = option("--untranslated");
if (listLanguage) {
  for (const item of untranslated.get(listLanguage) ?? []) console.log(`${item.key}\t${item.text}`);
  process.exit(0);
}

// ---- Source scan
/** Source with comments blanked out (strings, templates and JSX text kept). */
function stripComments(text) {
  let out = "", state = "code", quote = "", depth = [], previous = "";
  for (let index = 0; index < text.length; index++) {
    const char = text[index], next = text[index + 1];
    if (state === "line") { if (char === "\n") { state = "code"; out += char; } continue; }
    if (state === "block") { if (char === "*" && next === "/") { state = "code"; index++; } else if (char === "\n") out += char; continue; }
    if (state === "string") {
      out += char;
      if (char === "\\") { out += next ?? ""; index++; }
      else if (char === quote || (char === "\n" && quote !== "`")) { state = "code"; previous = char; }
      else if (quote === "`" && char === "$" && next === "{") { out += "{"; index++; depth.push("tpl"); state = "code"; previous = "{"; }
      continue;
    }
    if (state === "regex") {
      out += char;
      if (char === "\\") { out += next ?? ""; index++; }
      else if (char === "[") quote = "[";
      else if (char === "]" && quote === "[") quote = "";
      else if ((char === "/" && quote !== "[") || char === "\n") state = "code";
      continue;
    }
    // code
    if (char === "/" && next === "/") { state = "line"; index++; continue; }
    if (char === "/" && next === "*") { state = "block"; index++; continue; }
    if (char === "/" && (previous === "" || "(,=:[!&|?{};+-*%<>~^".includes(previous))) { state = "regex"; quote = ""; out += char; previous = char; continue; }
    if (char === '"' || char === "`" || (char === "'" && !/[A-Za-z]/.test(text[index - 1] ?? ""))) { state = "string"; quote = char; out += char; previous = char; continue; }
    if (char === "{" && depth.length) depth.push("{");
    if (char === "}" && depth.length) { const top = depth.pop(); if (top === "tpl") { state = "string"; quote = "`"; } }
    out += char;
    if (!/\s/.test(char)) previous = char;
  }
  return out;
}
const CJK_RUN = /[\p{sc=Han}\p{sc=Hiragana}\p{sc=Katakana}\p{sc=Hangul}　-〿！-｠]+/gu;
const SENTENCE = /^[A-Z][a-z'’]+(?:[ -][A-Za-z'’]+)+[.!?…:]?$/;
function englishProse(text) {
  let count = 0;
  for (const match of text.matchAll(/>([^<>{}\n]+)</g)) if (SENTENCE.test(match[1].trim())) count++;
  for (const match of text.matchAll(/\b(?:aria-label|title|placeholder|alt|label|data-hint)="([^"]+)"/g)) if (SENTENCE.test(match[1].trim())) count++;
  return count;
}
function* sourceFiles(dir) {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry), rel = relative(ui, path);
    if (statSync(path).isDirectory()) { if (rel !== "src/locales" && rel !== "src/motion-lab") yield* sourceFiles(path); continue; }
    if (/\.(ts|tsx)$/.test(entry) && !entry.endsWith(".d.ts") && rel !== "src/i18n/languages.ts") yield rel;
  }
}
const counts = {};
for (const file of sourceFiles(src)) {
  const code = stripComments(readFileSync(join(ui, file), "utf8"));
  const cjk = (code.match(CJK_RUN) ?? []).length, english = englishProse(code);
  if (cjk || english) counts[file] = { cjk, english };
}
const baseline = existsSync(baselinePath) ? JSON.parse(readFileSync(baselinePath, "utf8")).files ?? {} : {};
const shrinkable = [];
for (const file of new Set([...Object.keys(counts), ...Object.keys(baseline)])) {
  const now = counts[file] ?? { cjk: 0, english: 0 }, allowed = baseline[file] ?? { cjk: 0, english: 0 };
  for (const kind of ["cjk", "english"]) {
    if (now[kind] > (allowed[kind] ?? 0)) errors.push(`${file}: ${now[kind]} hardcoded ${kind === "cjk" ? "CJK text run(s)" : "English UI phrase(s)"} (baseline ${allowed[kind] ?? 0}); move the text to src/locales`);
  }
  if (now.cjk < (allowed.cjk ?? 0) || now.english < (allowed.english ?? 0)) shrinkable.push(file);
}
if (flag("--update-baseline")) {
  const raised = Object.entries(counts).filter(([file, now]) => now.cjk > (baseline[file]?.cjk ?? 0) || now.english > (baseline[file]?.english ?? 0));
  if (raised.length && !flag("--force")) { console.error(`refusing to raise the baseline for: ${raised.map(([file]) => file).join(", ")} (pass --force only for a reviewed exception)`); process.exit(1); }
  const files = Object.fromEntries(Object.entries(counts).sort(([a], [b]) => a.localeCompare(b)));
  // One line per file keeps parallel ratchets mergeable.
  const lines = Object.entries(files).map(([file, item]) => `    ${JSON.stringify(file)}: { "cjk": ${item.cjk}, "english": ${item.english} }`);
  writeFileSync(baselinePath, `{\n  "note": "Ceiling of hardcoded UI text per file. Only ever shrinks; regenerate with npm run test:i18n -- --update-baseline.",\n  "files": {\n${lines.join(",\n")}\n  }\n}\n`);
  console.log(`baseline updated: ${Object.keys(files).length} file(s), ${Object.values(files).reduce((sum, item) => sum + item.cjk, 0)} CJK run(s), ${Object.values(files).reduce((sum, item) => sum + item.english, 0)} English phrase(s)`);
  process.exit(errors.filter(error => !/hardcoded/.test(error)).length ? 1 : 0);
}

// ---- Report
const totals = Object.values(counts).reduce((sum, item) => ({ cjk: sum.cjk + item.cjk, english: sum.english + item.english }), { cjk: 0, english: 0 });
const keyCount = [...english.values()].reduce((sum, groups) => sum + groups.size, 0);
console.log(`catalogs: ${LANGUAGES.length} languages × ${NAMESPACES.length} namespaces, ${keyCount} en keys`);
for (const [language, missing] of untranslated) console.log(`  ${language.padEnd(5)} ${missing.length ? `${missing.length} untranslated (falls back to English)` : "complete"}${COMPLETE.has(language) ? " [required]" : ""}`);
console.log(`source: ${Object.keys(counts).length} file(s) with hardcoded text, ${totals.cjk} CJK run(s), ${totals.english} English phrase(s)`);
if (shrinkable.length) console.log(`  below baseline (run --update-baseline to ratchet): ${shrinkable.join(", ")}`);
for (const note of notes) console.log(note);
if (errors.length) {
  console.error(`\n${errors.length} i18n error(s):`);
  for (const error of errors) console.error(`  ${error}`);
  process.exit(1);
}
console.log("i18n: PASS");
