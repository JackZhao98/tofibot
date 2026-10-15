// File cards + online preview: one card after a re-publish, typed tiles, viewer for md/txt/csv/json/pdf/image,
// inline attachment links, in-document links with Back, unsupported fallback, iPad and phone screenshots.
// Synthetic data; /api is answered in the browser. Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:file-preview
import { auditFocus, focusRingProbe } from "./focus-audit.mjs";
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const shots = join(ui, "..", "artifacts", "file-preview");
mkdirSync(shots, { recursive: true });

const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = s => new Date(T0 + s * 1000).toISOString();
const bot = { id: "bot_a", name: "Ledger Cat", instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: "dm_a", created_at: iso(0) };
const dm = { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 0, user_visible: true };

// 1x1 PNG
const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
const pdf = Buffer.from("%PDF-1.1\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF");
const big = Array.from({ length: 60000 }, (_, i) => `line ${i + 1} of the long synthetic log`).join("\n");

const files = {
  md_new: { name: "frevana-seo-analysis.md", mime: "text/markdown", at: 20, body: "# SEO analysis\n\nCorrected figures.\n\n| Page | Visits |\n|---|---|\n| /home | 1200 |\n\n```js\nconsole.log('ok')\n```\n\nSee the [notes](attachment:att_txt), the [table](data.csv), [missing](ghost.md) and [web](https://example.com)." },
  md_old: { name: "frevana-seo-analysis.md", mime: "text/markdown", at: 10, body: "# SEO analysis\n\nOlder figures." },
  att_txt: { name: "notes.txt", mime: "text/plain; charset=utf-8", at: 21, body: "first line\nsecond line\nthird line\n" },
  att_csv: { name: "data.csv", mime: "text/csv", at: 22, body: 'name,score,comment\nAda,10,"says ""hi"", twice"\nGrace,12,ok\n' },
  att_json: { name: "config.json", mime: "application/json", at: 23, body: '{"a":1,"b":{"c":[1,2]}}' },
  att_pdf: { name: "paper.pdf", mime: "application/pdf", at: 24, body: pdf },
  att_xlsx: { name: "model.xlsx", mime: "application/octet-stream", at: 25, body: Buffer.from("PK\u0003\u0004") },
  att_zip: { name: "bundle.zip", mime: "application/zip", at: 26, body: Buffer.from("PK\u0003\u0004") },
  att_py: { name: "tool.py", mime: "text/plain; charset=utf-8", at: 27, body: "print('hi')\n" },
  att_big: { name: "huge.log", mime: "text/plain; charset=utf-8", at: 28, body: big },
  img_1: { name: "shot-1.png", mime: "image/png", at: 30, body: png },
  img_2: { name: "shot-2.png", mime: "image/png", at: 31, body: png },
  img_3: { name: "shot-3.png", mime: "image/png", at: 32, body: png },
  img_4: { name: "shot-4.png", mime: "image/png", at: 33, body: png },
  img_5: { name: "shot-5.png", mime: "image/png", at: 34, body: png },
  img_solo: { name: "solo.png", mime: "image/png", at: 35, body: png },
};
const att = id => ({ id, conversation_id: "dm_a", name: files[id].name, mime: files[id].mime, size: Buffer.byteLength(files[id].body), created_at: iso(files[id].at) });
const msg = (id, seq, content, attachments, at) => ({ id, conversation_id: "dm_a", role: "assistant", content, seq, created_at: iso(at), sender_bot_id: "bot_a", sender_bot_name: "Ledger Cat", run_id: `run_${id}`, attachments });
const messages = [
  msg("m1", 1, "Here is the corrected version, replacing the previous one: [frevana-seo-analysis.md](attachment:md_new)", ["md_old", "md_new"].map(att), 40),
  msg("m2", 2, "More files:", ["att_txt", "att_csv", "att_json", "att_pdf", "att_xlsx", "att_zip", "att_py", "att_big"].map(att), 50),
  msg("m3", 3, "Five screenshots.", ["img_1", "img_2", "img_3", "img_4", "img_5"].map(att), 60),
  msg("m4", 4, "One screenshot, see [it](attachment:img_solo) or the [table](attachment:att_csv).\n\n```python\n" + Array.from({ length: 20 }, (_, i) => `x${i} = ${i}`).join("\n") + "\n```", [att("img_solo")], 70),
];
let downloads = 0;

async function open(width, height, { locale = "zh-CN", theme = "light" } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, locale, colorScheme: theme, reducedMotion: "reduce", hasTouch: width < 700, isMobile: width < 700 });
  await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
  const page = await context.newPage();
  page.setDefaultTimeout(6000);
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/api/**", async route => {
    if (process.env.FP_DELAY && /^\/api\/attachments\//.test(new URL(route.request().url()).pathname)) await new Promise(r => setTimeout(r, Number(process.env.FP_DELAY))); // FP_DELAY=ms slows attachment fetches to expose timing races
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    const file = /^\/api\/attachments\/(.+)$/.exec(path);
    if (file) { const f = files[decodeURIComponent(file[1])]; if (!f) return route.fulfill({ status: 404, json: { error: { code: "not_found" } } }); downloads += 1; return route.fulfill({ status: 200, headers: { "Content-Type": f.mime, "Content-Disposition": `attachment; filename="${f.name}"` }, body: f.body }); }
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: false });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots: [bot] });
    if (path === "/api/conversations") return json({ conversations: [dm] });
    if (path.endsWith("/messages")) return json({ messages, drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs: [] });
    if (path.endsWith("/tools")) return json({ activities: [], summaries: [], tool_count: 0, has_more: false });
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path.endsWith("/schedules")) return json({ schedules: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    if (path === "/api/secret-inputs") return json({ requests: [] });
    return json({});
  });
  await page.goto(`${origin}/b/bot_a`);
  await page.locator(".message-block").first().waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(400);
  return { page, context, errors };
}

// Focus rule: a ring appears only for keyboard navigation. Opening the viewer with a click/tap must not ring anything
// (the dialog's auto-focus used to ring Download); Tab then rings what it focuses; the pointer sweep samples the page.
async function focusRule(page, { touch = false } = {}) {
  const ringless = async what => { const probe = await page.evaluate(focusRingProbe); assert.ok(probe.none || probe.field || !probe.ring, `${what}: ${probe.focused} draws ${probe.ring?.ring}`); };
  for (const name of ["frevana-seo-analysis.md", "notes.txt", "data.csv", ...(touch ? [] : ["model.xlsx"])]) {
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    await card(page, name).scrollIntoViewIfNeeded();
    if (touch) { const box = await card(page, name).boundingBox(); await page.touchscreen.tap(box.x + Math.min(40, box.width / 2), box.y + box.height / 2); } else await card(page, name).click();
    await viewer(page).waitFor().catch(error => { throw new Error(`${name}: viewer did not open (${error.message.split("\n")[0]})`); });
    await page.waitForTimeout(120);
    await ringless(`opened ${name} by pointer`);
    await page.keyboard.press("Tab");
    await page.waitForTimeout(100); // outlines inherit the global transition; let it start
    const probe = await page.evaluate(focusRingProbe);
    assert.ok(probe.ring || probe.field, `Tab inside the viewer (${name}) must ring ${probe.focused}`);
    await page.keyboard.press("Escape"); await viewer(page).waitFor({ state: "detached" });
    // closing with a pointer press on the close button leaves no ring on the card that regains focus
    await card(page, name).scrollIntoViewIfNeeded();
    if (touch) { const box = await card(page, name).boundingBox(); await page.touchscreen.tap(box.x + Math.min(40, box.width / 2), box.y + box.height / 2); } else await card(page, name).click();
    await viewer(page).waitFor();
    const close = await viewer(page).locator("button[aria-label]").last().boundingBox();
    if (touch) await page.touchscreen.tap(close.x + close.width / 2, close.y + close.height / 2); else await page.mouse.click(close.x + close.width / 2, close.y + close.height / 2);
    await viewer(page).waitFor({ state: "detached" });
    await page.waitForTimeout(120);
    await ringless(`closed ${name} by pointer`);
  }
  const found = await auditFocus(page, { touch, scope: "file-preview page" });
  assert.deepEqual(found, [], JSON.stringify(found));
}

const viewer = page => page.locator("dialog.file-preview[open]");
const card = (page, name) => page.locator(".attachment-file-row", { hasText: name }).locator(".attachment-file-open");
async function closeViewer(page) { await page.keyboard.press("Escape"); await viewer(page).waitFor({ state: "detached" }).catch(() => {}); assert.equal(await viewer(page).count(), 0, "Esc closes the viewer"); }

const results = [];
let current = null;
const step = async (name, fn) => { try { await fn(); results.push(`ok   ${name}`); } catch (error) { results.push(`FAIL ${name}\n${error.stack ?? error}`); process.exitCode = 1; await current?.keyboard.press("Escape").catch(() => {}); } };

try {
  const { page, errors, context } = await open(1180, 820);
  current = page;

  await step("a re-published file shows one card (newest) and the older version is hidden", async () => {
    assert.equal(await page.locator(".attachment-file-row", { hasText: "frevana-seo-analysis.md" }).count(), 1);
    const size = await page.locator(".attachment-file-row", { hasText: "frevana-seo-analysis.md" }).locator("small").innerText();
    assert.ok(size.startsWith(`${Math.round(Buffer.byteLength(files.md_new.body) / 1024) || 0}`) || size.includes("B"), size);
    assert.ok(size.includes(`${Buffer.byteLength(files.md_new.body)} B`), `newest size shown: ${size}`);
  });

  await step("file row is 340px with a colored tile per type and an always-visible download", async () => {
    const row = page.locator(".delivered-attachment.is-file").first();
    const box = await row.boundingBox();
    assert.equal(Math.round(box.width), 340);
    const tones = await page.evaluate(() => Object.fromEntries([...document.querySelectorAll(".attachment-file-row")].map(r => { const t = r.querySelector(".file-tile"); return [r.querySelector(".attachment-name").textContent, { tone: t.dataset.fileTone, label: t.textContent, bg: getComputedStyle(t).backgroundColor }]; })));
    assert.deepEqual(Object.fromEntries(Object.entries(tones).map(([k, v]) => [k, v.tone])), { "frevana-seo-analysis.md": "md", "notes.txt": "txt", "data.csv": "xlsx", "config.json": "json", "paper.pdf": "pdf", "model.xlsx": "xlsx", "bundle.zip": "zip", "tool.py": "code", "huge.log": "txt" });
    assert.equal(tones["model.xlsx"].label, "XLSX"); assert.equal(tones["paper.pdf"].label, "PDF"); assert.equal(tones["tool.py"].label, "PY");
    assert.notEqual(tones["model.xlsx"].bg, tones["paper.pdf"].bg);
    assert.equal(tones["paper.pdf"].bg, "rgb(184, 67, 47)"); assert.equal(tones["model.xlsx"].bg, "rgb(77, 138, 93)");
    const dl = page.locator(".attachment-file-row", { hasText: "notes.txt" }).locator("a.attachment-download");
    assert.equal(await dl.isVisible(), true); assert.equal(await dl.getAttribute("download"), "notes.txt");
    assert.match(await dl.getAttribute("href"), /\/api\/attachments\/att_txt$/);
    assert.equal(await page.locator(".attachment-file-row").first().evaluate(el => getComputedStyle(el.closest(".delivered-attachment")).backgroundColor !== "rgba(0, 0, 0, 0)"), true);
  });

  await step("images: 3-up grid with +N expands; single image keeps the 340px frame", async () => {
    const grid = page.locator(".delivered-attachment-grid");
    assert.equal(await grid.locator(".attachment-grid-tile").count(), 3);
    assert.equal((await grid.locator(".attachment-grid-more").innerText()).trim(), "+2");
    assert.equal(Math.round((await grid.boundingBox()).width), 340);
    await grid.locator(".attachment-grid-tile").first().click();
    assert.equal(await grid.locator("dialog[open]").count(), 1);
    await page.keyboard.press("Escape");
    await grid.locator(".is-more").click();
    assert.equal(await grid.locator(".attachment-grid-tile").count(), 5);
    const frame = page.locator(".attachment-image-frame");
    assert.equal(await frame.count(), 1);
    assert.equal(Math.round((await page.locator(".delivered-attachment.is-image").boundingBox()).width), 340);
    assert.equal(await frame.evaluate(el => getComputedStyle(el).borderRadius), "13px");
  });

  await step("code block has header, copy and fold", async () => {
    const block = page.locator(".message-code-block").last();
    assert.match(await block.locator(".message-code-meta").innerText(), /python · 20 行/);
    assert.equal(await block.locator(".message-code-copy").isVisible(), true);
    await block.locator(".message-code-more").click();
    assert.equal(await block.locator(".message-code.is-collapsed").count(), 0);
  });

  await step("markdown preview opens from the card, renders tables/code, Esc closes and returns focus", async () => {
    const trigger = card(page, "frevana-seo-analysis.md");
    await trigger.click();
    await viewer(page).waitFor();
    assert.equal(await viewer(page).locator("#fp-title").innerText(), "frevana-seo-analysis.md");
    assert.equal(await viewer(page).locator(".fp-md h1").innerText(), "SEO analysis");
    assert.equal(await viewer(page).locator(".fp-md table td").first().innerText(), "/home");
    await viewer(page).locator(".fp-md pre.message-code").first().waitFor();
    assert.equal(await viewer(page).locator(".fp-md pre.message-code").count(), 1);
    assert.equal(await viewer(page).locator("a[download]").isVisible(), true);
    await page.screenshot({ path: join(shots, "ipad-markdown.png") });
    await closeViewer(page);
    assert.equal(await page.evaluate(() => document.activeElement?.className), "attachment-file-open");
  });

  await step("inline attachment: link in message text opens the same viewer", async () => {
    await page.locator('.message-block[data-message-id="m1"] a.attachment-link').click();
    await viewer(page).waitFor();
    assert.equal(await viewer(page).locator("#fp-title").innerText(), "frevana-seo-analysis.md");
    assert.equal(await viewer(page).locator(".fp-md h1").innerText(), "SEO analysis");
    assert.equal(await viewer(page).locator("button[aria-label*='frevana'], .fp-icon").count() >= 1, true);
  });

  await step("links inside the document navigate within the viewer and Back returns", async () => {
    const v = viewer(page);
    assert.equal(await v.locator(".fp-head .fp-icon").count(), 1, "no Back on the first document");
    await v.locator('.fp-md a[data-attachment-id="att_txt"]').click();
    await v.locator("#fp-title", { hasText: "notes.txt" }).waitFor();
    assert.equal(await v.locator(".fp-gutter").innerText(), "1\n2\n3");
    assert.match(await v.locator(".fp-text").innerText(), /second line/);
    assert.equal(await v.locator(".fp-head .fp-icon").count(), 2, "Back button appears");
    await v.locator(".fp-head .fp-icon").first().click();
    await v.locator("#fp-title", { hasText: "frevana-seo-analysis.md" }).waitFor();
    await v.locator('.fp-md a[data-attachment-id="att_csv"]').click();
    await v.locator("#fp-title", { hasText: "data.csv" }).waitFor();
    await v.locator(".fp-table th").first().waitFor();
    const heads = await v.locator(".fp-table th").allInnerTexts();
    assert.deepEqual(heads, ["name", "score", "comment"]);
    assert.equal(await v.locator(".fp-table tbody tr").nth(0).locator("td").nth(2).innerText(), 'says "hi", twice');
    await page.screenshot({ path: join(shots, "ipad-csv.png") });
    await v.locator(".fp-head .fp-icon").first().click();
    assert.equal(await v.locator(".fp-md h1").innerText(), "SEO analysis");
    assert.equal(await v.locator(".unresolved-link").innerText(), "missing");
    assert.equal(await v.locator('.fp-md a[href="https://example.com"]').getAttribute("target"), "_blank");
    await closeViewer(page);
  });

  await step("txt, csv, json, pdf previews from the cards", async () => {
    await card(page, "config.json").click(); await viewer(page).waitFor();
    assert.equal(await viewer(page).locator(".fp-text").innerText(), JSON.stringify({ a: 1, b: { c: [1, 2] } }, null, 2));
    await closeViewer(page);
    await card(page, "data.csv").click(); await viewer(page).waitFor();
    // The table arrives after the attachment fetch; count() does not wait, so wait for the rows first.
    await viewer(page).locator(".fp-table tbody tr").nth(1).waitFor();
    assert.equal(await viewer(page).locator(".fp-table tbody tr").count(), 2);
    await closeViewer(page);
    await card(page, "tool.py").click(); await viewer(page).waitFor();
    assert.match(await viewer(page).locator(".fp-text").innerText(), /print\('hi'\)/);
    await closeViewer(page);
    await card(page, "paper.pdf").click(); await viewer(page).waitFor();
    const src = await viewer(page).locator("iframe.fp-frame").getAttribute("src");
    assert.equal(src, "/api/attachments/att_pdf?inline=1");
    await closeViewer(page);
  });

  await step("oversized text shows the head and a download note", async () => {
    await card(page, "huge.log").click(); await viewer(page).waitFor();
    await viewer(page).locator(".fp-note").waitFor();
    const lines = (await viewer(page).locator(".fp-gutter").innerText()).split("\n").length;
    assert.ok(lines > 1000 && lines < 60000, `first part only (${lines} lines)`);
    await closeViewer(page);
  });

  await step("image link opens the viewer; unsupported types fall back to Download", async () => {
    await page.locator('.message-block[data-message-id="m4"] a.attachment-link[data-attachment-id="img_solo"]').click();
    await viewer(page).waitFor();
    assert.equal(await viewer(page).locator(".fp-image img").getAttribute("src"), "/api/attachments/img_solo");
    await closeViewer(page);
    for (const name of ["model.xlsx", "bundle.zip"]) {
      await card(page, name).click(); await viewer(page).waitFor();
      assert.equal(await viewer(page).locator(".fp-unsupported strong").innerText(), "暂不支持预览");
      assert.equal(await viewer(page).locator(".fp-unsupported a[download]").getAttribute("download"), name);
      await closeViewer(page);
    }
    await page.locator('.message-block[data-message-id="m4"] a.attachment-link[data-attachment-id="att_csv"]').click();
    await viewer(page).waitFor();
    await viewer(page).locator(".fp-table th").nth(2).waitFor();
    assert.equal(await viewer(page).locator(".fp-table th").count(), 3);
    await closeViewer(page);
  });

  await step("viewer is keyboard accessible (focus inside, labelled dialog)", async () => {
    await card(page, "notes.txt").click(); await viewer(page).waitFor();
    assert.equal(await page.evaluate(() => document.activeElement?.closest("dialog.file-preview") !== null), true);
    await page.keyboard.press("Tab");
    assert.equal(await page.evaluate(() => document.activeElement?.closest("dialog.file-preview") !== null), true, "focus stays inside");
    assert.equal(await viewer(page).getAttribute("aria-labelledby"), "fp-title");
    await closeViewer(page);
  });

  await step("focus rings are keyboard-only (iPad, mouse)", () => focusRule(page));

  await step("no page errors on iPad", async () => assert.deepEqual(errors, []));
  await page.screenshot({ path: join(shots, "ipad-cards.png") });
  await context.close();

  const phone = await open(390, 844);
  await step("phone: cards fit, viewer is full-screen", async () => {
    assert.equal(await phone.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth), 0);
    await phone.page.screenshot({ path: join(shots, "phone-cards.png") });
    await phone.page.locator(".attachment-file-row", { hasText: "frevana-seo-analysis.md" }).locator(".attachment-file-open").click();
    await viewer(phone.page).waitFor();
    const box = await viewer(phone.page).boundingBox();
    assert.equal(Math.round(box.width), 390); assert.equal(Math.round(box.height), 844);
    await phone.page.screenshot({ path: join(shots, "phone-markdown.png") });
    await closeViewer(phone.page);
    await phone.page.locator(".attachment-file-row", { hasText: "data.csv" }).locator(".attachment-file-open").click();
    await viewer(phone.page).waitFor();
    await phone.page.screenshot({ path: join(shots, "phone-csv.png") });
    await closeViewer(phone.page);
    assert.deepEqual(phone.errors, []);
  });
  await step("focus rings are keyboard-only (phone, tap)", () => focusRule(phone.page, { touch: true }));
  await phone.context.close();

  const dark = await open(1180, 820, { theme: "dark", locale: "en" });
  await step("english + dark theme", async () => {
    assert.equal(await dark.page.locator(".attachment-file-row", { hasText: "notes.txt" }).locator("small").innerText(), `${Buffer.byteLength(files.att_txt.body)} B · Click to preview`);
    await card(dark.page, "model.xlsx").click(); await viewer(dark.page).waitFor();
    assert.equal(await viewer(dark.page).locator(".fp-unsupported strong").innerText(), "Preview not available");
    await dark.page.screenshot({ path: join(shots, "ipad-dark-unsupported.png") });
    await closeViewer(dark.page);
    await dark.page.screenshot({ path: join(shots, "ipad-dark-cards.png") });
  });
  await step("focus rings are keyboard-only (dark)", () => focusRule(dark.page));
  await dark.context.close();
} finally {
  await browser.close();
  await server.close();
}
console.log(results.join("\n"));
console.log(process.exitCode ? "file-preview: FAIL" : "file-preview: PASS");
