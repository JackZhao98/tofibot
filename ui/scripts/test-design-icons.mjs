import assert from "node:assert/strict";
import { readFileSync, readdirSync, mkdtempSync, rmSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { build } from "vite";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const json = path => JSON.parse(readFileSync(join(root, path), "utf8"));
const catalog = json("src/icons/catalog.json");
assert.deepEqual(catalog, json("test-fixtures/design/icons/manifest.json"), "React icon definitions must match the supplied design");
assert.deepEqual(catalog, json("public/icons/manifest.json"), "public exports must share the same definitions");
assert.equal(Object.keys(catalog.icons).length, 144);
// The archive wraps unchanged paths in C2PA provenance metadata and expands
// self-closing tags. Compare the drawing, retaining the supplied files verbatim.
const drawing = svg => svg.replace(/\s+xmlns:c2pa="[^"]*"/g, "").replace(/<metadata>[\s\S]*?<\/metadata>/g, "").replace(/<([\w:-]+)([^>]*?)\s*\/>/g, "<$1$2></$1>").trim();
for (const name of Object.keys(catalog.icons)) {
  for (const suffix of ["", "-filled"]) {
    const file = `svg/${name}${suffix}.svg`;
    assert.equal(drawing(readFileSync(join(root, "public/icons", file), "utf8")), drawing(readFileSync(join(root, "test-fixtures/design/icons", file), "utf8")), file);
  }
}
const output = mkdtempSync(join(root, ".test-design-icons-"));
try {
  await build({ configFile: false, logLevel: "silent", build: { ssr: join(root, "src/icons/index.tsx"), outDir: output, rollupOptions: { output: { entryFileNames: "icons.mjs" } } } });
  const { TofiIcon } = await import(pathToFileURL(join(output, "icons.mjs")));
  for (const [name, definition] of Object.entries(catalog.icons)) {
    for (const variant of ["outline", "filled"]) {
      const html = renderToStaticMarkup(createElement(TofiIcon, { name, variant, size: 20 }));
      assert.match(html, /aria-hidden="true"/);
      assert.match(html, /viewBox="0 0 24 24"/);
      assert.match(html, /stroke="currentColor"/);
      assert.match(html, /flex-shrink:0/);
      assert.equal((html.match(/data-tofi-part=/g) || []).length, definition[variant === "filled" ? "filledNodes" : "nodes"].length, `${name} ${variant}`);
    }
  }
  const titled = renderToStaticMarkup(createElement(TofiIcon, { name: "bot", title: "研究助理" }));
  assert.match(titled, /role="img"/);
  assert.match(titled, /aria-labelledby=/);
  assert.doesNotMatch(titled, /aria-hidden/);
  assert.match(titled, /<title[^>]+>研究助理<\/title>/);
} finally { rmSync(output, { recursive: true, force: true }); }

function inspect(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) { if (entry.name !== "lib" && entry.name !== "icons") inspect(path); continue; }
    if (!path.endsWith(".tsx")) continue;
    const source = readFileSync(path, "utf8");
    if (entry.name !== "ComposerGlyph.tsx") assert.doesNotMatch(source, /<svg\b/, `Ad-hoc SVG in ${entry.name}`);
    assert.doesNotMatch(source, /from ["']\.\/UiIcon["']/, `Legacy icon adapter in ${entry.name}`);
    assert.doesNotMatch(source, />\s*[✓✕✖▣▧⧉⤢×⌄]\s*</u, `Character icon in ${entry.name}`);
  }
}
inspect(join(root, "src"));
console.log("Tofi icons: 144 definitions, 288 asset/render pairs, accessibility and shared-component guards PASS");
