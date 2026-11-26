// The 2x board for a V3 PR (V3 PLAN 9.1, step 4): named catalog states, and optionally a live surface, rendered at 2x in
// light and dark at 1280 and 390 px, into one folder, with long pages cut into slices of at most 2200 image pixels. When a
// reference image is given (a frozen shots/Page*-{light,dark}.png or an artboard), each slice also gets a side-by-side
// file with the reference beside it. It is a look, not a pixel diff.
//
//   PLAYWRIGHT_MODULE=/abs/playwright/index.mjs node scripts/shot-v3-board.mjs --out DIR [options]
//     --states a,b,c        catalog state ids (any section; see --list). Several ids may share one inline frame.
//     --section id          only look for the states in this catalog section (default: any)
//     --live URL            also a live surface: a full URL, or a path for the server given with --origin
//     --origin URL          an already running dev server (default: a Vite dev server started here)
//     --skin v3|probe-a|probe-b   the catalog skin (default v3)
//     --themes light,dark   (default both)     --widths 1280,390   (default both)
//     --ref TEMPLATE        a reference image: "path/Page4-{theme}.png" (also {width}); "state=TEMPLATE" ties it to one
//                           state ("live" for the live surface); a bare TEMPLATE applies to every shot
//     --wait SELECTOR       live only: wait for this selector before the shot
//     --list                print the catalog state ids and exit
// Waits for the fonts and, in the catalog, for the catalog-ready flag. Fails (exit 1) on a page error, a catalog error or a
// state that does not exist. Output: <name>-<theme>-<width>[-NN].png, and <name>-<theme>-<width>[-NN]-vs-ref.png.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { viteCacheDir } from "./lib/vite-cache.mjs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const args = process.argv.slice(2);
const option = (name, fallback) => { const at = args.indexOf(name); return at >= 0 ? args[at + 1] : fallback; };
const options = name => args.flatMap((arg, i) => arg === name ? [args[i + 1]] : []);
const SLICE_IMAGE_PX = 2200, SCALE = 2;
const SLICE_CSS = SLICE_IMAGE_PX / SCALE;
const WIDTH_SIZES = { 1280: { width: 1280, height: 800 }, 390: { width: 390, height: 760 } };

if (!process.env.PLAYWRIGHT_MODULE) { console.error("board: PLAYWRIGHT_MODULE is not set"); process.exit(2); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);

let server, origin = option("--origin");
if (!origin) {
  const { createServer } = await import("vite");
  const { default: react } = await import("@vitejs/plugin-react");
  server = await createServer({ cacheDir: viteCacheDir(), configFile: false, mode: "development", root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
  await server.listen();
  origin = `http://127.0.0.1:${server.httpServer.address().port}`;
}
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const failures = [];
const fail = message => { failures.push(message); console.log(`FAIL ${message}`); };
const finish = code => { console.log(failures.length ? `board: FAIL (${failures.length})` : "board: done"); process.exit(code ?? (failures.length ? 1 : 0)); };

try {
  const LOAD_MS = 120_000;
  const index = await (async () => {
    const page = await browser.newPage();
    try { await page.goto(`${origin}/catalog.html`); await page.waitForFunction(() => Array.isArray(window.__catalog), null, { timeout: LOAD_MS }); return await page.evaluate(() => window.__catalog); }
    finally { await page.close(); }
  })();
  if (args.includes("--list")) { for (const section of index) console.log(`${section.id}: ${section.states.map(state => state.id).join(" ")}`); await browser.close(); await server?.close(); process.exit(0); }

  const out = resolve(option("--out", ""));
  if (!option("--out")) { console.error("board: --out DIR is required"); process.exit(2); }
  mkdirSync(out, { recursive: true });
  const states = (option("--states", "") || "").split(",").filter(Boolean);
  const live = option("--live");
  if (!states.length && !live) { console.error("board: give --states and/or --live"); process.exit(2); }
  const skin = option("--skin", "v3");
  const themes = option("--themes", "light,dark").split(",");
  const widths = option("--widths", "1280,390").split(",").map(Number);
  for (const width of widths) if (!WIDTH_SIZES[width]) { console.error(`board: width ${width} is not 1280 or 390`); process.exit(2); }
  const onlySection = option("--section");
  const refs = options("--ref").map(text => { const m = /^([\w-]+)=(.+)$/.exec(text); return m ? { name: m[1], template: m[2] } : { name: "", template: text }; });
  const refFor = (name, theme, width) => {
    const entry = refs.find(item => item.name === name) ?? refs.find(item => !item.name);
    if (!entry) return null;
    const file = resolve(entry.template.replaceAll("{theme}", theme).replaceAll("{width}", String(width)));
    return existsSync(file) ? file : (console.log(`note  no reference at ${file}`), null);
  };

  // Where each state lives: inline states share their section's frame; others have a frame of their own.
  const targets = [];
  for (const id of states) {
    const hits = index.flatMap(section => (!onlySection || section.id === onlySection) ? section.states.filter(state => state.id === id).map(state => ({ section, state })) : []);
    if (!hits.length) { fail(`no catalog state "${id}"${onlySection ? ` in section ${onlySection}` : ""}; try --list`); continue; }
    if (hits.length > 1) console.log(`note  "${id}" is in ${hits.map(hit => hit.section.id).join(", ")}; using ${hits[0].section.id} (pass --section)`);
    targets.push({ kind: "catalog", name: id, ...hits[0] });
  }
  if (live) targets.push({ kind: "live", name: "live", url: /^https?:/.test(live) ? live : `${origin}${live.startsWith("/") ? "" : "/"}${live}` });

  const context = await browser.newContext({ deviceScaleFactor: SCALE, locale: "en-US", reducedMotion: "reduce" });
  // The catalog runs on synthetic data: nothing may reach a real server. A live surface keeps its network.
  if (targets.every(target => target.kind === "catalog")) await context.route("**/api/**", route => route.abort());

  /** The slices of a box of `height` CSS px: [{ y, h }]. */
  const slicesOf = (top, height) => { const out = []; for (let y = 0; y < height; y += SLICE_CSS) out.push({ y: top + y, h: Math.min(SLICE_CSS, height - y) }); return out; };

  // Side by side: our slice on the left, the same band of the reference (scaled to the same width) on the right.
  async function sideBySide(file, ours, reference, band, pxWidth) {
    const page = await context.newPage();
    try {
      const css = pxWidth / SCALE; // the page is drawn at 2x, so its image is as wide as our slice
      await page.setViewportSize({ width: Math.round(2 * css + 3), height: 100 });
      const ref = readFileSync(reference).toString("base64"), mine = readFileSync(ours).toString("base64");
      const refMime = reference.endsWith(".jpg") || reference.endsWith(".jpeg") ? "jpeg" : "png";
      await page.setContent(`<body style="margin:0;background:#888;display:flex;gap:3px;align-items:flex-start">
        <div style="width:${css}px"><img id="a" style="width:100%;display:block" src="data:image/png;base64,${mine}"></div>
        <div id="b" style="width:${css}px;overflow:hidden;position:relative"><img id="r" style="width:100%;display:block;position:relative" src="data:image/${refMime};base64,${ref}"></div></body>`);
      await page.waitForFunction(() => [...document.images].every(image => image.complete && image.naturalWidth));
      // The reference is scaled to our image width; our slice starts band.from image pixels down, so does the band of it.
      await page.evaluate(from => {
        document.getElementById("b").style.height = `${document.getElementById("a").getBoundingClientRect().height}px`;
        document.getElementById("r").style.top = `${-from / 2}px`;
      }, band.from);
      const size = await page.evaluate(() => ({ w: document.body.scrollWidth, h: Math.ceil(document.getElementById("a").getBoundingClientRect().height) }));
      await page.setViewportSize({ width: size.w, height: Math.max(size.h, 50) });
      await page.screenshot({ path: file, clip: { x: 0, y: 0, width: size.w, height: size.h } });
    } finally { await page.close(); }
  }

  let written = 0;
  for (const target of targets) for (const theme of themes) for (const width of widths) {
    const label = `${target.name} ${theme} ${width}`;
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    page.on("console", message => { if (message.type() === "error" && /Uncaught|Unhandled/.test(message.text())) errors.push(message.text()); });
    try {
      await page.setViewportSize(WIDTH_SIZES[width]);
      let box;
      if (target.kind === "catalog") {
        const query = new URLSearchParams({ frame: target.section.id, theme, lng: "en", skin });
        if (target.state.kind !== "inline") query.set("state", target.state.id);
        await page.goto(`${origin}/catalog.html?${query}`);
        await page.waitForFunction(() => document.documentElement.dataset.catalogReady === "1", null, { timeout: LOAD_MS });
        const error = await page.evaluate(() => document.documentElement.dataset.catalogError ?? "");
        if (error) fail(`${label}: catalog error: ${error}`);
        if (target.state.widths && !target.state.widths.includes(width === 1280 ? "desktop" : "phone")) { console.log(`note  ${label}: the state is not drawn at this width; skipped`); continue; }
      } else {
        await page.addInitScript(appearance => { try { localStorage.setItem("tofi:appearance", appearance); } catch { /* storage may be blocked */ } }, theme);
        await page.emulateMedia({ colorScheme: theme });
        await page.goto(target.url, { waitUntil: "load" });
        if (option("--wait")) await page.waitForSelector(option("--wait"), { timeout: LOAD_MS });
      }
      await page.evaluate(() => document.fonts.ready);
      await page.waitForTimeout(500);
      let top = 0, height;
      if (target.kind === "catalog" && target.state.kind === "inline") {
        const element = page.locator(`[data-catalog-state="${target.state.id}"]`).first();
        if (!await element.count()) { fail(`${label}: the frame has no element for state ${target.state.id}`); continue; }
        await element.scrollIntoViewIfNeeded();
        box = await element.boundingBox();
        const scroll = await page.evaluate(() => scrollY);
        top = box.y + scroll; height = box.height;
        box = { x: Math.max(0, box.x - 12), width: Math.min(WIDTH_SIZES[width].width, box.width + 24) };
      } else {
        height = await page.evaluate(() => Math.max(document.documentElement.scrollHeight, document.body.scrollHeight));
        box = { x: 0, width: WIDTH_SIZES[width].width };
      }
      const pieces = slicesOf(Math.max(0, top - (target.kind === "catalog" && target.state.kind === "inline" ? 12 : 0)), height + (target.kind === "catalog" && target.state.kind === "inline" ? 24 : 0));
      const reference = refFor(target.name, theme, width);
      for (const [n, piece] of pieces.entries()) {
        const stem = `${target.name}-${theme}-${width}${pieces.length > 1 ? `-${String(n + 1).padStart(2, "0")}` : ""}`;
        const file = join(out, `${stem}.png`);
        await page.screenshot({ path: file, fullPage: true, clip: { x: box.x, y: piece.y, width: box.width, height: piece.h } });
        written += 1;
        if (reference) { await sideBySide(join(out, `${stem}-vs-ref.png`), file, reference, { from: n * SLICE_IMAGE_PX }, Math.round(box.width * SCALE)); written += 1; }
      }
      console.log(`ok    ${label}: ${pieces.length} slice(s)${reference ? " + reference" : ""}`);
    } catch (cause) {
      fail(`${label}: ${cause instanceof Error ? cause.message.split("\n")[0] : cause}`);
    } finally {
      if (errors.length) fail(`${label}: page error: ${errors.join("; ")}`);
      await page.close();
    }
  }
  console.log(`board: ${written} image(s) in ${out}`);
  await context.close();
} finally {
  await browser.close();
  await server?.close();
}
finish();
