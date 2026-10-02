import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";

// Keep the machine-readable export, design CSS and runtime in agreement.
const reference = readFileSync(new URL("../test-fixtures/design/tokens/colors.css", import.meta.url), "utf8");
const exported = JSON.parse(readFileSync(new URL("../test-fixtures/design/tokens/tokens.json", import.meta.url), "utf8"));
const css = readFileSync(new URL("../src/design-tokens.css", import.meta.url), "utf8");
function declarations(source, selector) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const body = source.match(new RegExp(`^${escaped}\\s*\\{([\\s\\S]*?)^\\}`, "m"))?.[1];
  if (!body) throw new Error(`Missing ${selector} token block`);
  return Object.fromEntries([...body.matchAll(/--([\w-]+)\s*:\s*([^;]+);/g)].map(([, name, value]) => [name, value.trim()]));
}

const base = declarations(css, ":root");
const referenceBase = declarations(reference, ":root");
const modes = {
  light: { actual: base, expected: { ...referenceBase, ...declarations(reference, ".light") } },
  dark: { actual: { ...base, ...declarations(css, ':root[data-theme="dark"]') }, expected: referenceBase },
};
assert.deepEqual(exported.color.themes.map(theme => theme.id).sort(), Object.keys(modes).sort(), "JSON themes must match CSS modes");
const exportedColors = Object.fromEntries(exported.color.tokens.map(token => [token.name, token.value]));
assert.equal(Object.keys(exportedColors).length, exported.color.tokens.length, "Duplicate JSON color token");
const expectedNames = [...new Set(Object.values(modes).flatMap(mode => Object.keys(mode.expected)))].sort();
assert.deepEqual(Object.keys(exportedColors).sort(), expectedNames, "JSON must export every CSS color token and alias exactly once");
for (const [name, value] of Object.entries(exportedColors)) {
  assert.deepEqual(Object.keys(value).sort(), Object.keys(modes).sort(), `JSON --${name} must define both modes`);
}
function resolve(values, name, seen = new Set()) {
  assert.ok(values[name], `Missing --${name}`);
  assert.ok(!seen.has(name), `Circular token --${name}`);
  return values[name].replace(/var\(--([\w-]+)\)/g, (_, dependency) => resolve(values, dependency, new Set([...seen, name])))
    .replace(/0\.(?=\d)/g, ".").replace(/\s+/g, "").toLowerCase();
}
function luminance(hex) {
  const rgb = hex.slice(1).match(/../g).map(byte => parseInt(byte, 16) / 255).map(v => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4);
  return rgb[0] * .2126 + rgb[1] * .7152 + rgb[2] * .0722;
}
function contrast(a, b) {
  const [lo, hi] = [luminance(a), luminance(b)].sort((x, y) => x - y);
  return (hi + .05) / (lo + .05);
}
for (const [theme, { actual, expected }] of Object.entries(modes)) {
  const exportedMode = Object.fromEntries(Object.entries(exportedColors).map(([name, value]) => [name, value[theme]]));
  for (const name of Object.keys(expected)) {
    assert.equal(resolve(exportedMode, name), resolve(expected, name), `JSON ${theme} --${name}`);
    assert.equal(resolve(actual, name), resolve(expected, name), `${theme} --${name}`);
  }
  for (const hue of ["clay", "iris", "lagoon", "honey", "green", "danger"]) {
    for (const surface of ["bg", "surface", "surface-2"]) {
      const ratio = contrast(resolve(actual, `${hue}-text`), resolve(actual, surface));
      assert.ok(ratio >= 4.5, `${theme} ${hue}-text on ${surface}: ${ratio.toFixed(2)}:1`);
    }
    assert.ok(contrast(resolve(actual, "on-block"), resolve(actual, hue)) >= 4.5, `${theme} on-block on ${hue}`);
  }
  assert.equal(resolve(actual, "work-status-ink"), resolve(actual, "lagoon-text"));
  assert.equal(resolve(actual, "on-danger"), resolve(actual, "on-block"));
}

// Guard against a component painting text with a fill token in either mode.
const sourceRoot = new URL("../src/", import.meta.url).pathname;
function inspect(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) { if (entry.name !== "lib") inspect(path); continue; }
    if (!entry.name.endsWith(".css") || entry.name === "design-tokens.css") continue;
    assert.doesNotMatch(readFileSync(path, "utf8"), /(?<![\w-])color\s*:\s*var\(--(?:clay|iris|lagoon|honey|yellow|pink|blue|orange|green|danger|status-error|status-working|status-done|status-unread)\)/, `Fill token used as foreground in ${relative(sourceRoot, path)}`);
  }
}
inspect(sourceRoot);
console.log(`final V2 tokens: ${expectedNames.length} JSON/CSS/runtime colors in both modes, 36 foreground contrast pairs, 12 fill pairs and foreground-use guard PASS`);
