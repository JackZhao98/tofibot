// Layer scale guard (docs/design/tokens.md §9.3-9.5, docs/design/migration.md step 1).
//  1. design-tokens.css defines the 13 --z-* layers and src/layers.ts mirrors them as Z for TS callers.
//  2. In product CSS a z-index is var(--z-*), calc(var(--z-*) + 1 | - 1), or a local integer -1..3 inside a component's own
//     stacking context. Older local values above 3 are a per-file ceiling in scripts/layers-baseline.json that can only
//     shrink (npm run test:layers -- --update-baseline lowers it). TS/TSX never writes a literal z-index (use Z).
//  3. One portal root: index.html carries <div id="tofi-layers"> after #root, overlays portal through layerRoot(), and no
//     product code portals or appends into document.body except the allowlisted non-overlay helpers below.
//  4. Browser (Chromium, production build, synthetic data): #tofi-layers is not a stacking context; a real hover hint lands
//     in it and is topmost at its centre; overlays stacked on one point paint in the order of the scale (drag > tooltip >
//     toast > form sheet > dialogs > cover > menu > popover > panel > pill > chrome > edge blur); and no element with a
//     local (non-layer) z-index competes in the root stacking context except the phone push pair (checked with the
//     new-message pill on screen).
// Usage: PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs npm run test:layers [-- --update-baseline] [-- --static-only]
import { execSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const baselinePath = join(ui, "scripts", "layers-baseline.json");
const flag = name => process.argv.includes(name);
const tracked = pattern => execSync(`git ls-files '${pattern}'`, { cwd: src }).toString().trim().split("\n").filter(file => file && existsSync(join(src, file)));
const skip = file => file.startsWith("motion-lab/") || file.startsWith("lib/tofi-avatar/") || file.includes("acceptance") || file.startsWith("locales/");
const failures = [];
const fail = message => failures.push(message);
const passed = [];

// ---- 1. the scale and its mirror
export const SCALE = { base: 0, raised: 1, "edge-blur": 10, chrome: 20, pill: 30, panel: 40, popover: 50, menu: 60, cover: 70, dialog: 80, toast: 90, tooltip: 100, drag: 110 };
const camel = name => name.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
const tokens = readFileSync(join(src, "design-tokens.css"), "utf8");
const defined = [...tokens.matchAll(/--z-([a-z-]+)\s*:\s*(-?\d+)\s*;/g)].map(m => [m[1], Number(m[2])]);
for (const [name, value] of Object.entries(SCALE)) {
  const found = defined.filter(([n]) => n === name);
  if (found.length !== 1 || found[0][1] !== value) fail(`design-tokens.css: --z-${name} must be defined once as ${value} (found ${found.map(f => f[1]).join(", ") || "none"})`);
}
for (const [name] of defined) if (!(name in SCALE)) fail(`design-tokens.css: --z-${name} is not in the layer scale (docs/design/tokens.md §9.3)`);
const mirror = existsSync(join(src, "layers.ts")) ? readFileSync(join(src, "layers.ts"), "utf8") : (fail("src/layers.ts is missing: it mirrors the scale as Z and owns layerRoot()"), "");
for (const [name, value] of Object.entries(SCALE)) {
  const found = new RegExp(`\\b${camel(name)}:\\s*(-?\\d+)`).exec(mirror);
  if (!found || Number(found[1]) !== value) fail(`layers.ts: Z.${camel(name)} must be ${value}`);
}
if (!failures.length) passed.push("the 13 --z-* layers are defined once and layers.ts mirrors them");

// ---- 2. no literal layers
const strip = text => text.replace(/\/\*[\s\S]*?\*\//g, " ");
const LAYER = `var\\(--z-(?:${Object.keys(SCALE).join("|")})\\)`;
const allowed = new RegExp(`^(?:${LAYER}|calc\\(\\s*${LAYER}\\s*[+-]\\s*1\\s*\\)|-1|0|1|2|3|auto)$`);
export function cssViolations(text) {
  const out = [];
  for (const match of strip(text).matchAll(/(?<![\w-])z-index\s*:\s*([^;{}]+)/g)) {
    const value = match[1].replace(/\s*!important\s*$/, "").trim();
    if (!allowed.test(value)) out.push(value);
  }
  return out;
}
export function scriptViolations(text) {
  const code = text.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/(^|[^:])\/\/[^\n]*/g, "$1");
  return [...code.matchAll(/\bzIndex\s*(?::|=)\s*["'`]?\s*-?\d|z-index\s*:\s*-?\d/g)].map(m => m[0]);
}
const counts = {};
for (const file of tracked("*.css")) if (!skip(file) && file !== "design-tokens.css") { const n = cssViolations(readFileSync(join(src, file), "utf8")).length; if (n) counts[`src/${file}`] = n; }
const baseline = existsSync(baselinePath) ? JSON.parse(readFileSync(baselinePath, "utf8")).files ?? {} : {};
if (flag("--update-baseline")) {
  const raised = Object.entries(counts).filter(([file, n]) => n > (baseline[file] ?? 0));
  if (raised.length && existsSync(baselinePath) && !flag("--force")) { console.error(`refusing to raise the baseline for: ${raised.map(([file]) => file).join(", ")}`); process.exit(1); }
  const lines = Object.keys(counts).sort().map(file => `    ${JSON.stringify(file)}: ${counts[file]}`);
  writeFileSync(baselinePath, `{\n  "note": "Ceiling of local z-index values above 3 per file (component-internal stacking that predates the layer scale). Only ever shrinks; regenerate with npm run test:layers -- --update-baseline.",\n  "files": {\n${lines.join(",\n")}\n  }\n}\n`);
  console.log(`baseline updated: ${lines.length} file(s), ${Object.values(counts).reduce((a, b) => a + b, 0)} value(s)`);
}
let literalFailures = 0;
for (const file of Object.keys(counts)) if (counts[file] > (baseline[file] ?? 0) && !flag("--update-baseline")) { literalFailures++; fail(`${file}: ${counts[file]} z-index value(s) outside the layer scale (baseline ${baseline[file] ?? 0}): ${cssViolations(readFileSync(join(ui, file), "utf8")).join(", ")}; use var(--z-*) for a global layer or -1..3 inside the component's own stacking context`); }
for (const file of [...tracked("*.ts"), ...tracked("*.tsx")]) {
  if (skip(file) || file.endsWith(".d.ts")) continue;
  const hits = scriptViolations(readFileSync(join(src, file), "utf8"));
  if (hits.length) { literalFailures++; fail(`src/${file}: literal z-index in script (${hits.join(", ")}); write "var(--z-<layer>)" (or String(Z.<layer>) from layers.ts), or set a class whose CSS uses var(--z-*)`); }
}
const shrinkable = Object.keys(baseline).filter(file => (counts[file] ?? 0) < baseline[file]);
if (!literalFailures) passed.push(`no z-index outside the scale (${Object.values(counts).reduce((a, b) => a + b, 0)} older local value(s) held at the ceiling) and none written from TS`);

// ---- 3. one portal root
let portalFailures = 0;
const html = readFileSync(join(ui, "index.html"), "utf8");
if (!/<div id="root"><\/div>\s*<div id="tofi-layers"><\/div>/.test(html)) { portalFailures++; fail(`index.html: <div id="tofi-layers"></div> must follow <div id="root"></div>`); }
// Body appends that are not overlays: an off-screen caret mirror, the clipboard fallback field, the download anchor.
// proxyChallengeRecover.ts: a hidden, short-lived same-origin frame that renews a proxy clearance; not an overlay.
const BODY_APPEND_ALLOWED = { "textareaCaret.ts": 1, "App.tsx": 1, "botPackage.ts": 1, "layers.ts": 1, "proxyChallengeRecover.ts": 1 };
for (const file of [...tracked("*.ts"), ...tracked("*.tsx")]) {
  if (skip(file)) continue;
  const code = readFileSync(join(src, file), "utf8");
  if (/createPortal\(/.test(code) && /,\s*document\.body\s*\)/.test(code)) { portalFailures++; fail(`src/${file}: createPortal into document.body; portal into layerRoot() from layers.ts`); }
  const appends = (code.match(/document\.body\.(?:append|appendChild|prepend|insertBefore)\(/g) || []).length;
  if (appends > (BODY_APPEND_ALLOWED[file] ?? 0)) { portalFailures++; fail(`src/${file}: ${appends} append(s) into document.body; overlays go into layerRoot() from layers.ts`); }
}
if (!portalFailures) passed.push("index.html carries #tofi-layers and no overlay portals or appends into document.body");

if (failures.length) { for (const message of failures) console.error(`not ok - ${message}`); process.exit(1); }
for (const name of passed) console.log(`ok - ${name}`);
if (shrinkable.length) console.log(`  below baseline (run --update-baseline to ratchet): ${shrinkable.join(", ")}`);
if (flag("--static-only") || flag("--update-baseline")) { console.log("layers: static checks PASS (browser checks not run)"); process.exit(0); }

// ---- 4. the browser
const { createFixture } = await import("./lib/chat-fixture.mjs");
const fixture = await createFixture();
const results = [];
const check = async (name, run) => {
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n").slice(0, 3).join("\n    ")}`); }
};
const truthy = (value, message) => { if (!value) throw new Error(message); };

await check("#tofi-layers follows #root and is not a stacking context", async () => {
  const { page, context, errors } = await fixture.open({ viewport: { width: 1280, height: 820 } });
  const info = await page.evaluate(() => {
    const root = document.getElementById("tofi-layers"), cs = root && getComputedStyle(root);
    return root && { afterRoot: root.previousElementSibling?.id === "root", parent: root.parentElement === document.body, z: cs.zIndex, position: cs.position, isolation: cs.isolation, transform: cs.transform, filter: cs.filter, opacity: cs.opacity, contain: cs.contain, willChange: cs.willChange, backdrop: cs.backdropFilter };
  });
  truthy(info, "no #tofi-layers element");
  truthy(info.afterRoot && info.parent, "#tofi-layers must be the <body> child right after #root");
  const stacking = info.z !== "auto" || info.position !== "static" || info.isolation !== "auto" || info.transform !== "none" || info.filter !== "none" || info.opacity !== "1" || !/^(none|normal)$/.test(info.contain) || info.willChange !== "auto" || (info.backdrop && info.backdrop !== "none");
  truthy(!stacking, `#tofi-layers would form a stacking context and fold every overlay into one layer: ${JSON.stringify(info)}`);
  truthy(!errors.length, errors.join("; "));
  await context.close();
});

await check("a real hover hint portals into #tofi-layers and is topmost at its centre, above a toast", async () => {
  const { page, context, errors } = await fixture.open({ viewport: { width: 1280, height: 820 } });
  await page.evaluate(() => document.querySelector(".workspace").insertAdjacentHTML("beforeend", '<div class="stream-status workspace-status workspace-toast" role="alert" data-probe="toast">probe</div>'));
  const target = page.locator(".chat-header [data-hint]").last();
  await target.hover();
  await page.locator(".action-hint").waitFor({ timeout: 3000 });
  await page.waitForTimeout(250);
  const info = await page.evaluate(() => {
    const hint = document.querySelector(".action-hint"), r = hint.getBoundingClientRect();
    const toast = document.querySelector("[data-probe=toast]");
    Object.assign(toast.style, { left: `${r.left - 20}px`, top: `${r.top - 10}px`, width: `${r.width + 40}px`, height: `${r.height + 20}px`, transform: "none", animation: "none" });
    hint.style.pointerEvents = "auto";
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    const top = document.elementFromPoint(x, y);
    return { parent: hint.parentElement?.id, topIsHint: top === hint || hint.contains(top), top: top?.className };
  });
  truthy(info.parent === "tofi-layers", `the hint was portaled into ${info.parent || "document.body"}, not #tofi-layers`);
  truthy(info.topIsHint, `at the hint's centre the topmost element is "${info.top}", not the hint`);
  truthy(!errors.length, errors.join("; "));
  await context.close();
});

// Synthetic overlays on one point, real CSS: each probe is the real class at its real DOM parent, moved over the point.
const STACK = [
  // [name, parent selector, html, expected layer]
  ["drop-ghost", "#tofi-layers", '<div class="web-drop-ghost">g</div>', "drag"],
  ["hint", "#tofi-layers", '<div class="action-hint" role="tooltip">h</div>', "tooltip"],
  ["toast", ".workspace", '<div class="stream-status workspace-status workspace-toast" role="alert">t</div>', "toast"],
  ["sheet", "#tofi-layers", '<div class="sheet-overlay">s</div>', "dialog+1"],
  ["delete", "#tofi-layers", '<div class="delete-dialog-overlay">d</div>', "dialog"],
  ["onboarding", "#tofi-layers", '<div class="onb-scrim">o</div>', "dialog"],
  ["leave", "#tofi-layers", '<div class="settings-leave-overlay">l</div>', "dialog"],
  ["restart", "#tofi-layers", '<div class="resource-restart-overlay">r</div>', "dialog"],
  ["boot", "#root", '<div class="boot boot-dissolve">b</div>', "cover"],
  ["menu", "#tofi-layers", '<div class="sidebar-context-menu" role="menu">m</div>', "menu"], // a Radix menu since step 2
  ["identity", "#tofi-layers", '<section class="bot-identity-card">i</section>', "popover"],
  ["card", "#tofi-layers", '<div class="view-only-chat is-card">c</div>', "panel"],
  ["floating-computer", ".workspace", '<div class="desktop-floating-shell">f</div>', "pill"],
];
await check("overlays stacked on one point paint in the order of the scale, the settings window among the dialogs", async () => {
  const { page, context, errors } = await fixture.open({ viewport: { width: 1280, height: 820 }, motion: "reduce" });
  await page.evaluate(STACK => {
    for (const [name, parent, html] of STACK) { document.querySelector(parent).insertAdjacentHTML("beforeend", html); document.querySelector(parent).lastElementChild.dataset.probe = name; }
    const pane = document.querySelector(".detail-pane");
    pane.className = "detail-pane visible"; // the settings window: .workspace .detail-pane:has(.settings-tabs)
    pane.removeAttribute("inert"); // inert while no panel is open, and inert boxes are not hit-tested
    pane.insertAdjacentHTML("beforeend", '<div class="settings-tabs"></div>');
    pane.dataset.probe = "settings";
  }, STACK);
  await page.waitForTimeout(400); // reduced motion still runs a 0.01 ms transition on every property
  const order = await page.evaluate(debug => {
    const composer = document.querySelector(".composer"), r = composer.getBoundingClientRect();
    const x = Math.round(r.left + r.width / 2), y = Math.round(r.top + r.height / 2);
    composer.dataset.probe = "composer";
    for (const el of document.querySelectorAll("[data-probe]")) {
      if (el === composer || el.dataset.probe === "settings") { el.style.pointerEvents = "auto"; continue; }
      Object.assign(el.style, { transition: "none", position: "fixed", inset: "auto", left: `${x - 30}px`, top: `${y - 30}px`, width: "60px", height: "60px", right: "auto", bottom: "auto", transform: "none", translate: "none", animation: "none", display: "block", visibility: "visible", opacity: "1", pointerEvents: "auto", contain: "none" });
    }
    const seen = [];
    for (const el of document.elementsFromPoint(x, y)) { const probe = el.closest("[data-probe]")?.dataset.probe; if (probe && !seen.includes(probe)) seen.push(probe); }
    if (debug) return [...seen, "|", ...[...document.querySelectorAll("[data-probe]")].map(el => { const r = el.getBoundingClientRect(), cs = getComputedStyle(el); return `${el.dataset.probe}:${Math.round(r.left)},${Math.round(r.top)},${Math.round(r.width)}x${Math.round(r.height)} ${cs.display}/${cs.visibility}/${cs.pointerEvents}/z${cs.zIndex}/${cs.position}`; })];
    return seen;
  }, Boolean(process.env.LAYERS_DEBUG));
  const expected = ["drop-ghost", "hint", "toast", "sheet", ["delete", "settings", "onboarding", "leave", "restart"], "boot", "menu", "identity", "card", "floating-computer", "composer"];
  const flat = expected.flat();
  for (const name of flat) truthy(order.includes(name), `"${name}" is not hit at the shared point; seen top to bottom: ${order.join(" > ")}`);
  const rank = name => expected.findIndex(entry => entry === name || (Array.isArray(entry) && entry.includes(name)));
  const seenRanks = order.filter(name => flat.includes(name)).map(rank);
  truthy(seenRanks.every((value, index) => index === 0 || value >= seenRanks[index - 1]), `paint order top to bottom is ${order.join(" > ")}; the scale wants ${expected.map(e => Array.isArray(e) ? `(${e.join("|")})` : e).join(" > ")}`);
  truthy(!errors.length, errors.join("; "));
  await context.close();
});

// A local value only means something inside its component's stacking context; out in the root it competes with the layers.
const LOCAL_IN_ROOT_ALLOWED = [
  ["aside.contact-pane", 6, "phone push pair (styles.css:317), local to the list/chat pair; tokens.md §9.4"],
  ["main.chat-pane", 7, "phone push pair (styles.css:322)"],
];
for (const viewport of [{ width: 1280, height: 820 }, { width: 1000, height: 820 }, { width: 390, height: 844 }]) {
  await check(`${viewport.width}px: no local z-index competes in the root stacking context`, async () => {
    const { page, context, errors } = await fixture.open({ viewport });
    // Bring up the new-message pill too: scroll away from the latest message, then let one arrive.
    await page.locator(".message-scroll").hover();
    await page.mouse.wheel(0, -700);
    await page.waitForTimeout(500);
    await page.evaluate(({ WHOLE, at }) => window.__emit("message", { id: "m_layers", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_layers", content: WHOLE, created_at: at }), { WHOLE: fixture.WHOLE, at: fixture.iso(41 * 20) });
    await page.locator(".jump-latest").waitFor({ timeout: 5000 });
    const leaks = await page.evaluate(SCALE => {
      const layerValues = new Set(Object.values(SCALE).flatMap(v => [v - 1, v, v + 1]));
      const isSC = el => {
        if (el === document.documentElement) return true;
        const cs = getComputedStyle(el), parent = el.parentElement ? getComputedStyle(el.parentElement) : null;
        if (cs.display === "none" || cs.display === "contents") return false;
        if (cs.position === "fixed" || cs.position === "sticky") return true;
        if (cs.zIndex !== "auto" && (cs.position !== "static" || (parent && /flex|grid/.test(parent.display)))) return true;
        if (parseFloat(cs.opacity) < 1 || cs.transform !== "none" || cs.translate !== "none" || cs.scale !== "none" || cs.rotate !== "none") return true;
        if (cs.filter !== "none" || (cs.backdropFilter && cs.backdropFilter !== "none") || cs.perspective !== "none" || cs.clipPath !== "none" || (cs.maskImage && cs.maskImage !== "none")) return true;
        if (cs.mixBlendMode !== "normal" || cs.isolation === "isolate" || /layout|paint|strict|content/.test(cs.contain) || /transform|opacity|filter|z-index|isolation|translate|scale|rotate/.test(cs.willChange)) return true;
        return false;
      };
      const out = [];
      for (const el of document.querySelectorAll("body *")) {
        const z = getComputedStyle(el).zIndex;
        if (z === "auto" || layerValues.has(Number(z)) || !isSC(el)) continue;
        let parent = el.parentElement; while (parent && !isSC(parent)) parent = parent.parentElement;
        if (!parent || parent === document.documentElement) out.push([`${el.tagName.toLowerCase()}.${[...el.classList][0] ?? ""}`, Number(z)]);
      }
      return out;
    }, SCALE);
    const unexpected = leaks.filter(([name, z]) => !LOCAL_IN_ROOT_ALLOWED.some(([n, v]) => n === name && v === z));
    truthy(!unexpected.length, `local z-index values in the root stacking context (give the component root isolation:isolate, or use a --z-* layer): ${unexpected.map(([n, z]) => `${n} (${z})`).join(", ")}`);
    truthy(!errors.length, errors.join("; "));
    await context.close();
  });
}

await fixture.close();
if (failures.length) { console.log(`layers: FAIL (${failures.length} of ${results.length + failures.length} browser check(s))`); process.exit(1); }
console.log(`layers: PASS (static checks and ${results.length} browser checks)`);
