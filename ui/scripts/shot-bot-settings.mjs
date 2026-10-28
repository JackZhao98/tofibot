// Screenshots, enter/exit frame strips and behaviour checks for Bot settings, Group settings, the Members widget and the New group
// dialog (docs/design/widgets-and-sidebar.md section 8 step 6). Synthetic data only; the browser talks to route stubs, never to a server.
// Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-bot-settings.mjs   (SHOT_DIR=/tmp/surfaces-r3, THEMES=light,dark, STRIPS=0)
// Exit code 1 when a behaviour check fails.
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";

const out = process.env.SHOT_DIR || resolve(ui, "../artifacts/surfaces-r3");
mkdirSync(`${out}/strips`, { recursive: true });
const themes = (process.env.THEMES || "light,dark").split(",");
const NOW = "2026-10-26T12:00:00Z";
const BOT = { id: "bot-synthetic", name: "Mochi", instructions: "You keep the launch calendar and chase owners for updates.\nBe brief and kind.", model: "default", reasoning_effort: "default", effective_model: "synthetic-model", effective_reasoning_effort: "medium", dm_conversation_id: "conv-synthetic", created_at: NOW, is_primary: true };
const BOT2 = { id: "bot-two", name: "Atlas", instructions: "Second synthetic bot", model: "default", dm_conversation_id: "conv-two", created_at: NOW };
const BOT3 = { id: "bot-three", name: "Nori", instructions: "Third synthetic bot", model: "default", dm_conversation_id: "conv-three", created_at: NOW };
const DM = { id: "conv-synthetic", kind: "dm", name: "Mochi", bot_id: BOT.id, bot_ids: [BOT.id], updated_at: NOW };
const GROUP = { id: "conv-group", kind: "group", name: "Launch crew", bot_ids: [BOT.id, BOT2.id], updated_at: "2026-10-26T11:00:00Z" };
const SKILLS = { skills: [
  { name: "web-research", description: "Search the web and read pages", builtin: true, enabled: true },
  { name: "calendar-notes", description: "Keep a running calendar of launches", enabled: true, set_by: "owner", set_at: NOW },
  { name: "release-notes", description: "Draft release notes from merged work", enabled: false },
], bundles: [] };
const SECRETS = { secrets: [{ name: "LAUNCH_API_TOKEN", created_at: NOW }, { name: "STATUS_PAGE_KEY", created_at: NOW, last_used_at: NOW }] };
const TASKS = { tasks: [] };

const { server, origin } = await startUi();
const browser = await launch();
const errors = [];
const failures = [];
const check = (ok, message) => { console.log(`${ok ? "ok  " : "FAIL"} ${message}`); if (!ok) failures.push(message); };

async function open(theme, width, { group = false } = {}) {
  const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme });
  const page = await context.newPage();
  page.on("pageerror", error => errors.push(`${theme} ${width}: ${error.message}`));
  const writes = { patch: [], group: [], primary: 0 };
  const state = { info: { kind: "firecracker", state: "ready" }, capture: "image", messages: [], conversation: DM };
  await installStubs(page, state);
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url()); const p = url.pathname; const method = route.request().method();
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    if (p === "/api/bots" && method === "GET") return json({ bots: [BOT, BOT2, BOT3] });
    if (p === "/api/bots/" + BOT.id && method === "PATCH") { const body = JSON.parse(route.request().postData() || "{}"); writes.patch.push(body); Object.assign(BOT, body); return json(BOT); }
    if (p === "/api/groups" && method === "POST") { const body = JSON.parse(route.request().postData() || "{}"); writes.group.push(body); return json({ id: "conv-new", kind: "group", name: body.name, bot_ids: body.bot_ids, updated_at: NOW }); }
    if (p === "/api/primary-assistant" && method === "PUT") { writes.primary += 1; return json({ bot_id: BOT2.id }); }
    if (p === "/api/conversations") return json({ conversations: [DM, GROUP, { id: "conv-two", kind: "dm", name: "Atlas", bot_id: BOT2.id, bot_ids: [BOT2.id], updated_at: "2026-10-26T10:00:00Z" }] });
    if (p.endsWith("/skills")) return json(SKILLS);
    if (p.endsWith("/secrets")) return json(SECRETS);
    if (p.endsWith("/debug-preview")) return json({ system_prompt: "You are Mochi.\nYou keep the launch calendar.\nBe brief and kind.", tools: [] });
    if (p === "/api/model-settings") return json({ model: "synthetic-model", reasoning_effort: "medium" });
    if (p === "/api/models") return json({ models: [{ id: "synthetic-model", label: "Synthetic model", name: "Synthetic model", provider: "anthropic", reasoning_efforts: ["low", "medium", "high"], default_reasoning: "medium" }, { id: "other-model", label: "Other model", name: "Other model", provider: "anthropic", reasoning_efforts: ["low", "high"], default_reasoning: "low" }], source: "live" });
    if (p.endsWith("/task-cards")) return json(TASKS);
    if (p.endsWith("/task-impact")) return json({ given_tasks: 1, held_tasks: 0, open_waits: 0 });
    return route.fallback();
  });
  await page.goto(origin + "/");
  await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
  if (width < 700) await page.locator(".conversation-row").first().click();
  await page.locator(".chat-header").waitFor();
  await page.waitForTimeout(900);
  if (group) { await page.locator(".conversation-row", { hasText: "Launch crew" }).click(); await page.waitForTimeout(600); }
  return { context, page, writes };
}

async function strip(page, name, action, { count = 5, settle = 700 } = {}) {
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
  const picked = [];
  for (let i = 0; i < count; i++) { const target = start + (i / (count - 1)) * Math.min(0.32, Math.max(0.1, (after.at(-1)?.t ?? start) - start)); picked.push(after.reduce((best, frame) => Math.abs(frame.t - target) < Math.abs(best.t - target) ? frame : best, after[0] ?? frames.at(-1))); }
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
  // ---- 1280: every Bot settings section, the style picker, the dirty bar, confirm-on-close, save ----
  {
    const { context, page, writes } = await open(theme, 1280);
    const shot = async (name, wait = 700) => { await page.waitForTimeout(wait); await page.screenshot({ path: `${out}/${theme}-1280-${name}.png` }); };
    await page.locator(".chat-identity").click();
    await page.locator(".bot-settings").waitFor();
    await shot("bs-1-identity", 900);
    for (const [index, id] of [[2, "role"], [3, "model"], [4, "tools"], [5, "secrets"], [6, "advanced"]]) {
      await page.locator(`.bs-nav-item[data-section="${id}"]`).click();
      await shot(`bs-${index}-${id}`, 600);
    }
    await page.locator('.bs-nav-item[data-section="advanced"]').click();
    await page.locator(".bot-inspector > summary").click(); await shot("bs-6-advanced-prompt-open", 700);
    await page.locator('.bs-nav-item[data-section="identity"]').click(); await page.waitForTimeout(400);
    await page.locator(".bs-avatar-row .bs-button").click();
    await page.locator(".bot-style-pop").waitFor(); await shot("bs-1-identity-style-popover", 700);
    await page.keyboard.press("Escape"); await page.waitForTimeout(400);
    check(await page.locator(".bot-settings").count() === 1, `${theme}: Escape closes the style popover only`);
    // dirty bar
    check(await page.locator(".cover-dirty-bar[data-open]").count() === 0, `${theme}: no dirty bar before an edit`);
    await page.locator('.bs-field input').first().fill("Mochi Prime");
    await page.waitForTimeout(400);
    check(await page.locator(".cover-dirty-bar[data-open]").count() === 1, `${theme}: an edit opens the dirty bar`);
    await shot("bs-7-dirty-bar", 200);
    // switching sections keeps the draft and the bar
    await page.locator('.bs-nav-item[data-section="role"]').click(); await page.waitForTimeout(300);
    check(await page.locator(".cover-dirty-bar[data-open]").count() === 1, `${theme}: the bar stays across sections`);
    await page.locator(".bs-field textarea").fill("Keep the launch calendar.");
    check((await page.locator(".bs-count").innerText()).includes("25"), `${theme}: role character count`);
    // confirm on close
    await page.keyboard.press("Escape"); await page.waitForTimeout(500);
    check(await page.locator(".cover-leave-dialog").count() === 1, `${theme}: Escape with unsaved changes asks`);
    await shot("bs-8-confirm-close", 200);
    await page.getByRole("button", { name: "Keep editing" }).click(); await page.waitForTimeout(400);
    check(await page.locator(".bot-settings").count() === 1 && await page.locator(".cover-leave-dialog").count() === 0, `${theme}: Keep editing returns to the form`);
    await page.locator(".cover-close").click(); await page.waitForTimeout(400);
    check(await page.locator(".cover-leave-dialog").count() === 1, `${theme}: the close button asks too`);
    await page.getByRole("button", { name: "Discard changes" }).click(); await page.waitForTimeout(700);
    check(await page.locator(".cover-window").count() === 0, `${theme}: Discard changes closes the cover`);
    check(writes.patch.length === 0, `${theme}: discarding wrote nothing`);
    // Delete Bot goes through the existing confirm dialog, over the cover
    await page.locator(".chat-identity").click(); await page.locator(".bot-settings").waitFor();
    await page.locator('.bs-nav-item[data-section="advanced"]').click(); await page.waitForTimeout(300);
    await page.locator(".bs-button.is-danger").click(); await page.locator(".delete-dialog").waitFor();
    await shot("bs-10-delete-confirm", 500);
    check(await page.getByRole("button", { name: "Cancel" }).isVisible(), `${theme}: the delete question is readable (the cover closed first)`);
    await page.getByRole("button", { name: "Cancel" }).click(); await page.waitForTimeout(500);
    check(await page.locator(".delete-dialog").count() === 0 && await page.locator(".cover-window").count() === 0, `${theme}: cancelling the delete leaves the chat`);
    // save keeps the cover open
    await page.locator(".chat-identity").click(); await page.locator(".bot-settings").waitFor();
    await page.locator('.bs-field input').first().fill("Mochi Prime");
    await page.locator(".cover-dirty-save").click(); await page.waitForTimeout(900);
    check(writes.patch.length === 1 && writes.patch[0].name === "Mochi Prime", `${theme}: Save sends one PATCH with the form`);
    check(await page.locator(".bot-settings").count() === 1, `${theme}: Save keeps the cover open`);
    check(await page.locator(".cover-dirty-bar[data-open]").count() === 0, `${theme}: the bar leaves after saving`);
    await shot("bs-9-saved", 300);
    BOT.name = "Mochi"; // restore for the next theme
    await context.close();
  }
  // ---- 390: list -> page ----
  {
    const { context, page } = await open(theme, 390);
    const shot = async (name, wait = 700) => { await page.waitForTimeout(wait); await page.screenshot({ path: `${out}/${theme}-390-${name}.png` }); };
    await page.locator(".chat-identity").click(); await page.locator(".bot-settings").waitFor();
    await shot("bs-phone-1-list", 900);
    await page.locator('.bs-nav-item[data-section="model"]').click();
    await shot("bs-phone-2-page-model", 700);
    check(await page.locator(".bot-settings[data-view='page']").count() === 1, `${theme}: phone: choosing a section shows the page`);
    await page.locator(".bs-back").click(); await page.waitForTimeout(500);
    check(await page.locator(".bot-settings[data-view='list']").count() === 1, `${theme}: phone: the back arrow returns to the list`);
    await page.locator('.bs-nav-item[data-section="identity"]').click(); await page.waitForTimeout(500);
    await page.locator('.bs-field input').first().fill("Mochi Prime"); await page.waitForTimeout(400);
    await shot("bs-phone-3-dirty-bar", 200);
    await context.close();
  }
  // ---- group: settings cover, members widget, new group dialog ----
  {
    const { context, page, writes } = await open(theme, 1280, { group: true });
    const shot = async (name, wait = 700) => { await page.waitForTimeout(wait); await page.screenshot({ path: `${out}/${theme}-1280-${name}.png` }); };
    await page.locator(".chat-identity").click(); await page.locator(".members-widget").waitFor();
    await shot("widget-members", 900);
    check(await page.locator(".mw-member").count() === 2, `${theme}: the roster lists both members`);
    await page.locator(".mw-gear").click(); await page.locator(".group-settings").waitFor();
    await shot("group-settings", 900);
    await page.locator(".gs-field input").fill("Launch crew 2"); await page.waitForTimeout(400);
    await shot("group-settings-dirty", 200);
    await page.locator(".gs-member", { hasText: "Atlas" }).click(); await page.waitForTimeout(700);
    await shot("group-settings-removal-impact", 300);
    await page.keyboard.press("Escape"); await page.waitForTimeout(500);
    check(await page.locator(".cover-leave-dialog").count() === 1, `${theme}: group settings asks before losing edits`);
    await page.getByRole("button", { name: "Discard changes" }).click(); await page.waitForTimeout(700);
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Group settings" }).click(); await page.locator(".group-settings").waitFor();
    check(await page.locator(".gs-field input").inputValue() === "Launch crew", `${theme}: More > Group settings opens the cover with the saved name`);
    await page.keyboard.press("Escape"); await page.waitForTimeout(600);
    // new group dialog from the sidebar +
    await page.getByRole("button", { name: "New Bot or group" }).click();
    await page.getByRole("menuitem", { name: "New group", exact: true }).click(); await page.locator(".new-group-dialog").waitFor();
    await shot("new-group-dialog", 900);
    await page.locator(".new-group-dialog input").first().fill("Launch two");
    await page.locator(".new-group-dialog .gs-member", { hasText: "Mochi" }).click();
    await page.locator(".new-group-dialog .gs-member", { hasText: "Atlas" }).click();
    await shot("new-group-dialog-filled", 300);
    await page.locator(".new-group-create").click(); await page.waitForTimeout(900);
    check(writes.group.length === 1 && writes.group[0].bot_ids.length === 2, `${theme}: Create group posts the name and members`);
    await context.close();
  }
  // ---- enter / exit strips ----
  if (process.env.STRIPS !== "0") {
    {
      const { context, page } = await open(theme, 1280);
      const log = (name, times) => console.log(`${theme} ${name}: ${times.join(",")} ms`);
      log("cover-bot-settings-open", await strip(page, `${theme}-1280-cover-bot-settings-open`, () => page.locator(".chat-identity").click())); await page.waitForTimeout(800);
      log("section-switch", await strip(page, `${theme}-1280-section-switch`, () => page.locator('.bs-nav-item[data-section="role"]').click(), { count: 5, settle: 400 })); await page.waitForTimeout(300);
      log("dirty-bar-in", await strip(page, `${theme}-1280-dirty-bar-in`, () => page.locator(".bs-field textarea").fill("Changed role"), { settle: 400 })); await page.waitForTimeout(300);
      log("dirty-bar-out", await strip(page, `${theme}-1280-dirty-bar-out`, () => page.locator(".cover-dirty-discard").click(), { settle: 400 })); await page.waitForTimeout(300);
      log("cover-bot-settings-close", await strip(page, `${theme}-1280-cover-bot-settings-close`, () => page.keyboard.press("Escape"))); await page.waitForTimeout(600);
      await context.close();
    }
    {
      const { context, page } = await open(theme, 1280, { group: true });
      const log = (name, times) => console.log(`${theme} ${name}: ${times.join(",")} ms`);
      log("widget-members-open", await strip(page, `${theme}-1280-widget-members-open`, () => page.locator(".chat-identity").click())); await page.waitForTimeout(800);
      log("cover-group-settings-open", await strip(page, `${theme}-1280-cover-group-settings-open`, () => page.locator(".mw-gear").click())); await page.waitForTimeout(800);
      log("cover-group-settings-close", await strip(page, `${theme}-1280-cover-group-settings-close`, () => page.keyboard.press("Escape"))); await page.waitForTimeout(600);
      log("widget-members-close", await strip(page, `${theme}-1280-widget-members-close`, () => page.locator(".chat-identity").click())); await page.waitForTimeout(600);
      log("new-group-open", await strip(page, `${theme}-1280-new-group-open`, async () => { await page.getByRole("button", { name: "New Bot or group" }).click(); await page.getByRole("menuitem", { name: "New group", exact: true }).click(); })); await page.waitForTimeout(800);
      log("new-group-close", await strip(page, `${theme}-1280-new-group-close`, () => page.keyboard.press("Escape")));
      await context.close();
    }
    {
      const { context, page } = await open(theme, 390);
      const log = (name, times) => console.log(`${theme} 390 ${name}: ${times.join(",")} ms`);
      log("cover-open", await strip(page, `${theme}-390-cover-bot-settings-open`, () => page.locator(".chat-identity").click())); await page.waitForTimeout(800);
      log("page-in", await strip(page, `${theme}-390-section-page-in`, () => page.locator('.bs-nav-item[data-section="role"]').click(), { settle: 500 })); await page.waitForTimeout(500);
      log("page-out", await strip(page, `${theme}-390-section-page-out`, () => page.locator(".bs-back").click(), { settle: 500 })); await page.waitForTimeout(400);
      log("cover-close", await strip(page, `${theme}-390-cover-bot-settings-close`, () => page.keyboard.press("Escape")));
      await context.close();
    }
  }
}
console.log("page errors:", errors.length ? errors : "none");
await browser.close();
await server.close();
console.log(failures.length ? `FAILED ${failures.length}` : "all checks passed", out);
process.exit(failures.length || errors.length ? 1 : 0);
