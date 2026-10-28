// Component catalog guard (docs/design/migration.md step 6, docs/decisions/0034-component-catalog-dev-only.md).
//  1. Dev only: the production build (every `vite build` in package.json's "build" script, run into a temp directory)
//     contains no catalog.html, no file carrying the catalog's marker string and no catalog source path; no product file
//     imports from src/catalog; and the catalog loads the same global stylesheets, in the same order, as src/main.tsx.
//  2. It is components.md made visible: one section per numbered component section (1-23), same numbers.
//  3. Browser (Chromium, Vite dev server, synthetic data only): every state at every width it declares opens without a
//     page error or a failed drive step, in light and (at 1280 px) dark; no /api request ever reaches the network; each
//     overlay is the topmost element at its own centre; the keyboard closes (and, where declared, reopens) it; the
//     origin's real localStorage is never written.
// Screenshots: CATALOG_SHOTS=/abs/dir. Only some sections: CATALOG_ONLY=dialogs,menus. Build check only: --build-only.
// Usage: PLAYWRIGHT_MODULE=/abs/path/playwright/index.mjs npm run test:catalog
import { mkdtempSync, readFileSync, readdirSync, rmSync, statSync, existsSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const src = join(ui, "src");
const failures = [];
const fail = message => { failures.push(message); console.log(`FAIL ${message}`); };
const pass = message => console.log(`ok   ${message}`);
const walk = dir => readdirSync(dir).flatMap(name => { const path = join(dir, name); return statSync(path).isDirectory() ? walk(path) : [path]; });
const marker = /export const CATALOG_MARKER = "([^"]+)"/.exec(readFileSync(join(src, "catalog", "sandbox.ts"), "utf8"))?.[1];
if (!marker) fail("src/catalog/sandbox.ts must export CATALOG_MARKER");

// ---- 1a. static: stylesheets, imports, configs
const cssImports = (file, prefix) => [...readFileSync(file, "utf8").matchAll(/^import "([^"]+\.css)";$/gm)].map(m => m[1]).filter(path => path.startsWith(prefix)).map(path => path.slice(prefix.length));
const productCss = cssImports(join(src, "main.tsx"), "./");
const catalogCss = cssImports(join(src, "catalog", "main.tsx"), "../");
if (productCss.join() !== catalogCss.join()) fail(`catalog/main.tsx must import the global stylesheets of src/main.tsx in the same order:\n  main:    ${productCss.join(" ")}\n  catalog: ${catalogCss.join(" ")}`);
else pass(`catalog loads the ${productCss.length} global stylesheets of src/main.tsx in order`);
const leaks = walk(src).filter(file => /\.(tsx?|css)$/.test(file) && !relative(src, file).startsWith("catalog/"))
  .filter(file => /(?:from\s+|import\s*\(?\s*)["'][^"']*\/catalog\//.test(readFileSync(file, "utf8")));
if (leaks.length) fail(`product code imports the dev-only catalog: ${leaks.map(file => relative(ui, file)).join(", ")}`);
else pass("no product file imports from src/catalog");
const pkg = JSON.parse(readFileSync(join(ui, "package.json"), "utf8"));
for (const file of ["vite.config.ts", "vite.motion-lab.config.ts"]) if (/catalog/i.test(readFileSync(join(ui, file), "utf8"))) fail(`${file} mentions the catalog: it must not be a build input`);
if (/catalog/i.test(pkg.scripts.build)) fail(`package.json "build" mentions the catalog: ${pkg.scripts.build}`);

// ---- 1b. the real production build contains no trace of the catalog
const { build } = await import("vite");
// vite build sets NODE_ENV=production for the whole process; the dev server below must start as development again.
const nodeEnv = process.env.NODE_ENV;
const outDir = mkdtempSync(join(tmpdir(), "tofi-catalog-dist-"));
try {
  const steps = pkg.scripts.build.split("&&").map(step => step.trim()).filter(step => /^vite build\b/.test(step));
  if (!steps.length) fail(`package.json "build" runs no vite build: ${pkg.scripts.build}`);
  for (const [index, step] of steps.entries()) {
    const config = /--config\s+(\S+)/.exec(step)?.[1] ?? "vite.config.ts";
    await build({ configFile: resolve(ui, config), root: ui, logLevel: "error", build: { outDir, ...(index === 0 ? { emptyOutDir: true } : {}) } });
  }
  const files = walk(outDir);
  const names = files.map(file => relative(outDir, file));
  if (!names.includes("index.html")) fail(`the build produced no index.html (${names.length} files): the check did not run`);
  // (dist/icons/catalog.js is the icon set's own catalog, not this page.)
  const named = names.filter(name => /(^|\/)catalog\.html$|(^|\/)assets\/catalog[-.]/i.test(name));
  const carrying = files.filter(file => { const text = readFileSync(file).toString("latin1"); return (marker && text.includes(marker)) || text.includes("src/catalog/") || text.includes("catalog.html"); }).map(file => relative(outDir, file));
  if (named.length) fail(`production build contains catalog files: ${named.join(", ")}`);
  if (carrying.length) fail(`production build carries catalog code: ${carrying.join(", ")}`);
  if (!named.length && !carrying.length) pass(`production build (${steps.length} vite builds, ${names.length} files) has no catalog page, chunk or marker`);
} finally {
  rmSync(outDir, { recursive: true, force: true });
  if (nodeEnv === undefined) delete process.env.NODE_ENV; else process.env.NODE_ENV = nodeEnv;
}

if (process.argv.includes("--build-only")) finish();

// ---- 2 and 3. browser
if (!process.env.PLAYWRIGHT_MODULE) { fail("PLAYWRIGHT_MODULE is not set: the browser checks cannot run"); finish(); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const { createServer } = await import("vite");
const { default: react } = await import("@vitejs/plugin-react");
const server = await createServer({ configFile: false, mode: "development", root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const shots = process.env.CATALOG_SHOTS;
if (shots) mkdirSync(shots, { recursive: true });
const WIDTHS = { desktop: { width: 1280, height: 800 }, phone: { width: 390, height: 760 } };
const networkApi = [];

try {
  const context = await browser.newContext({ viewport: WIDTHS.desktop, locale: "en-US" });
  await context.route("**/*", route => {
    const url = new URL(route.request().url());
    if (url.pathname.startsWith("/api/") || url.pathname === "/health") { networkApi.push(`${route.request().method()} ${url.pathname}`); return route.abort(); }
    return route.continue();
  });
  const shell = await context.newPage();
  const shellErrors = [];
  shell.on("pageerror", error => shellErrors.push(error.message));
  await shell.goto(`${origin}/catalog.html`);
  await shell.waitForFunction(() => Array.isArray(window.__catalog), null, { timeout: 30000 });
  const index = await shell.evaluate(() => window.__catalog);
  await shell.locator("iframe[data-catalog-frame]").first().waitFor({ timeout: 30000 });
  if (shellErrors.length) fail(`shell: ${shellErrors.join("; ")}`); else pass(`shell lists ${index.length} sections and renders frames`);
  await shell.close();

  // 2. one section per numbered section of components.md
  // components.md is an internal doc, untracked in the public repo; the check runs only where it exists.
  const componentsDoc = join(ui, "..", "docs", "design", "components.md");
  const documented = !existsSync(componentsDoc) ? index.map(section => Number(section.number)) : [...readFileSync(componentsDoc, "utf8").matchAll(/^## (\d+)\. /gm)].map(m => Number(m[1])).filter(n => n >= 1 && n <= 23);
  const numbers = index.map(section => Number(section.number));
  const missing = documented.filter(n => !numbers.includes(n));
  if (missing.length || numbers.length !== documented.length) fail(`catalog sections ${numbers.join(",")} do not match components.md sections ${documented.join(",")}`);
  else pass(`one catalog section for each of components.md §${documented[0]}–§${documented.at(-1)} (${index.reduce((n, s) => n + s.states.length, 0)} states)`);

  const only = process.env.CATALOG_ONLY?.split(",");
  const jobs = [];
  for (const section of index) {
    if (only && !only.includes(section.id)) continue;
    const inline = section.states.filter(state => state.kind === "inline");
    for (const width of ["desktop", "phone"]) {
      for (const theme of width === "desktop" ? ["light", "dark"] : ["light"]) {
        if (inline.length) jobs.push({ section, state: null, width, theme });
        for (const state of section.states.filter(state => state.kind !== "inline" && state.widths.includes(width))) jobs.push({ section, state, width, theme });
      }
    }
  }

  const small = index.find(section => section.id === "computer")?.states.find(state => state.id === "small");
  if (small && (!only || only.includes("computer"))) jobs.push({ section: index.find(section => section.id === "computer"), state: small, width: "desktop", theme: "light", lateChunk: true });

  let checkedProbes = 0, checkedKeys = 0;
  async function run(job) {
    const { section, state, width, theme, lateChunk } = job;
    const name = `${section.id}/${state?.id ?? "inline"} ${theme} ${WIDTHS[width].width}${lateChunk ? " (lazy chunk arrives late)" : ""}`;
    const page = await context.newPage();
    // A slow network: the computer window's lazy chunk and its stylesheet land after the window is already open. The window
    // must already sit in its place (its box rules are eager), not be measured as an unstyled block and keep those numbers.
    if (lateChunk) await page.route(/BotDesktopPanel|desktop-presence/, async route => { await new Promise(done => setTimeout(done, 1500)); await route.continue(); });
    await page.setViewportSize(WIDTHS[width]);
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    const query = new URLSearchParams({ frame: section.id, theme, lng: "en" });
    if (state) query.set("state", state.id);
    try {
      await page.goto(`${origin}/catalog.html?${query}`);
      await page.waitForFunction(() => document.documentElement.dataset.catalogReady === "1", null, { timeout: 30000 });
      const error = await page.evaluate(() => document.documentElement.dataset.catalogError ?? "");
      if (error) fail(`${name}: ${error}`);
      const screenshot = () => page.screenshot({ path: join(shots, `${section.number.padStart(2, "0")}-${section.id}-${state?.id ?? "inline"}-${theme}-${WIDTHS[width].width}.png`), fullPage: !state });
      if (shots && !state?.probe) { await page.waitForTimeout(300); await screenshot(); }
      if (state?.probe) {
        // Let entrance animations settle before asking what is on top.
        await page.waitForTimeout(450);
        // Late chunk: judge the window only once the chunk's content has replaced the skeleton, i.e. its stylesheet has landed.
        if (lateChunk) await page.waitForFunction(() => document.querySelector(".desktop-floating-shell .computer-detail"), null, { timeout: 15000 });
        // Hit testing follows paint order only for elements that take the pointer. Hints, drop overlays and the like are
        // pointer-events:none by design, so for the check every element takes it: the hit is then whatever paints on top.
        const probe = () => page.evaluate(selector => {
          const target = [...document.querySelectorAll(selector)].find(element => element.getClientRects().length && getComputedStyle(element).visibility !== "hidden");
          if (!target) return { found: false };
          const style = document.createElement("style");
          style.textContent = "*, *::before, *::after { pointer-events: auto !important; }";
          document.head.append(style);
          try {
            const rect = target.getBoundingClientRect();
            const x = Math.min(Math.max(rect.left + rect.width / 2, 0), innerWidth - 1), y = Math.min(Math.max(rect.top + Math.min(rect.height, innerHeight) / 2, 0), innerHeight - 1);
            const hit = document.elementFromPoint(x, y);
            const describe = element => element ? `${element.tagName.toLowerCase()}${element.id ? `#${element.id}` : ""}${typeof element.className === "string" && element.className ? `.${element.className.trim().split(/\s+/).join(".")}` : ""}` : "nothing";
            return { found: true, inside: Boolean(hit && target.contains(hit)), hit: describe(hit), target: describe(target) };
          } finally { style.remove(); }
        }, state.probe);
        let top = await probe();
        for (let attempt = 0; attempt < 8 && !(top.found && top.inside); attempt += 1) { await page.waitForTimeout(250); top = await probe(); }
        if (!top.found) fail(`${name}: no visible ${state.probe}`);
        else if (!top.inside) fail(`${name}: ${top.target} is covered at its centre by ${top.hit}`);
        else checkedProbes += 1;
        if (shots) await screenshot();
        if (top.found && state.keyboard) {
          await page.bringToFront();
          // Keys go where the product put focus; only when nothing has it does the check focus into the overlay.
          await page.evaluate(selector => { const target = document.querySelector(selector); if (target && (!document.activeElement || document.activeElement === document.body)) (target.querySelector("button, [href], input, select, textarea, [tabindex]") ?? target).focus?.(); }, state.probe);
          await page.keyboard.press(state.keyboard.close);
          const closed = await page.waitForFunction(selector => ![...document.querySelectorAll(selector)].some(element => element.getClientRects().length && getComputedStyle(element).visibility !== "hidden"), state.probe, { timeout: 2000 }).then(() => true, () => false);
          if (!closed) fail(`${name}: ${state.keyboard.close} did not close ${state.probe} (focus: ${await page.evaluate(() => { const element = document.activeElement; return element ? `${element.tagName.toLowerCase()}.${String(element.className).split(" ").join(".")}` : "none"; })}; open dialogs: ${await page.evaluate(() => [...document.querySelectorAll("dialog[open], [aria-modal=true]")].map(element => element.className).join(", ") || "none")})`);
          else if (state.keyboard.reopen) {
            const { focus, key, shift } = state.keyboard.reopen;
            await page.waitForTimeout(250);
            await page.locator(focus).first().focus();
            await page.keyboard.press(shift ? `Shift+${key}` : key);
            const reopened = await page.waitForFunction(selector => [...document.querySelectorAll(selector)].some(element => element.getClientRects().length), state.probe, { timeout: 3000 }).then(() => true, () => false);
            if (!reopened) fail(`${name}: ${shift ? "Shift+" : ""}${key} on ${focus} did not reopen ${state.probe}`); else checkedKeys += 1;
          } else checkedKeys += 1;
        }
      }
      if (errors.length) fail(`${name}: page error: ${errors.join("; ")}`);
    } catch (cause) {
      fail(`${name}: ${cause instanceof Error ? cause.message.split("\n")[0] : cause}${errors.length ? ` (page errors: ${errors.join("; ")})` : ""}`);
    } finally { await page.close(); }
  }
  const queue = [...jobs];
  await Promise.all(Array.from({ length: Number(process.env.CATALOG_PARALLEL ?? 4) }, async () => { while (queue.length) await run(queue.shift()); }));
  pass(`${jobs.length} frames opened; ${checkedProbes} overlays topmost at their centre; ${checkedKeys} keyboard close checks`);

  // A frame in another language: the catalog shows the product's own catalogs.
  const zh = await context.newPage();
  await zh.goto(`${origin}/catalog.html?frame=settings&theme=light&lng=zh-CN`);
  await zh.waitForFunction(() => document.documentElement.dataset.catalogReady === "1", null, { timeout: 30000 });
  if (await zh.evaluate(() => document.documentElement.lang) !== "zh-CN") fail("a frame with lng=zh-CN did not switch the product language"); else pass("frames render in the chosen language");
  await zh.close();

  // The pending connection row has no illustration; its one cue is a breathing ring on the primary button (opacity and scale only), still under reduced motion.
  if (!only || only.includes("settings")) for (const reduced of [false, true]) {
    const waiting = await context.newPage();
    await waiting.emulateMedia({ reducedMotion: reduced ? "reduce" : "no-preference" });
    await waiting.goto(`${origin}/catalog.html?frame=settings&theme=light&lng=en&state=connection-waiting-loopback`);
    await waiting.waitForFunction(() => document.documentElement.dataset.catalogReady === "1", null, { timeout: 30000 });
    const cue = await waiting.evaluate(() => {
      const button = document.querySelector('.mcp-service[data-kind="waiting"] [data-pending]');
      const after = button ? getComputedStyle(button, "::after") : null;
      return { found: Boolean(button), animation: after?.animationName ?? "", illustration: document.querySelectorAll(".web-oauth-link, .web-oauth-node, .web-oauth-provider").length, note: document.querySelector(".mcp-login-note")?.textContent ?? "" };
    });
    if (!cue.found) fail("pending row: the primary button carries no data-pending cue");
    else if (cue.illustration) fail("pending row: the authorization illustration is back");
    else if (reduced ? cue.animation !== "none" : cue.animation !== "mcp-pending-breathe") fail(`pending row: ring animation is "${cue.animation}" with reduced motion ${reduced ? "on" : "off"}`);
    else if (!/127\.0\.0\.1/.test(cue.note)) fail("pending row: the route line does not say the redirect is 127.0.0.1");
    else pass(`pending row: no illustration, ${reduced ? "static ring under reduced motion" : "breathing ring on the primary button"}, one route line`);
    await waiting.close();
  }

  if (networkApi.length) fail(`${networkApi.length} /api requests reached the network (the dev server would proxy them to a real TOFI): ${[...new Set(networkApi)].slice(0, 12).join(", ")}`);
  else pass("no /api request reached the network");
  const stored = (await context.storageState()).origins.flatMap(item => item.localStorage.map(entry => `${item.origin} ${entry.name}`));
  if (stored.length) fail(`the catalog wrote the origin's real localStorage: ${stored.join(", ")}`); else pass("the origin's real localStorage is untouched");
  await context.close();
} finally {
  await browser.close();
  await server.close();
}
finish();

function finish() {
  if (failures.length) { console.log(`catalog: FAIL (${failures.length})`); process.exit(1); }
  console.log("catalog: PASS");
  process.exit(0);
}
