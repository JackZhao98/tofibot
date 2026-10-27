// Screenshots of widgets, sidebar and covers (docs/design/widgets-and-sidebar.md) at 1440 / 1100 / 960 / 390, light + dark,
// plus frame strips of each enter and exit. Synthetic data only.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-surfaces.mjs   (SHOT_DIR, WIDTHS=1440,1100, THEMES=light,
// SECTIONS=base,r2: "base" is the round-1 set, "r2" the automatic widgets, the strip, the Artifacts cover and their frame strips)
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";
import { BOT as WBOT, DM as WDM, GROUP as WGROUP, botOwner, newState, openWorkspace } from "./widgets-fixture.mjs";

const out = process.env.SHOT_DIR || resolve(ui, "../artifacts/surfaces");
mkdirSync(out, { recursive: true });
const widths = (process.env.WIDTHS || "1440,1100,960,390").split(",").map(Number);
const themes = (process.env.THEMES || "light,dark").split(",");
const sections = (process.env.SECTIONS || "base,r2").split(",");
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
async function strip(page, name, action, { count = 5, settle = 700, clip, detect = false, span = 0.32, appears } = {}) {
  const client = await page.context().newCDPSession(page);
  const frames = [];
  client.on("Page.screencastFrame", event => { frames.push({ t: event.metadata.timestamp, data: event.data }); client.send("Page.screencastFrameAck", { sessionId: event.sessionId }).catch(() => {}); });
  await client.send("Page.startScreencast", { format: "jpeg", quality: 85, everyNthFrame: 1 }); // jpeg: the encoder keeps up, so no painted frame is dropped
  await page.waitForTimeout(120);
  const start = Date.now() / 1000;
  // The moment the selector enters the page, read inside the page (a round trip to Node would arrive late).
  if (appears) await page.evaluate(selector => { window.__appeared = 0; const seen = () => { if (document.querySelector(selector)) { window.__appeared = Date.now() / 1000; return true; } return false; }; if (!seen()) { const watch = new MutationObserver(() => { if (seen()) watch.disconnect(); }); watch.observe(document.documentElement, { childList: true, subtree: true, attributes: true }); } }, appears);
  await action();
  // `appears`: the change comes later than the action (a poll, a stream reconnect). The strip starts when this selector is in the page.
  let appeared = 0;
  if (appears) { await page.waitForFunction(() => window.__appeared > 0, null, { timeout: 15000 }); appeared = await page.evaluate(() => window.__appeared); }
  await page.waitForTimeout(settle);
  await client.send("Page.stopScreencast");
  await client.detach();
  let begin = appeared ? appeared - 0.03 : start;
  const after = frames.filter(frame => frame.t >= begin - 0.02);
  if (after.length < 2) { console.log("strip", name, "caught", after.length, "frames"); }
  const picked = [];
  for (let i = 0; i < count; i++) { const target = begin + (i / (count - 1)) * Math.min(span, Math.max(0.1, (after.at(-1)?.t ?? begin) - begin)); picked.push(after.reduce((best, frame) => Math.abs(frame.t - target) < Math.abs(best.t - target) ? frame : best, after[0] ?? frames.at(-1))); }
  mkdirSync(`${out}/strips`, { recursive: true });
  const { writeFileSync } = await import("node:fs");
  picked.forEach((frame, i) => writeFileSync(`${out}/strips/${name}-f${i + 1}.jpg`, Buffer.from(frame.data, "base64")));
  const start0 = begin;
  const sheet = await page.context().newPage();
  const size = page.viewportSize();
  const tile = 620, tileHeight = Math.round(tile * size.height / size.width);
  await sheet.setViewportSize({ width: tile * 3 + 30, height: ((clip ? Math.round(tile * clip.height / clip.width) : tileHeight) + 22) * Math.ceil(picked.length / 3) + 18 });
  await sheet.setContent(`<body style="margin:0;background:#222;display:flex;flex-wrap:wrap;gap:6px;padding:6px">${picked.map((frame, i) => `<figure style="margin:0;width:${tile}px;color:#ddd;font:12px monospace">${clip ? `<div style="width:${tile}px;height:${Math.round(tile * clip.height / clip.width)}px;overflow:hidden"><img style="display:block;transform-origin:0 0;transform:scale(${tile / clip.width}) translate(${-clip.x}px,${-clip.y}px);width:${size.width}px;height:${size.height}px" src="data:image/jpeg;base64,${frame.data}"></div>` : `<img style="display:block;width:${tile}px;height:${tileHeight}px" src="data:image/jpeg;base64,${frame.data}">`}f${i + 1} +${Math.round((frame.t - start0) * 1000)}ms</figure>`).join("")}</body>`);
  await sheet.screenshot({ path: `${out}/strips/${name}.png` });
  await sheet.close();
  return picked.map(frame => Math.round((frame.t - start0) * 1000));
}

/**
 * Frames of a transition at exact animation times, independent of how loaded the machine is (the screencast above drops frames when it is):
 * every CSS and Web Animation that is running at the moment the change lands is paused in the page (before it can paint), then set to
 * each time in `times` and photographed. `click` is a selector clicked inside the page (so the pause happens in the same task as the React
 * commit); `run` is a Node-side trigger (a state change the page polls or streams in); `appears` is the selector whose arrival is the change.
 */
async function stepStrip(page, name, { click, run, appears, times = [0, 45, 90, 135, 180, 230, 290], clip }) {
  await page.evaluate(async ({ click, appears }) => {
    window.__stepped = false;
    window.__pause = () => { if (window.__stepped) return; window.__stepped = true; window.__anims = document.getAnimations().map(a => ({ a, young: Number(a.currentTime ?? 0) < 600 })); for (const { a } of window.__anims) if (a.playState !== "finished") a.pause(); };
    if (appears) { const seen = () => { if (document.querySelector(appears)) { window.__pause(); return true; } return false; }; if (!seen()) { const watch = new MutationObserver(() => { if (seen()) watch.disconnect(); }); watch.observe(document.documentElement, { childList: true, subtree: true, attributes: true }); } }
    if (click) { document.querySelector(click).click(); /* React flushes a click in a microtask */ for (let i = 0; i < 4; i++) await Promise.resolve(); if (!appears) window.__pause(); }
  }, { click, appears });
  if (run) await run();
  await page.waitForFunction(() => window.__stepped, null, { timeout: 15000 });
  mkdirSync(`${out}/strips`, { recursive: true });
  const shots = [];
  for (const t of times) {
    await page.evaluate(time => { for (const { a, young } of window.__anims) if (young) { try { a.currentTime = time; } catch { /* finished */ } } for (const a of document.getAnimations()) if (!window.__anims.some(item => item.a === a)) a.pause(); }, t);
    await page.waitForTimeout(40);
    const buffer = await page.screenshot(clip ? { clip } : {});
    shots.push(buffer);
    const { writeFileSync } = await import("node:fs");
    writeFileSync(`${out}/strips/${name}-f${shots.length}.png`, buffer);
  }
  await page.evaluate(() => {
    // Hand the page back: what was started by the change runs to its end; what was already running goes on (a finished animation must not be played again: it would restart).
    for (const { a, young } of window.__anims) { try { if (young && a.effect.getComputedTiming().iterations !== Infinity) a.finish(); else if (a.playState === "paused") a.play(); } catch { /* done */ } }
    for (const a of document.getAnimations()) { try { if (a.playState === "paused") a.play(); } catch { /* done */ } }
    window.__stepped = false;
  });
  const sheet = await page.context().newPage();
  const tile = 300, ratio = clip ? clip.height / clip.width : page.viewportSize().height / page.viewportSize().width;
  await sheet.setViewportSize({ width: tile * 4 + 30, height: Math.ceil(shots.length / 4) * (Math.round(tile * ratio) + 20) + 14 });
  await sheet.setContent(`<body style="margin:0;background:#222;display:flex;flex-wrap:wrap;gap:6px;padding:6px">${shots.map((buffer, i) => `<figure style="margin:0;width:${tile}px;color:#ddd;font:11px monospace"><img style="display:block;width:${tile}px" src="data:image/png;base64,${buffer.toString("base64")}">f${i + 1} +${times[i]}ms</figure>`).join("")}</body>`);
  await sheet.screenshot({ path: `${out}/strips/${name}.png` });
  await sheet.close();
  return times;
}

for (const theme of sections.includes("base") ? themes : []) {
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
if (process.env.STRIPS !== "0" && sections.includes("base")) {
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
// ---- round 2: automatic widgets, the computer strip, the Artifacts cover (synthetic workspace, widgets-fixture.mjs) ----
if (sections.includes("r2")) {
  const RIGHT = { x: 1040, y: 0, width: 400, height: 520 };
  for (const theme of themes) {
    const T = `${theme}-1440`;
    const log = (name, times) => console.log(`${theme} ${name}: ${times.join(",")} ms`);
    const quiet = page => page.waitForTimeout(900);
    const file = (page, suffix) => `${out}/${T}-${suffix}.png`;
    const label = (page, text) => page.getByRole("button", { name: text, exact: true });
    // A. Artifacts: appears with the first file; the next one flags its row.
    {
      const state = newState();
      const { context, page } = await openWorkspace(browser, origin, state, { theme });
      await page.waitForTimeout(3500); // the idle chunk warm-up has finished, as it has long before a Bot hands over a file
      await page.screenshot({ path: file(page, "r2-dm-no-files") });
      log("artifacts-auto-appear", await stepStrip(page, `${T}-artifacts-auto-appear`, { appears: '[data-card="artifacts"]', run: async () => { state.addFile(1); }, clip: RIGHT }));
      await page.waitForTimeout(1200);
      await page.screenshot({ path: file(page, "widget-artifacts-auto") });
      state.addFile(2); await page.waitForTimeout(1500); state.addFile(3); await page.waitForTimeout(3200); // the earlier highlights have faded
      log("artifacts-new-highlight", await stepStrip(page, `${T}-artifacts-new-highlight`, { appears: 'li[data-fresh]', run: async () => { state.addFile(4); }, times: [0, 25, 50, 75, 100, 140, 200], clip: RIGHT }));
      await page.screenshot({ path: file(page, "widget-artifacts-4") });
      await quiet(page);
      log("cover-open-from-all", await stepStrip(page, `${T}-artifacts-cover-open-from-all`, { click: ".artifacts-widget-all", appears: ".cover-window", times: [0, 40, 80, 120, 170, 230, 290] }));
      await page.waitForTimeout(900);
      await page.screenshot({ path: file(page, "cover-artifacts-scope-chat") });
      await page.getByRole("tab", { name: "This Bot" }).click(); await page.waitForTimeout(700);
      await page.screenshot({ path: file(page, "cover-artifacts-scope-bot") });
      await page.getByRole("tab", { name: "All", exact: true }).first().click(); await page.waitForTimeout(700);
      await page.screenshot({ path: file(page, "cover-artifacts-scope-all") });
      await page.getByRole("tab", { name: "Images" }).click(); await page.waitForTimeout(600);
      await page.screenshot({ path: file(page, "cover-artifacts-type-images") });
      await page.getByRole("tab", { name: "Documents" }).click(); await page.getByRole("switch").check(); await page.waitForTimeout(600);
      await page.screenshot({ path: file(page, "cover-artifacts-uploads-on") });
      await context.close();
    }
    // B. Computer: appears when a Bot takes the desktop, folds into the strip, unfolds; 30 s after release it folds by itself.
    {
      const state = newState();
      state.addFile(1); state.addFile(2);
      const { context, page } = await openWorkspace(browser, origin, state, { theme });
      await page.waitForTimeout(3500);
      log("computer-auto-appear", await stepStrip(page, `${T}-computer-auto-appear`, { appears: '[data-card="computer"]', run: async () => { state.ownership = botOwner(); }, clip: RIGHT }));
      await page.locator(".desktop-live-screen").waitFor({ state: "attached" });
      await page.waitForTimeout(2000);
      await page.screenshot({ path: file(page, "widget-computer-driven-bot") });
      // There is no fold button on the small screen: the fold is the 30 s one after the Bot lets go.
      state.ownership = { owner: null, waiting: [] };
      await page.waitForTimeout(31500);
      await page.screenshot({ path: file(page, "widget-computer-folded-after-30s") });
      console.log(theme, "computer folded by itself after the Bot let go:", await page.locator(".computer-detail.is-folded").count() === 1);
      // you: taking the mouse opens the big window.
      await page.locator(".desktop-strip-open").click(); await page.waitForTimeout(600);
      await page.locator(".desktop-enlarge").click(); await page.waitForTimeout(800);
      await page.locator(".desktop-driver-switch").click();
      await page.locator(".computer-detail.is-expanded.is-driven-by-you").waitFor({ timeout: 8000 }).catch(() => console.log("no is-driven-by-you"));
      await page.waitForTimeout(700);
      await page.screenshot({ path: file(page, "computer-driven-you-big") });
      await context.close();
    }
    // C. Space: a group with three widgets on a 700 px window.
    {
      const state = newState();
      state.addFile(1, WGROUP);
      const { context, page } = await openWorkspace(browser, origin, state, { theme, group: true, height: 700 });
      await page.locator(".chat-identity").click();
      await label(page, "Open artifacts").click();
      state.ownership = botOwner("run-g", WGROUP);
      await page.waitForTimeout(3500);
      await page.screenshot({ path: file(page, "space-rule-700") });
      await context.close();
    }
  }
  // D. Narrower widths and phone: the Artifacts cover.
  for (const width of [1100, 390]) {
    const state = newState();
    for (let n = 1; n <= 5; n++) state.addFile(n);
    const { context, page } = await openWorkspace(browser, origin, state, { theme: "light", width, height: width < 700 ? 800 : 900 });
    await page.getByRole("button", { name: "Open artifacts" }).click(); await page.waitForTimeout(900);
    if (width >= 1100) await page.getByRole("button", { name: "All artifacts" }).click();
    await page.waitForTimeout(900);
    await page.screenshot({ path: `${out}/light-${width}-r2-artifacts-cover.png` });
    await context.close();
  }
}
console.log("errors:", errors.length ? errors : "none");
await browser.close();
await server.close();
console.log("done", out);
