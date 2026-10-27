// Screenshots of widgets, sidebar and covers (docs/design/widgets-and-sidebar.md) at 1440 / 1100 / 960 / 390, light + dark,
// plus frame strips of each enter and exit. Synthetic data only.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-surfaces.mjs   (SHOT_DIR, WIDTHS=1440,1100, THEMES=light)
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";

const out = process.env.SHOT_DIR || resolve(ui, "../artifacts/surfaces");
mkdirSync(out, { recursive: true });
const widths = (process.env.WIDTHS || "1440,1100,960,390").split(",").map(Number);
const themes = (process.env.THEMES || "light,dark").split(",");
const NOW = "2026-10-26T12:00:00Z";
const BOT = { id: "bot-synthetic", name: "Mochi", instructions: "Synthetic bot", model: "default", dm_conversation_id: "conv-synthetic", created_at: NOW };
const BOT2 = { id: "bot-two", name: "Atlas", instructions: "Second synthetic bot", model: "default", dm_conversation_id: "conv-two", created_at: NOW };
const DM = { id: "conv-synthetic", kind: "dm", name: "Mochi", bot_id: BOT.id, bot_ids: [BOT.id], updated_at: NOW };
const GROUP = { id: "conv-group", kind: "group", name: "Launch crew", bot_ids: [BOT.id, BOT2.id], updated_at: "2026-10-26T11:00:00Z" };
const item = (n, name, mime) => ({ id: `att-${n}`, name, mime, size: 24000 + n * 1000, created_at: `2026-10-26T1${n}:00:00Z`, conversation_id: DM.id, conversation_name: "Mochi", conversation_kind: "dm", conversation_bot_id: BOT.id, conversation_archived: false, bot_id: BOT.id, message_id: `msg-${n}`, message_seq: n, uploaded: false, version_group: `vg-${n}`, version_count: 1 });
const ARTIFACTS = [item(4, "quarterly-report.pdf", "application/pdf"), item(3, "chart.png", "image/svg+xml"), item(2, "notes.md", "text/markdown"), item(1, "data.csv", "text/csv")];
const msg = (n, role, content) => ({ id: `msg-${n}`, conversation_id: DM.id, seq: n, role, content, created_at: `2026-10-26T11:0${n}:00Z`, sender_bot_id: role === "assistant" ? BOT.id : undefined });
const MESSAGES = [msg(1, "user", "Can you pull the quarterly numbers together?"), msg(2, "assistant", "Done. I put the report, the chart and my notes in the files below."), msg(3, "user", "Great, thanks. Please also keep the raw data."), msg(4, "assistant", "The raw data is saved as data.csv.")];
const TASKS = [{ id: "w1", conversation_id: DM.id, bot_id: BOT.id, kind: "task", title: "Draft the quarterly report", description: "", status: "in_progress", created_at: NOW, updated_at: NOW }];

const { server, origin } = await startUi();
const browser = await launch();
const errors = [];

async function open(theme, width) {
  const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme });
  const page = await context.newPage();
  page.on("pageerror", error => errors.push(`${theme} ${width}: ${error.message}`));
  const state = { info: { kind: "firecracker", state: "ready" }, capture: "image", messages: MESSAGES, conversation: DM };
  await installStubs(page, state);
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url()); const p = url.pathname;
    const json = body => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (p === "/api/bots") return json({ bots: [BOT, BOT2] });
    if (p === "/api/conversations") return json({ conversations: [DM, GROUP, { id: "conv-two", kind: "dm", name: "Atlas", bot_id: BOT2.id, bot_ids: [BOT2.id], updated_at: "2026-10-26T10:00:00Z" }] });
    if (p === "/api/artifacts") return json({ items: ARTIFACTS.slice(0, Number(url.searchParams.get("limit") || 50)), next_cursor: "" });
    if (p.endsWith("/work-items") || p === "/api/work-items") return json({ work_items: TASKS });
    return route.fallback();
  });
  await page.goto(origin + "/");
  await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
  if (width < 700) await page.locator(".conversation-row").first().click();
  await page.locator(".chat-header").waitFor();
  await page.waitForTimeout(900);
  return { context, page, state };
}

/**
 * Frames of a transition, taken from the browser's own screencast (every painted frame with its timestamp), so the strip shows what
 * the person saw rather than what a screenshot loop happened to catch. `action` runs after the recording starts; `settle` ms later the
 * frames are cut to `count` evenly spaced ones between the action and the last change, written as <name>-f1..fN.png plus a contact sheet.
 */
async function strip(page, name, action, { count = 5, settle = 700, clip } = {}) {
  const client = await page.context().newCDPSession(page);
  const frames = [];
  client.on("Page.screencastFrame", event => { frames.push({ t: event.metadata.timestamp, data: event.data }); client.send("Page.screencastFrameAck", { sessionId: event.sessionId }).catch(() => {}); });
  await client.send("Page.startScreencast", { format: "png", everyNthFrame: 1 });
  await page.waitForTimeout(120);
  const start = Date.now() / 1000;
  await action();
  await page.waitForTimeout(settle);
  await client.send("Page.stopScreencast");
  await client.detach();
  const after = frames.filter(frame => frame.t >= start - 0.02);
  if (after.length < 2) { console.log("strip", name, "caught", after.length, "frames"); }
  const picked = [];
  for (let i = 0; i < count; i++) { const target = start + (i / (count - 1)) * Math.min(0.32, Math.max(0.1, (after.at(-1)?.t ?? start) - start)); picked.push(after.reduce((best, frame) => Math.abs(frame.t - target) < Math.abs(best.t - target) ? frame : best, after[0] ?? frames.at(-1))); }
  mkdirSync(`${out}/strips`, { recursive: true });
  const { writeFileSync } = await import("node:fs");
  picked.forEach((frame, i) => writeFileSync(`${out}/strips/${name}-f${i + 1}.png`, Buffer.from(frame.data, "base64")));
  const sheet = await page.context().newPage();
  const size = page.viewportSize();
  const tile = 620, tileHeight = Math.round(tile * size.height / size.width);
  await sheet.setViewportSize({ width: tile * 3 + 30, height: (tileHeight + 22) * Math.ceil(picked.length / 3) + 18 });
  await sheet.setContent(`<body style="margin:0;background:#222;display:flex;flex-wrap:wrap;gap:6px;padding:6px">${picked.map((frame, i) => `<figure style="margin:0;width:${tile}px;color:#ddd;font:12px monospace"><img style="display:block;width:${tile}px;height:${tileHeight}px" src="data:image/png;base64,${frame.data}">f${i + 1} +${Math.round((frame.t - start) * 1000)}ms</figure>`).join("")}</body>`);
  await sheet.screenshot({ path: `${out}/strips/${name}.png` });
  await sheet.close();
  return picked.map(frame => Math.round((frame.t - start) * 1000));
}

for (const theme of themes) {
  for (const width of widths) {
    const { context, page } = await open(theme, width);
    const file = suffix => `${out}/${theme}-${width}-${suffix}.png`;
    const shot = async (suffix, wait = 900) => { await page.waitForTimeout(wait); await page.screenshot({ path: file(suffix) }); };
    const label = text => page.getByRole("button", { name: text, exact: true });
    await shot("dm-header", 0);
    if (width >= 1100) {
      await page.locator("[data-computer-toggle]").click();
      await label("Open artifacts").click();
      await shot("widgets");
      await label("Tasks").click();
      await shot("tasks-sidebar");
      await page.locator(".sidebar-shell-expand").click();
      await shot("sidebar-expanded");
      await page.locator(".sidebar-shell-expand").click();
      await page.waitForTimeout(400);
      await page.keyboard.press("Escape"); await page.waitForTimeout(400);
    } else if (width >= 768) {
      await label("Tasks").click();
      await shot("tasks-sidebar");
      await page.keyboard.press("Escape"); await page.waitForTimeout(500);
    }
    await page.locator(".chat-identity").click();
    await shot("bot-settings-cover");
    await page.keyboard.press("Escape"); await page.waitForTimeout(500);
    if (width >= 1100) { await page.getByRole("button", { name: "All artifacts" }).click(); }
    else await label("Open artifacts").click();
    await shot("artifacts-cover");
    await page.keyboard.press("Escape"); await page.waitForTimeout(500);
    if (width >= 1100) {
      await page.keyboard.press("Escape"); await page.waitForTimeout(500);
      // Group header and its Members widget.
      await page.locator(".conversation-row", { hasText: "Launch crew" }).click(); await page.waitForTimeout(600);
      await shot("group-header", 0);
      await page.locator(".chat-identity").click();
      await shot("group-members-widget");
    }
    await context.close();
  }
}

// ---- enter / exit strips (1440, and the cover at 390) ----
if (process.env.STRIPS !== "0") {
  for (const theme of themes) {
    {
      const { context, page } = await open(theme, 1440);
      const label = text => page.getByRole("button", { name: text, exact: true });
      const log = (name, times) => console.log(`${theme} ${name}: ${times.join(",")} ms`);
      const quiet = () => page.waitForTimeout(900);
      const T = `${theme}-1440`;
      log("widget-computer-enter", await strip(page, `${T}-widget-computer-enter`, () => page.locator("[data-computer-toggle]").click())); await quiet();
      log("widget-artifacts-enter", await strip(page, `${T}-widget-artifacts-enter`, () => label("Open artifacts").click())); await quiet();
      log("widget-fold", await strip(page, `${T}-widget-fold`, () => page.locator('[data-card="artifacts"] .context-card-fold').click())); await quiet();
      log("widget-unfold", await strip(page, `${T}-widget-unfold`, () => page.locator('[data-card="artifacts"] .context-card-fold').click())); await quiet();
      log("widget-artifacts-exit", await strip(page, `${T}-widget-artifacts-exit`, () => label("Open artifacts").click())); await quiet();
      log("widget-computer-exit", await strip(page, `${T}-widget-computer-exit`, () => page.locator("[data-computer-toggle]").click())); await quiet();
      log("sidebar-open", await strip(page, `${T}-sidebar-open`, () => label("Tasks").click())); await quiet();
      log("sidebar-expand", await strip(page, `${T}-sidebar-expand`, () => page.locator(".sidebar-shell-expand").click())); await quiet();
      log("sidebar-collapse", await strip(page, `${T}-sidebar-collapse`, () => page.locator(".sidebar-shell-expand").click())); await quiet();
      log("sidebar-replace", await strip(page, `${T}-sidebar-replace`, async () => { await label("More actions").click(); await page.getByRole("menuitem", { name: "This Bot's memory" }).click(); })); await quiet();
      log("sidebar-close", await strip(page, `${T}-sidebar-close`, () => page.getByRole("button", { name: /^Close/ }).first().click())); await quiet();
      log("cover-open", await strip(page, `${T}-cover-bot-settings-open`, () => page.locator(".chat-identity").click())); await quiet();
      log("cover-close", await strip(page, `${T}-cover-bot-settings-close`, () => page.keyboard.press("Escape"))); await quiet();
      log("cover-artifacts-over-widgets", await (async () => { await label("Open artifacts").click(); await page.waitForTimeout(800); await page.locator("[data-computer-toggle]").click(); await page.waitForTimeout(900); return strip(page, `${T}-cover-over-widgets-open`, () => page.getByRole("button", { name: "All artifacts" }).click()); })()); await quiet();
      log("cover-over-widgets-close", await strip(page, `${T}-cover-over-widgets-close`, () => page.keyboard.press("Escape")));
      await context.close();
    }
    {
      const { context, page } = await open(theme, 390);
      const log = (name, times) => console.log(`${theme} 390 ${name}: ${times.join(",")} ms`);
      log("cover-open", await strip(page, `${theme}-390-cover-bot-settings-open`, () => page.locator(".chat-identity").click())); await page.waitForTimeout(900);
      log("cover-close", await strip(page, `${theme}-390-cover-bot-settings-close`, () => page.keyboard.press("Escape")));
      await context.close();
    }
  }
}
console.log("errors:", errors.length ? errors : "none");
await browser.close();
await server.close();
console.log("done", out);
