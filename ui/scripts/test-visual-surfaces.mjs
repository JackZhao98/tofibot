import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = name => readFileSync(new URL(`../src/${name}`, import.meta.url), "utf8");

test("overlapping surfaces share tinted glass and paired backdrop filters", () => {
  // Each V2 overlay that is not on the kit Dialog draws its own glass in its own sheet; the kit dialogs get theirs from tb-glass.
  const recipe = (file, name) => {
    const css = read(file).replace(/\/\*[\s\S]*?\*\//g, "");
    const rule = [...css.matchAll(/([^{}]+)\{([^{}]+)\}/g)].find(([, selectors, body]) => selectors.split(",").some(part => part.trim() === `.${name}`) && body.includes("background:var(--glass-strong)"));
    assert.ok(rule, `${file}: .${name} draws the shared glass`);
    assert.match(rule[2], /-webkit-backdrop-filter:\s*var\(--glass-blur\)/, `${name} blurs`);
    assert.match(rule[2], /(?<!-webkit-)backdrop-filter:\s*var\(--glass-blur\)/, `${name} blurs`);
    return rule;
  };
  for (const [file, name] of [["computer-resources.css", "resource-restart-dialog"], ["layout/settings-page.css", "settings-leave-dialog"], ["permission-coach.css", "permission-coach"], ["conversation-workspace.css", "view-only-chat"]]) recipe(file, name);
  for (const name of ["cover-window", "search-palette"]) assert.match(read(name === "cover-window" ? "CoverShell.tsx" : "SearchPalette.tsx"), /ds\/Dialog\/Dialog/, `${name} is the kit Dialog`);
  // Menus, popovers and the Select list are on the kit's glass since PRs 2.2 and 2.3.
  for (const file of ["overlays/RadixOverlays.tsx", "SelectRadix.tsx"]) assert.match(read(file), /\btb-glass\b/, `${file} uses the kit glass`);
  // The material is a skin role (V3 PLAN 7.6): the sheet reads --glass-strong and --glass-blur directly (the V2 bridge is gone, PR 9.1).
  // Both themes obscure the content beneath the glass: the strong glass is at least 84% opaque, the blur is a real blur.
  const skin = read("skin/v3.css");
  const alphas = [...skin.matchAll(/--glass-strong:\s*rgba\([^)]*,\s*(\.?\d+(?:\.\d+)?)\)/g)].map(match => Number(match[1]));
  assert.equal(alphas.length, 2, "both themes define the strong glass");
  for (const alpha of alphas) assert.ok(alpha >= .84, `the strong glass hides what is behind it (alpha ${alpha})`);
  assert.match(skin, /--glass-blur:\s*blur\(\d+px\)/, "the skin's glass blurs");
  assert.doesNotMatch(read("desktop-system.css"), /background:color-mix\([^;]+88%,transparent\)/, "desktop does not weaken the menu material");
});

test("popup keyframes fade in place without corner origins or translation", () => {
  for (const file of ["styles.css", "search-palette.css", "permission-coach.css"]) {
    for (const [, name, body] of read(file).matchAll(/@keyframes ([\w-]+)\s*\{((?:[^{}]|\{[^{}]*\})*)\}/g)) {
      if (/^(menu-|modal-|drawer-|search-panel-|delete-dialog-arrive|permission-coach-in)/.test(name)) {
        assert.doesNotMatch(body, /translate|transform|scale/, `${file}: ${name} stays in place`);
        assert.match(body, /opacity/, `${file}: ${name} fades`);
      }
    }
  }
  assert.doesNotMatch(read("FloatingDesktop.tsx"), /transformOrigin:/, "mini window never flies from a corner");
  assert.doesNotMatch(read("AttachmentLightbox.tsx"), /transform:|translate\(/, "image dialog opens in place");
  for (const [file, name] of [["computer-resources.css", "resource-restart-dialog"], ["layout/settings-page.css", "settings-leave-dialog"], ["permission-coach.css", "permission-coach"]]) {
    assert.match(read(file), new RegExp(`prefers-reduced-motion:\\s*reduce\\)\\s*\\{[^{}]*\\.${name}\\s*\\{\\s*animation:\\s*none`), `${file}: reduced motion stops the ${name} grow`);
  }
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
