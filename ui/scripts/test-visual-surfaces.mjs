import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = name => readFileSync(new URL(`../src/${name}`, import.meta.url), "utf8");

test("overlapping surfaces share tinted glass and paired backdrop filters", () => {
  const css = read("v2-app.css");
  // Locate the material rule by its declarations, rather than lazy component file order.
  const material = [...css.matchAll(/([^{}]+)\{([^{}]+)\}/g)].find(([, selectors, body]) => selectors.includes(".sheet,") && body.includes("background:var(--legacy-glass)"));
  assert.ok(material, "one shared surface recipe exists");
  for (const name of ["sidebar-account-menu", "more-menu", "mention-menu", "composer-add-menu", "ui-select-content", "cover-window", "sheet", "search-palette", "desktop-window-controls"]) {
    assert.ok(material[1].includes(`.${name}`), `${name} uses the shared material`);
  }
  assert.match(material[2], /-webkit-backdrop-filter:var\(--blur\)/);
  assert.match(material[2], /(?<!-webkit-)backdrop-filter:var\(--blur\)/);
  // The material is a skin role now (V3 PLAN 7.6): the bridge points the V2 glass names at --glass-strong, and the blur at --glass-blur.
  const bridge = read("skin/v2-bridge.css");
  assert.match(bridge, /--legacy-glass:\s*var\(--glass-strong\)/, "the V2 glass is the skin's strong glass");
  assert.match(bridge, /--blur:\s*var\(--glass-blur\)/, "the V2 blur is the skin's glass blur");
  // Both themes obscure the content beneath the glass: the strong glass is at least 84% opaque, the blur is a real blur.
  const skin = read("skin/v3.css");
  const alphas = [...skin.matchAll(/--glass-strong:\s*rgba\([^)]*,\s*(\.?\d+(?:\.\d+)?)\)/g)].map(match => Number(match[1]));
  assert.equal(alphas.length, 2, "both themes define the strong glass");
  for (const alpha of alphas) assert.ok(alpha >= .84, `the strong glass hides what is behind it (alpha ${alpha})`);
  assert.match(skin, /--glass-blur:\s*blur\(\d+px\)/, "the skin's glass blurs");
  assert.doesNotMatch(read("desktop-system.css"), /background:color-mix\([^;]+88%,transparent\)/, "desktop does not weaken the menu material");
});

test("popup keyframes fade in place without corner origins or translation", () => {
  for (const file of ["interaction-system.css", "search-palette.css", "permission-coach.css"]) {
    for (const [, name, body] of read(file).matchAll(/@keyframes ([\w-]+)\s*\{((?:[^{}]|\{[^{}]*\})*)\}/g)) {
      if (/^(menu-|modal-|drawer-|search-panel-|delete-dialog-arrive|permission-coach-in)/.test(name)) {
        assert.doesNotMatch(body, /translate|transform|scale/, `${file}: ${name} stays in place`);
        assert.match(body, /opacity/, `${file}: ${name} fades`);
      }
    }
  }
  assert.doesNotMatch(read("FloatingDesktop.tsx"), /transformOrigin:/, "mini window never flies from a corner");
  assert.doesNotMatch(read("AttachmentLightbox.tsx"), /transform:|translate\(/, "image dialog opens in place");
  assert.match(read("v2-app.css"), /prefers-reduced-motion:reduce\)\s*\{\s*(?:\.tofi-pop\[data-state\],\s*)?:root:root[^{}]+\{ animation:none;/, "reduced motion overrides closed-state selectors too");
});

test("native title row controls clear traffic lights and use no-drag hit targets", () => {
  const css = read("desktop-system.css");
  // Option C (owner 2026-11-14): the toggle sits at 78 px inside the card (86 px in the window), after the lights at 22 px.
  assert.match(css, /\.sidebar-top :is\(\.sidebar-toggle-slot,\.sidebar-logo-expand,\.search-slot,\.create-menu-anchor\) \{[^}]*left:78px/);
  // Every app-region declaration lives in forced.css since PR 0.3, so the no-drag hole is read there.
  assert.match(read("forced.css"), /\.sidebar-top :is\(\.sidebar-toggle-slot,\.sidebar-logo-expand,\.search-slot,\.create-menu-anchor\)\{-webkit-app-region:no-drag\}/);
  // Collapsed or narrow: the header drag layer starts after the capsule.
  assert.match(css, /:is\(\.sidebar-collapsed,\.mac-narrow\) \.chat-header > \.chat-header-drag \{ left:200px/);
});
