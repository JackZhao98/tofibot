import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = name => readFileSync(new URL(`../src/${name}`, import.meta.url), "utf8");

test("overlapping surfaces share tinted glass and paired backdrop filters", () => {
  const css = read("v2-app.css");
  // Locate the material rule by its declarations, rather than lazy component file order.
  const material = [...css.matchAll(/([^{}]+)\{([^{}]+)\}/g)].find(([, selectors, body]) => selectors.includes(".sheet,") && body.includes("background:var(--glass)"));
  assert.ok(material, "one shared surface recipe exists");
  for (const name of ["sidebar-account-menu", "more-menu", "mention-menu", "composer-add-menu", "ui-select-content", "cover-window", "sheet", "search-palette", "desktop-window-controls"]) {
    assert.ok(material[1].includes(`.${name}`), `${name} uses the shared material`);
  }
  assert.match(material[2], /-webkit-backdrop-filter:var\(--blur\)/);
  assert.match(material[2], /(?<!-webkit-)backdrop-filter:var\(--blur\)/);
  const tokens = read("design-tokens.css");
  assert.equal([...tokens.matchAll(/--glass:rgba\([^;]+,\.96\)/g)].length, 2, "both themes obscure content beneath the glass");
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
  assert.match(read("v2-app.css"), /prefers-reduced-motion:reduce\)\s*\{\s*:root:root[^{}]+\{ animation:none;/, "reduced motion overrides closed-state selectors too");
});

test("native collapsed controls clear traffic lights and use no-drag hit targets", () => {
  const css = read("desktop-system.css");
  assert.match(css, /\.sidebar-top\{[^}]*left:78px/);
  assert.match(css, /\.sidebar-top button\{[^}]*-webkit-app-region:no-drag/);
  // Collapsed: one title row; the header drag layer starts after the rail buttons.
  assert.match(css, /\.sidebar-collapsed \.chat-header>\.chat-header-drag\{left:calc\(206px/);
});
