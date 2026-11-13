// Isolation guard for the desktop app's Mac settings entry (desktop-settings.html -> src/desktop-settings/main.tsx).
// The desktop app gives the privileged IPC only to that page, so the page must not be able to render server content or
// talk to the server. This walks the entry's static and dynamic import graph and fails on:
//   - api.ts, OwnerSession, MessageMarkdown, anything under chat/ or artifacts/ (and anything that imports them)
//   - fetch, XMLHttpRequest, EventSource, WebSocket, sendBeacon, dangerouslySetInnerHTML in any file of the graph
//   - page storage (localStorage, sessionStorage, indexedDB, document.cookie) in the page's own files; in shared files
//     only the validated language preference helper (i18n/index.ts) may read it
//   - packages outside react, react-dom, i18next, react-i18next and @radix-ui/*
//   - useAppearance anywhere in the graph, and storage in InteractionSystem.tsx outside useAppearance
//   - an import() or import.meta.glob() it cannot follow (a non-literal path would hide part of the graph)
// It then builds the entry with its real config and checks the output: dist/desktop-settings.html exists, its scripts and
// styles exist, and no built file carries an /api/ URL. No browser is needed.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const entry = join(src, "desktop-settings", "main.tsx");

const FORBIDDEN_FILE = [/^api\.tsx?$/, /^OwnerSession\.tsx?$/, /^MessageMarkdown\.tsx?$/, /^chat\//, /^artifacts\//];
const FORBIDDEN_CODE = [
  [/\bfetch\s*\(/, "fetch"], [/\bXMLHttpRequest\b/, "XMLHttpRequest"], [/\bEventSource\b/, "EventSource"],
  [/\bWebSocket\b/, "WebSocket"], [/\bsendBeacon\b/, "sendBeacon"], [/dangerouslySetInnerHTML\s*[=:]|\binnerHTML\s*=/, "dangerouslySetInnerHTML or innerHTML"],
];
const STORAGE = /\b(localStorage|sessionStorage|indexedDB)\b|document\.cookie/;
const PACKAGES = [/^react$/, /^react\/jsx(-dev)?-runtime$/, /^react-dom(\/.*)?$/, /^i18next$/, /^react-i18next$/, /^@radix-ui\/.+$/];
const SHARED_STORAGE = new Set(["i18n/index.ts"]); // the validated language preference helper
const SOURCE = /\.(tsx?|mts|m?js)$/;

const stripComments = text => text.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:"'`\\])\/\/.*$/gm, "$1");
const rel = file => relative(src, file).split("\\").join("/");

function resolveLocal(from, spec) {
  const base = resolve(dirname(from), spec);
  for (const candidate of [base, `${base}.ts`, `${base}.tsx`, join(base, "index.ts"), join(base, "index.tsx")]) {
    if (existsSync(candidate) && statSync(candidate).isFile()) return candidate;
  }
  return null;
}

const seen = new Map(); // file -> importer
const packages = new Set();
const queue = [[entry, "(entry)"], [join(src, "desktop-bot-browser", "main.tsx"), "(entry)"]];
while (queue.length) {
  const [file, importer] = queue.pop();
  if (seen.has(file)) continue;
  seen.set(file, importer);
  if (!SOURCE.test(file)) continue;
  const text = stripComments(readFileSync(file, "utf8"));
  const specs = [];
  for (const m of text.matchAll(/(?:^|[\n;])\s*(?:import|export)\s+(type\s+)?(?:[^"'`;]*?\sfrom\s+)?["']([^"']+)["']/g)) if (!m[1]) specs.push(m[2]);
  const dynamic = [...text.matchAll(/\bimport\s*\(\s*["']([^"']+)["']\s*\)/g)];
  for (const m of dynamic) specs.push(m[1]);
  // A dynamic import this walk cannot read would hide its target: fail rather than skip it.
  assert.equal((text.match(/\bimport\s*\(/g) || []).length, dynamic.length, `${rel(file)}: an import() with a non-literal path; the isolation walk cannot follow it`);
  // The i18n loader globs every language bundle: pull those in as dynamic imports. No other glob is followed.
  const localesGlob = /import\.meta\.glob\(\s*["']\.\.\/locales\/\*\/index\.ts["']/g;
  const globs = (text.match(/import\.meta\.glob\s*\(/g) || []).length, known = (text.match(localesGlob) || []).length;
  assert.equal(globs, known, `${rel(file)}: an import.meta.glob() other than the locales bundle; the isolation walk cannot follow it`);
  if (known && rel(file) !== "i18n/index.ts") assert.fail(`${rel(file)}: only i18n/index.ts may glob the locales`);
  if (known) {
    for (const lang of readdirSync(join(src, "locales"))) if (existsSync(join(src, "locales", lang, "index.ts"))) specs.push(`../locales/${lang}/index.ts`);
  }
  for (const spec of specs) {
    if (/\.(css|json|svg|png|woff2?)(\?.*)?$/.test(spec)) continue;
    if (spec.startsWith(".")) {
      const target = resolveLocal(file, spec);
      assert.ok(target, `${rel(file)}: cannot resolve ${spec}`);
      queue.push([target, file]);
    } else packages.add(spec);
  }
}

const files = [...seen.keys()].filter(file => SOURCE.test(file));
const own = files.filter(file => /^(desktop-settings|desktop-bot-browser)\//.test(rel(file)));
const failures = [];
for (const file of files) {
  const name = rel(file);
  if (FORBIDDEN_FILE.some(pattern => pattern.test(name))) failures.push(`${name} is in the import graph (imported by ${rel(seen.get(file)) || "entry"})`);
  const text = stripComments(readFileSync(file, "utf8"));
  for (const [pattern, label] of FORBIDDEN_CODE) if (pattern.test(text)) failures.push(`${name} uses ${label}`);
  if (STORAGE.test(text)) {
    if (own.includes(file)) failures.push(`${name} touches page storage`);
    else if (!SHARED_STORAGE.has(name)) {
      // Shared UI that merely contains a storage helper must not be called from the page: only InteractionSystem's
      // useAppearance (not used here) is tolerated, only while its storage stays inside that function and nothing in
      // the graph calls it (checked below).
      if (name !== "InteractionSystem.tsx") failures.push(`${name} touches page storage and is not an allowed helper`);
      else {
        const lines = text.split("\n"), start = lines.findIndex(line => /^export function useAppearance\(/.test(line));
        let end = lines.findIndex((line, index) => index > start && /^(export |function |const |let |class )/.test(line));
        if (end < 0) end = lines.length;
        lines.forEach((line, index) => {
          if (STORAGE.test(line) && (start < 0 || index < start || index >= end)) failures.push(`${name}:${index + 1} touches page storage outside useAppearance`);
        });
      }
    }
  }
}
for (const spec of packages) if (!PACKAGES.some(pattern => pattern.test(spec))) failures.push(`package ${spec} is not allowed`);
for (const file of files) {
  // useAppearance reads page storage and calls workspace-only IPC. Its definition is the one mention allowed.
  const text = stripComments(readFileSync(file, "utf8"));
  const mentions = (text.match(/\buseAppearance\b/g) || []).length;
  if (mentions > (rel(file) === "InteractionSystem.tsx" ? 1 : 0)) failures.push(`${rel(file)} uses useAppearance`);
}
for (const file of own) {
  const text = stripComments(readFileSync(file, "utf8"));
  // A runtime import of ../desktop would register workspace IPC listeners; the page may only import its types.
  if (/(?:^|\n)\s*import\s+(?!type\b)[^;]*from\s+["']\.\.\/desktop["']/.test(text)) failures.push(`${rel(file)} imports ../desktop at runtime`);
}
// The i18n helper may read exactly one key: the language preference.
const i18nSource = readFileSync(join(src, "i18n", "index.ts"), "utf8");
assert.match(i18nSource, /const STORAGE_KEY = "tofi\.language"/, "i18n/index.ts storage key");
const reads = [...i18nSource.matchAll(/localStorage\??\.(\w+)\(([^)]*)\)/g)].map(m => `${m[1]}(${m[2].trim()})`);
assert.deepEqual([...new Set(reads)].sort(), ["getItem(STORAGE_KEY)", "setItem(STORAGE_KEY, preference)"], "i18n/index.ts may use storage only for the language preference");
// Nothing in the workspace may pull the privileged page's code in.
for (const file of (function walk(dir) { return readdirSync(dir, { withFileTypes: true }).flatMap(e => e.isDirectory() ? (e.name === "desktop-settings" || e.name === "locales" ? [] : walk(join(dir, e.name))) : [join(dir, e.name)]); })(src)) {
  if (SOURCE.test(file) && /["']\.{1,2}\/(\.\.\/)*desktop-settings\//.test(readFileSync(file, "utf8"))) failures.push(`${rel(file)} imports the desktop-settings entry`);
}
assert.deepEqual(failures, [], `desktop settings isolation violated:\n  ${failures.join("\n  ")}`);
assert.ok(own.some(file => rel(file) === "desktop-settings/MacSettingsPage.tsx"), "the page is part of the graph");
console.log(`PASS import graph: ${files.length} modules, ${own.length} own, packages ${[...packages].sort().join(", ")}`);

// ---- The build: package.json builds the entry, and the entry's own config produces the page.
const pkg = JSON.parse(readFileSync(join(ui, "package.json"), "utf8"));
assert.match(pkg.scripts.build, /vite build --config vite\.desktop-pages\.config\.ts\s*$/, "npm run build ends with the desktop-settings build");
const out = mkdtempSync(join(tmpdir(), "tofi-desktop-settings-"));
try {
  execFileSync("npx", ["vite", "build", "--config", "vite.desktop-pages.config.ts", "--outDir", out, "--emptyOutDir", "--logLevel", "error"], { cwd: ui, stdio: "inherit" });
  const htmlPath = join(out, "desktop-settings.html");
  assert.ok(existsSync(htmlPath), "dist/desktop-settings.html exists after the build");
  assert.ok(existsSync(join(out, "desktop-bot-browser.html")), "desktop Bot browser entry is built");
  assert.ok(!existsSync(join(out, "index.html")), "the second build does not emit the workspace index.html");
  const html = readFileSync(htmlPath, "utf8");
  const refs = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+)"/g)].map(m => m[1]);
  assert.ok(refs.some(ref => ref.endsWith(".js")) && refs.some(ref => ref.endsWith(".css")), "the page references its script and stylesheet");
  for (const ref of refs) assert.ok(existsSync(join(out, ref)), `${ref} is built`);
  assert.ok(!/manifest\.webmanifest|serviceWorker/.test(html), "the page carries no PWA tags");
  const built = readdirSync(join(out, "assets")).filter(name => /\.(js|css)$/.test(name));
  for (const name of built) {
    const text = readFileSync(join(out, "assets", name), "utf8");
    assert.ok(!/["'`]\/api\//.test(text), `${name} contains an /api/ URL`);
  }
  console.log(`PASS build: desktop-settings.html + ${built.length} assets, no /api/ URL`);
} finally { rmSync(out, { recursive: true, force: true }); }
