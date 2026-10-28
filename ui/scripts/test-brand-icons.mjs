// Brand icons: every directory service wears its real vendored mark, with provenance on file.
import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { openUiModules, uiRoot } from "./ui-modules.mjs";

const repo = path.dirname(uiRoot);
const iconsDir = path.join(repo, "assets/brand-icons");
const manifest = JSON.parse(fs.readFileSync(path.join(iconsDir, "manifest.json"), "utf8"));
const sha = file => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
const publicFile = url => path.join(uiRoot, "public", url);

const luminance = hex => {
  const [r, g, b] = [0, 2, 4].map(i => parseInt(hex.slice(i, i + 2), 16) / 255).map(v => v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (a, b) => { const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };

const ui = await openUiModules({ language: "zh-CN" });
try {
  const { integrationCatalog, heldIntegrations } = await ui.load("/src/integrationCatalog.ts");
  const { brandIcons, resolveBrandIcon } = await ui.load("/src/brandIcons.ts");
  const { ServiceMark } = await ui.load("/src/ServiceMark.tsx");

  // 1. Every catalog id has a registry entry.
  const ids = [...integrationCatalog.map(item => item.id), ...heldIntegrations.map(item => item.id)];
  assert.ok(ids.length >= 18, `catalog unexpectedly small: ${ids.length}`);
  for (const id of ids) assert.ok(brandIcons.some(item => item.ids.includes(id)), `no brand registry entry for catalog id "${id}"`);
  const keys = brandIcons.map(item => item.key);
  assert.equal(new Set(keys).size, keys.length, "duplicate registry key");

  // 2. Every referenced asset exists and is non-empty; 3. the manifest covers it; 4. hashes agree.
  const entries = new Map(manifest.assets.map(item => [item.name, item]));
  assert.ok(!/not wired/i.test(manifest.delivery), "manifest delivery line is stale");
  for (const icon of brandIcons) {
    assert.ok(icon.src.startsWith("/brand/services/"), `${icon.key}: assets are served locally, got ${icon.src}`);
    const file = publicFile(icon.src);
    assert.ok(fs.existsSync(file) && fs.statSync(file).size > 0, `${icon.key}: missing or empty asset ${icon.src}`);
    const name = path.basename(icon.src);
    const entry = entries.get(name);
    assert.ok(entry, `${icon.key}: manifest.json has no entry for ${name}`);
    assert.ok(entry.source && /^https:\/\//.test(entry.source), `${name}: manifest source missing`);
    assert.ok(/^[0-9a-f]{64}$/.test(entry.sha256 ?? ""), `${name}: manifest sha256 missing`);
    assert.equal(entry.runtime, "brand/services/" + name, `${name}: manifest runtime path`);
    assert.ok(entry.license, `${name}: manifest license missing`);
    assert.equal(sha(path.join(iconsDir, name)), entry.sha256, `${name}: audit copy differs from manifest sha256`);
    assert.equal(sha(file), entry.sha256, `${name}: runtime copy differs from manifest sha256 (audit copy is the source of truth)`);
    if (entry.modified) {
      assert.ok(entry.recolor && entry.modification && entry.upstream_sha256, `${name}: modified assets must record recolor, modification and the upstream hash`);
      assert.notEqual(entry.upstream_sha256, entry.sha256, `${name}: marked modified but identical to upstream`);
    }
    // Contrast: a single-color SVG mark must clear 3:1 (WCAG graphical objects) on its tile in both themes.
    if (name.endsWith(".svg")) {
      const text = fs.readFileSync(file, "utf8");
      const fills = new Set([...text.matchAll(/fill="#([0-9a-fA-F]{6})"/g)].map(match => match[1]));
      if (fills.size === 1 && entry.kit === "simple-icons") {
        const [fill] = fills;
        const tile = icon.tile === "dark" ? "111111" : "ffffff";
        assert.ok(contrast(fill, tile) >= 3, `${icon.key}: ${fill} on ${tile} is ${contrast(fill, tile).toFixed(2)}:1`);
      } else if (entry.kit === "simple-icons") {
        const tile = icon.tile === "dark" ? "111111" : "ffffff";
        assert.ok(contrast("000000", tile) >= 3, `${icon.key}: default black mark on ${tile}`);
      }
    }
  }
  // Orphans: every manifest asset that claims a runtime copy must be served.
  for (const entry of manifest.assets) if (entry.runtime) assert.ok(fs.existsSync(path.join(uiRoot, "public", entry.runtime)), `${entry.name}: runtime copy missing`);

  // 5. ServiceMark never falls back to generic for a directory service, in either shipped language.
  const render = (name, id) => renderToStaticMarkup(createElement(ServiceMark, { name, id }));
  for (const language of ["zh-CN", "en"]) {
    await ui.setLanguage(language);
    for (const item of [...integrationCatalog, ...heldIntegrations]) {
      const html = render(item.name);
      assert.ok(!html.includes('data-mark="generic"'), `${language}: "${item.name}" (${item.id}) renders the generic fallback`);
      assert.ok(html.includes('<img') && html.includes('alt=""'), `${language}: "${item.name}" has no decorative brand image`);
      assert.ok(!html.includes("filter"), "no CSS filter recoloring");
      const byId = resolveBrandIcon("whatever", item.id);
      assert.ok(byId, `${item.id}: id lookup`);
    }
  }
  assert.ok(render("Gmail").includes('data-mark="gmail"'), "the local Gmail row wears the Gmail mark");
  assert.ok(render("Google Gmail").includes('data-mark="gmail"'));
  assert.ok(render("My own server").includes('data-mark="generic"'), "an unknown custom service keeps the generic glyph");
  assert.ok(!/service-mark-letter/.test(fs.readFileSync(path.join(uiRoot, "src/ServiceMark.tsx"), "utf8")), "letter monograms are gone");
  console.log(`Brand icons: PASS (${ids.length} directory services, ${brandIcons.length} registry marks, manifest + sha256 + contrast verified)`);
} finally {
  await ui.close();
}
