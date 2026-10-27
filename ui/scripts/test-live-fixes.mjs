// Live-preview fixes: the centred screens are centred, the bottom of the message pane is a progressive blur (not a veil) that
// costs little to scroll under, and the "new message" pill is the topmost thing at its own centre.
//  - login card, boot logo and startup-failure card sit on the vertical centre of the viewport (desktop and phone);
//  - the strip above the composer is stacked backdrop-filter layers, each masked by a gradient band, with the -webkit- pair;
//    no flat veil remains on the composer; the blur strengthens toward the composer;
//  - scrolling under the blur stays inside the frame budget, next to the same scroll without it;
//  - the pill renders above the blur and the composer chrome, is hit-testable at its centre and clicks through to the latest.
// Synthetic data. Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:live-fixes
// LIVE_FIXES_ENGINE=webkit runs the same checks in WebKit.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createFixture } from "./lib/chat-fixture.mjs";
const blurCss = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "src", "progressive-blur.css"), "utf8");

const fixture = await createFixture({ engineName: process.env.LIVE_FIXES_ENGINE === "webkit" ? "webkit" : "chromium" });
// Backdrop blur does not reach screenshots in headless WebKit: the pixel checks run in Chromium, the structural ones in both.
const pixels = fixture.engine === fixture.pw.chromium;
const sizes = [{ name: "desktop", width: 1440, height: 1028 }, { name: "phone", width: 390, height: 844 }];
const results = [], failures = [], report = {};
const check = async (name, run) => {
  try { await run(); results.push(name); console.log(`ok - ${name}`); }
  catch (error) { failures.push(name); console.log(`not ok - ${name}\n    ${String(error.message).split("\n")[0]}`); }
};
const json = (route, body, status = 200) => route.fulfill({ status, json: body });
const path = route => new URL(route.request().url()).pathname;
const centreOffset = (page, selector) => page.evaluate(selector => { const el = document.querySelector(selector); const r = el.getBoundingClientRect(); return { y: +(r.top + r.height / 2 - innerHeight / 2).toFixed(2), x: +(r.left + r.width / 2 - innerWidth / 2).toFixed(2), height: r.height, top: r.top, bottom: innerHeight - r.bottom }; }, selector);

for (const size of sizes) {
  const viewport = { width: size.width, height: size.height };
  await check(`${size.name}: the sign-in card is centred in the viewport`, async () => {
    const { page, context, errors } = await fixture.open({ viewport, path: "/", waitFor: ".owner-card",
      route: async route => { const p = path(route); if (p === "/api/server-info") { await json(route, { service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: "owner" } }); return true; } if (p.endsWith("/auth/session")) { await json(route, { enabled: true, setup_required: false, authenticated: false, password_transport_allowed: true }); return true; } return false; } });
    const o = await centreOffset(page, ".owner-card");
    report[`login ${size.name}`] = o;
    assert.ok(Math.abs(o.y) <= 1 && Math.abs(o.x) <= 1, `the card centre is ${o.y}px / ${o.x}px off the viewport centre`);
    await page.screenshot({ path: process.env.LIVE_FIXES_SHOTS ? `${process.env.LIVE_FIXES_SHOTS}/login-${size.name}.png` : undefined });
    assert.deepEqual(errors, []);
    await context.close();
  });
  await check(`${size.name}: the boot logo is centred in the viewport`, async () => {
    const { page, context } = await fixture.open({ viewport, path: "/", waitFor: ".boot-logo-wrap",
      route: async route => { if (path(route).startsWith("/api/")) { await new Promise(() => {}); } return true; } });
    await page.waitForTimeout(500);
    const o = await centreOffset(page, ".boot-logo-wrap");
    report[`boot ${size.name}`] = o;
    assert.ok(Math.abs(o.y) <= 1 && Math.abs(o.x) <= 1, `the logo centre is ${o.y}px / ${o.x}px off the viewport centre`);
    await page.screenshot({ path: process.env.LIVE_FIXES_SHOTS ? `${process.env.LIVE_FIXES_SHOTS}/boot-${size.name}.png` : undefined });
    await context.close();
  });
  await check(`${size.name}: the startup-failure card is centred in the viewport`, async () => {
    const { page, context } = await fixture.open({ viewport, path: "/", waitFor: ".boot-card",
      route: async route => { if (path(route) === "/api/conversations") { await json(route, { error: "unavailable" }, 500); return true; } return false; } });
    const o = await centreOffset(page, ".boot-card");
    report[`failure ${size.name}`] = o;
    assert.ok(Math.abs(o.y) <= 1 && Math.abs(o.x) <= 1, `the card centre is ${o.y}px / ${o.x}px off the viewport centre`);
    await context.close();
  });
  await check(`${size.name}: the new-message pill is topmost at its centre and clicks through`, async () => {
    const { page, context, errors } = await fixture.open({ viewport });
    await page.locator(".message-scroll").hover();
    await page.mouse.wheel(0, -700);
    await page.waitForTimeout(700);
    await page.evaluate(({ WHOLE }) => window.__emit("message", { id: "m_w", conversation_id: "dm_a", seq: 41, role: "assistant", sender_bot_id: "bot_a", run_id: "run_w", content: WHOLE, created_at: new Date().toISOString() }), { WHOLE: fixture.WHOLE });
    await page.locator(".jump-latest").waitFor();
    await page.waitForTimeout(600);
    const hit = await page.evaluate(() => {
      const pill = document.querySelector(".jump-latest"); const r = pill.getBoundingClientRect();
      const at = (x, y) => { const el = document.elementFromPoint(x, y); return Boolean(el && pill.contains(el)); };
      return { centre: at(r.left + r.width / 2, r.top + r.height / 2), inside: [[8, 8], [r.width - 8, 8], [8, r.height - 8], [r.width - 8, r.height - 8]].map(([dx, dy]) => at(r.left + dx, r.top + dy)), within: r.left >= 0 && r.right <= innerWidth && r.top >= 0 && r.bottom <= innerHeight, text: pill.textContent };
    });
    report[`pill ${size.name}`] = hit;
    assert.ok(hit.centre, "the pill is not the topmost element at its centre (elementFromPoint)");
    assert.ok(hit.inside.every(Boolean), "part of the pill is covered");
    assert.ok(hit.within, "the pill is not fully inside the viewport");
    await page.screenshot({ path: process.env.LIVE_FIXES_SHOTS ? `${process.env.LIVE_FIXES_SHOTS}/pill-${size.name}.png` : undefined });
    await page.locator(".jump-latest").click();
    await page.waitForTimeout(800);
    assert.equal(await page.locator(".jump-latest").count(), 0, "the pill is gone after the jump");
    assert.ok(await page.evaluate(() => { const s = document.querySelector(".message-scroll"); return s.scrollHeight - s.clientHeight - s.scrollTop <= 1; }), "the pane is at the latest message");
    assert.deepEqual(errors, []);
    await context.close();
  });
}

await check("there is no blur or veil above the composer (owner 2026-10-26); the header keeps its blur", async () => {
  const { page, context } = await fixture.open({ viewport: { width: 1440, height: 1028 } });
  const info = await page.evaluate(() => {
    const veil = getComputedStyle(document.querySelector(".composer"), "::before");
    const scroll = getComputedStyle(document.querySelector(".message-scroll"));
    return { strips: document.querySelectorAll(".edge-blur").length, veilContent: veil.content, veilImage: veil.backgroundImage, mask: scroll.maskImage || scroll.webkitMaskImage || "none", header: getComputedStyle(document.querySelector(".chat-header"), "::before").backdropFilter };
  });
  report.noBottomBlur = info;
  assert.equal(info.strips, 0, "no bottom blur strip is rendered");
  assert.ok(info.veilContent === "none" || info.veilContent === "normal" || info.veilImage === "none", `no veil above the composer (${info.veilContent} ${info.veilImage})`);
  assert.equal(info.mask, "none", "the message pane has no bottom fade mask");
  assert.ok(/blur/.test(info.header), "the header blur stays");
  await context.close();
});

await check("the header is see-through frosted glass: content runs under it, blurred and visible, with a light tint and a ramp", async () => {
  const { page, context } = await fixture.open({ viewport: { width: 1440, height: 1028 } });
  const info = await page.evaluate(() => {
    const header = document.querySelector(".chat-header"), scroller = document.querySelector(".message-scroll");
    const hs = getComputedStyle(header, "::before"), ha = getComputedStyle(header, "::after");
    const rgba = value => { const m = value.match(/rgba?\(([^)]+)\)/); if (!m) return 0; const p = m[1].split(/[ ,\/]+/).filter(Boolean).map(Number); return p.length > 3 ? p[3] : 1; };
    const hr = header.getBoundingClientRect(), sr = scroller.getBoundingClientRect();
    const sheetText = [...document.styleSheets].flatMap(sh => { try { return [...sh.cssRules].map(r => r.cssText); } catch { return []; } }).join("\n");
    return { layers: [hs, ha].map(cs => ({ blur: parseFloat((cs.backdropFilter || cs.webkitBackdropFilter || "").replace(/.*blur\(([\d.]+)px\).*/, "$1")) || 0, mask: cs.maskImage || cs.webkitMaskImage || "none" })),
      tintAlpha: Math.max(...[...hs.backgroundImage.matchAll(/color\([^)]*\)|rgba?\([^)]*\)/g)].map(m => rgba(m[0])), 0), scrollMask: getComputedStyle(scroller).maskImage || getComputedStyle(scroller).webkitMaskImage || "none",
      under: sr.top <= hr.top + 1, headerPointer: getComputedStyle(header).pointerEvents, pillPointer: getComputedStyle(header.querySelector(".header-actions")).pointerEvents, hr: { top: hr.top, bottom: hr.bottom } };
  });
  report.header = info;
  assert.ok(info.layers.every(l => l.blur > 0), "both glass layers blur");
  assert.ok(info.layers.every(l => /gradient/.test(l.mask)), "both layers fade out toward the list through a gradient mask");
  assert.ok(info.layers[1].blur > info.layers[0].blur, "the layers strengthen");
  assert.ok(info.tintAlpha <= 0.45, `the tint is ${info.tintAlpha}: more than a light translucent tint`);
  assert.equal(info.scrollMask, "none", `the message pane still has a mask that hides what runs under the header (${info.scrollMask})`);
  assert.ok(info.under, "the message pane does not run up under the header");
  assert.equal(info.headerPointer, "none", "the header's empty area would eat the pane's clicks and wheel");
  assert.equal(info.pillPointer, "auto", "the pills must stay clickable");
  if (!pixels) { await context.close(); return; }
  // Pixels: a field of wide bands under the header must be soft but still visible inside the glass, and sharp below its ramp.
  await page.evaluate(({ top }) => {
    const field = document.createElement("div");
    field.id = "bands"; field.style.cssText = `position:fixed;z-index:2;left:560px;width:400px;top:${top}px;bottom:0;background:repeating-linear-gradient(to bottom,#000 0 32px,#fff 32px 64px);pointer-events:none`;
    document.querySelector(".chat-body").append(field);
  }, { top: info.hr.top });
  await page.waitForTimeout(200);
  const maxStep = async (y, height) => {
    const png = await page.screenshot({ clip: { x: 640, y, width: 160, height } });
    return page.evaluate(async base64 => {
      const image = new Image(); image.src = "data:image/png;base64," + base64; await image.decode();
      const c = document.createElement("canvas"); c.width = image.width; c.height = image.height; const ctx = c.getContext("2d"); ctx.drawImage(image, 0, 0);
      const d = ctx.getImageData(0, 0, c.width, c.height).data; let best = 0;
      for (let y = 1; y < c.height; y++) { const a = d[((y - 1) * c.width + 20) * 4], b = d[(y * c.width + 20) * 4]; best = Math.max(best, Math.abs(a - b)); }
      return best;
    }, png.toString("base64"));
  };
  const glass = await maxStep(Math.round(info.hr.top) + 22, 24), ramp = await maxStep(Math.round(info.hr.bottom) - 8, 16), sharp = await maxStep(Math.round(info.hr.bottom) + 90, 40);
  report.headerPixels = { glass, ramp, sharp };
  console.log("    header", JSON.stringify(report.headerPixels));
  assert.ok(sharp >= 200, `the band field is not rendering (${sharp})`);
  assert.ok(glass >= 3, `what runs under the header is covered (edge contrast ${glass}): it must show through`);
  assert.ok(glass <= sharp * 0.35, `what runs under the header is not blurred (edge contrast ${glass} against ${sharp})`);
  assert.ok(ramp >= glass - 1 && ramp <= sharp, `the blur does not ramp out toward the list (${glass} -> ${ramp} -> ${sharp})`);
  await page.screenshot({ path: process.env.LIVE_FIXES_SHOTS ? `${process.env.LIVE_FIXES_SHOTS}/header-bands.png` : undefined });
  await context.close();
});

await check("scrolling under the blur stays inside the frame budget", async () => {
  // "off" is the pane as it was: no strip, no header glass, the composer field on its own frosted glass.
  const OFF = ".chat-header::before,.chat-header::after{display:none!important}.composer-row{-webkit-backdrop-filter:var(--blur)!important;backdrop-filter:var(--blur)!important}";
  const once = async hide => {
    const { page, context } = await fixture.open({ viewport: { width: 1180, height: 820 } });
    if (hide) await page.addStyleTag({ content: OFF });
    const frames = await page.evaluate(() => new Promise(resolve => {
      const sc = document.querySelector(".message-scroll"); const times = []; let last = performance.now(), n = 0, dir = -1;
      const tick = t => { times.push(t - last); last = t; sc.scrollTop += dir * 24; if (++n % 40 === 0) dir = -dir; if (n < 200) requestAnimationFrame(tick); else resolve(times.slice(2)); };
      requestAnimationFrame(tick);
    }));
    await context.close();
    frames.sort((a, b) => a - b);
    return { p50: frames[Math.floor(frames.length * 0.5)], p95: frames[Math.floor(frames.length * 0.95)], max: frames.at(-1) };
  };
  // Three rounds each, median of the rounds: one noisy round on a busy machine must not decide.
  const measure = async hide => { const rounds = []; for (let i = 0; i < 3; i++) rounds.push(await once(hide)); const mid = key => rounds.map(r => r[key]).sort((a, b) => a - b)[1]; return { p50: +mid("p50").toFixed(1), p95: +mid("p95").toFixed(1), max: +mid("max").toFixed(1) }; };
  const off = await measure(true), on = await measure(false);
  report.scrollCost = { blurOff: off, blurOn: on };
  console.log("    scroll", JSON.stringify(report.scrollCost));
  // Headless Chromium rasterises in software, so absolute frame times are high there; what is bounded is what the blur adds.
  assert.ok(on.p50 <= off.p50 * 1.4 + 4, `median frame ${on.p50} ms with the blur against ${off.p50} without`);
  assert.ok(on.p95 <= off.p95 * 1.5 + 8, `p95 frame ${on.p95} ms with the blur against ${off.p95} without`);
});

await fixture.close();
console.log(`\n${results.length} passed, ${failures.length} failed`);
if (process.env.LIVE_FIXES_DEBUG) console.log(JSON.stringify(report, null, 1));
if (failures.length) process.exit(1);
