// Work trace (line B of divide-and-trace): the strip grid the page shares with the server, and the folded line at the end of a
// run's Activity list. Part 1 checks the grid arithmetic against the constants in internal/app/montage.go. Part 2 mounts the real
// component in a Vite fixture with /api stubbed (synthetic data) and drives it with Playwright: the line, the thumbnails cut from
// the one strip, the viewer, a click on a step, the way back, deletion in two steps, and all seven languages at 390px.
//
//   PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:work-trace
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { deflateSync } from "node:zlib";
import { fileURLToPath, pathToFileURL } from "node:url";
import { openUiModules } from "./ui-modules.mjs";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));

// ---- 1. the grid ----
const modules = await openUiModules({ language: "en" });
let layout;
try {
  layout = await modules.load("/src/workTraceLayout.ts");
} finally {
  await modules.close();
}
const go = await readFile(join(ui, "..", "internal/app/montage.go"), "utf8");
const goConst = name => Number(new RegExp(`${name}\\s*=\\s*(\\d+)`).exec(go)?.[1]);
assert.deepEqual([layout.TRACE_TILE_W, layout.TRACE_TILE_H, layout.TRACE_LABEL_H, layout.TRACE_GAP], [goConst("montageTileW"), goConst("montageTileH"), goConst("montageLabelH"), goConst("montageGap")], "the page and the server draw the same grid");

assert.deepEqual(layout.traceStripSize(5), { width: 3 * 424 + 4 * 2, height: 2 * 283 + 3 * 2 });
assert.deepEqual(layout.traceStripSize(2), { width: 2 * 424 + 3 * 2, height: 283 + 2 * 2 }, "two cells make two columns");
assert.deepEqual(layout.traceStripSize(12), { width: 1280, height: 4 * 283 + 5 * 2 });
assert.equal(layout.traceCells(5, 5), 5);
assert.equal(layout.traceCells(11, 15), 12, "earlier steps take the first cell");
for (const cells of [2, 3, 5, 9, 12]) {
  const size = layout.traceStripSize(cells);
  for (let i = 0; i < cells; i++) {
    const r = layout.traceCellRect(i, cells);
    assert.equal(layout.traceCellAt((r.x + r.width / 2) / size.width, (r.y + r.height / 2) / size.height, cells), i, `cell ${i} of ${cells}: its centre`);
    assert.equal(layout.traceCellAt((r.x + 3) / size.width, (r.y + r.height + 8) / size.height, cells), i, `cell ${i} of ${cells}: its label line belongs to it`);
  }
}
assert.equal(layout.traceCellAt(0.5, 0.9999, 5), null, "the bottom margin");
assert.equal(layout.traceCellAt(0.9, 0.9, 5), null, "an empty cell of the last row");
assert.equal(layout.traceCellAt(-0.1, 0.5, 5), null);
assert.equal(layout.traceCellAt(0.5, 1.2, 5), null);
{
  const style = layout.traceThumbStyle("/a.png", 1, 5, 64);
  assert.equal(style.width, 64);
  assert.equal(style.height, 40);
  assert.equal(style.backgroundSize, `${(1280 * 64 / 424).toFixed(2)}px ${(572 * 64 / 424).toFixed(2)}px`);
  assert.equal(style.backgroundPosition, `${(-(2 + 426) * 64 / 424).toFixed(2)}px ${(-2 * 64 / 424).toFixed(2)}px`);
}
console.log("PASS the strip grid matches the server's");

// ---- 2. the component ----
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const { createServer } = await import("vite");
const { default: react } = await import("@vitejs/plugin-react");

/** A PNG of colour blocks, one per cell of a strip, so the viewer has something to show. */
function png(width, height, color) {
  const raw = Buffer.alloc((width * 4 + 1) * height);
  for (let y = 0; y < height; y++) {
    raw[y * (width * 4 + 1)] = 0;
    for (let x = 0; x < width; x++) raw.set(color(x, y), y * (width * 4 + 1) + 1 + x * 4);
  }
  const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
  const crc = buf => { let c = 0xffffffff; for (const b of buf) c = crcTable[(c ^ b) & 255] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
  const chunk = (type, data) => { const out = Buffer.alloc(12 + data.length); out.writeUInt32BE(data.length, 0); out.write(type, 4); data.copy(out, 8); out.writeUInt32BE(crc(out.subarray(4, 8 + data.length)), 8 + data.length); return out; };
  const head = Buffer.alloc(13); head.writeUInt32BE(width, 0); head.writeUInt32BE(height, 4); head[8] = 8; head[9] = 6;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", head), chunk("IDAT", deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
}
const strip = size => png(size.width, size.height, (x, y) => [(x * 255 / size.width) | 0, (y * 255 / size.height) | 0, 120, 255]);
const tile = n => png(64, 40, () => [30 * n, 90, 200 - 20 * n, 255]);

/** The numbers in a CSS length list, as the browser normalises it. */
const numbers = text => text.match(/-?[\d.]+/g).map(Number);

const fixture = await mkdtemp(join(ui, ".work-trace-"));
let server, browser;
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import WorkTrace from '../src/WorkTrace';
const query=new URLSearchParams(location.search);
const tiles=Number(query.get('tiles')||5), steps=Number(query.get('steps')||tiles);
const run={id:'r1',conversation_id:'c1',bot_id:'b1',status:'done',created_at:'2026-11-01T10:00:00Z',updated_at:'2026-11-01T10:05:00Z',trace:{attachment_id:'strip1',tiles,steps,created_at:'2026-11-01T10:05:00Z'}};
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<div className="workspace" style={{padding:16}}><WorkTrace run={run as any}/></div>));
`);
  server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({ executablePath: process.env.TOFI_TEST_CHROME || undefined });

  async function open(params = {}, { width = 1100, height = 900 } = {}) {
    const tiles = Number(params.tiles ?? 5), steps = Number(params.steps ?? tiles), cells = tiles + (steps > tiles ? 1 : 0);
    const cols = Math.min(3, cells), rows = Math.ceil(cells / cols);
    const size = { width: cols * 424 + (cols + 1) * 2, height: rows * 283 + (rows + 1) * 2 };
    const context = await browser.newContext({ viewport: { width, height }, reducedMotion: "reduce" });
    const page = await context.newPage();
    const errors = [], requests = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      requests.push(`${request.method()} ${url.pathname}`);
      if (url.pathname === "/api/attachments/strip1") { await route.fulfill({ status: 200, contentType: "image/png", body: strip(size) }); return; }
      const cap = /^\/api\/attachments\/cap-(\d+)$/.exec(url.pathname);
      if (cap) { await route.fulfill({ status: 200, contentType: "image/png", body: tile(Number(cap[1])) }); return; }
      if (url.pathname === "/api/runs/r1/captures" && request.method() === "GET") {
        // The strip shows the last `tiles` captures of the run; the third one of them was redacted.
        const first = steps - tiles + 1;
        const captures = Array.from({ length: steps }, (_, i) => ({ seq: i + 1, label: `click ${i + 1}`, redacted: i + 1 === first + 2, attachment_id: i + 1 === first + 2 ? undefined : `cap-${i + 1}` }));
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ captures, trace: null }) });
        return;
      }
      if (url.pathname === "/api/runs/r1/captures" && request.method() === "DELETE") { await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ deleted: 4 }) }); return; }
      errors.push(`unstubbed ${request.method()} ${url.pathname}`);
      await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "not stubbed" } }) });
    });
    await page.goto(`${origin}/${basename(fixture)}/index.html?${new URLSearchParams(params)}`);
    await page.locator(".work-trace").waitFor({ timeout: 20000 });
    return { page, errors, requests, size, cells, close: () => context.close() };
  }
  const imageName = page => page.locator("dialog.attachment-lightbox img").getAttribute("src");
  /** Click the centre of strip cell `index` in the open viewer. */
  async function clickCell(t, index) {
    const box = await t.page.locator("dialog.attachment-lightbox img").boundingBox();
    const r = layout.traceCellRect(index, t.cells);
    await t.page.mouse.click(box.x + (r.x + r.width / 2) / t.size.width * box.width, box.y + (r.y + r.height / 2) / t.size.height * box.height);
  }

  // ---- the folded line and its thumbnails ----
  {
    const t = await open();
    const text = (await t.page.locator(".work-trace-line").innerText()).replace(/\s+/g, " ");
    assert.match(text, /Work trace · 5 steps/);
    assert.equal(await t.page.locator(".work-trace-thumb").count(), 5, "one cut of the strip per step, up to six");
    const style = await t.page.locator(".work-trace-thumb").nth(1).evaluate(el => ({ image: el.style.backgroundImage, size: el.style.backgroundSize, pos: el.style.backgroundPosition, w: el.getBoundingClientRect().width }));
    assert.match(style.image, /\/api\/attachments\/strip1/, "the thumbnails are cut from the strip, no second file");
    assert.deepEqual(numbers(style.size), numbers(`${(1280 * 64 / 424).toFixed(2)}px ${(572 * 64 / 424).toFixed(2)}px`));
    assert.equal(Math.round(style.w), 64);
    assert.equal(await t.page.locator("dialog[open]").count(), 0, "folded until opened");
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    await t.close();
    console.log("PASS the folded line shows the title, the steps and thumbnails cut from the strip");
  }
  {
    const t = await open({ tiles: 11, steps: 15 }, { height: 1200 });
    assert.match(await t.page.locator(".work-trace-line").innerText(), /15 steps/);
    assert.equal(await t.page.locator(".work-trace-thumb").count(), 6);
    const pos = await t.page.locator(".work-trace-thumb").first().evaluate(el => el.style.backgroundPosition);
    assert.deepEqual(numbers(pos), numbers(`${(-(2 + 426) * 64 / 424).toFixed(2)}px ${(-2 * 64 / 424).toFixed(2)}px`), "the summary cell is skipped: the first thumbnail is the second cell");
    await t.close();
    console.log("PASS earlier steps fold into the first cell and the thumbnails skip it");
  }

  // ---- the viewer ----
  {
    const t = await open();
    await t.page.locator(".work-trace-line").click();
    await t.page.locator("dialog.attachment-lightbox[open]").waitFor();
    assert.match(await imageName(t.page), /strip1$/, "the strip opens first");
    assert.match(await t.page.locator(".attachment-lightbox-bar small").innerText(), /Work trace · 1\/5/, "the strip and the four steps that kept a picture");
    await clickCell(t, 1);
    assert.match(await imageName(t.page), /cap-2$/, "a click on the second step shows its own picture");
    assert.match(await t.page.locator(".attachment-lightbox-bar small").innerText(), /Step 2: click 2/);
    await t.page.locator("dialog.attachment-lightbox img").click();
    assert.match(await imageName(t.page), /strip1$/, "a click on the picture goes back to the strip");
    await clickCell(t, 2);
    assert.match(await imageName(t.page), /strip1$/, "a redacted step has no picture to open");
    await clickCell(t, 4);
    assert.match(await imageName(t.page), /cap-5$/);
    await t.page.keyboard.press("Escape");
    await t.page.locator("dialog[open]").waitFor({ state: "detached" });
    assert.deepEqual(t.errors, [], `page errors: ${t.errors}`);
    await t.close();
    console.log("PASS the viewer opens the strip, a step opens its picture, a redacted step opens nothing");
  }

  // ---- deletion ----
  {
    const t = await open();
    await t.page.getByRole("button", { name: "Delete trace" }).click();
    assert.equal(await t.page.locator(".work-trace-confirm").count(), 1, "deleting asks first");
    await t.page.getByRole("button", { name: "Keep" }).click();
    assert.equal(t.requests.some(r => r.startsWith("DELETE")), false, "Keep sends nothing");
    await t.page.getByRole("button", { name: "Delete trace" }).click();
    await t.page.getByRole("button", { name: "Delete", exact: true }).click();
    await t.page.getByText("Work trace deleted.").waitFor();
    assert.ok(t.requests.includes("DELETE /api/runs/r1/captures"));
    assert.equal(await t.page.locator(".work-trace-line").count(), 0);
    await t.close();
    console.log("PASS deleting the trace asks first, then removes the line");
  }

  // ---- languages ----
  for (const lang of ["en", "zh-CN", "zh-TW", "ja", "ko", "de", "fr"]) {
    const t = await open({ lang }, { width: 390, height: 800 });
    const text = await t.page.locator(".work-trace").innerText();
    assert.ok(!/trace\.|\{\{/.test(text), `${lang}: raw key on the page: ${text}`);
    const overflow = await t.page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    assert.equal(overflow, false, `${lang}: horizontal overflow at 390px`);
    await t.close();
  }
  console.log("PASS all seven languages render the line at 390px without raw keys or overflow");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, { recursive: true, force: true });
}
