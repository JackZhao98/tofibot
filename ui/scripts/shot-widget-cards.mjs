// Screenshots of the Terminal widget (owner 2026-11-14: cards in the widget column, not a sidebar panel).
// Synthetic data only. Run: PLAYWRIGHT_MODULE=<playwright/index.mjs> node scripts/shot-widget-cards.mjs   (SHOT_DIR, THEMES=light,dark)
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { installStubs, launch, startUi, ui } from "./computer-window-fixture.mjs";

const out = process.env.SHOT_DIR || resolve(ui, "../artifacts/widget-cards");
mkdirSync(out, { recursive: true });
const themes = (process.env.THEMES || "light,dark").split(",");
const NOW = "2026-11-14T12:00:00Z";
const BOT = { id: "bot-synthetic", name: "Mochi", instructions: "Synthetic bot", model: "default", dm_conversation_id: "conv-synthetic", created_at: NOW };
const BOT2 = { id: "bot-two", name: "Atlas", instructions: "Second synthetic bot", model: "default", dm_conversation_id: "conv-two", created_at: NOW };
const DM = { id: "conv-synthetic", kind: "dm", name: "Mochi", bot_id: BOT.id, bot_ids: [BOT.id], updated_at: NOW };
const DM2 = { id: "conv-two", kind: "dm", name: "Atlas", bot_id: BOT2.id, bot_ids: [BOT2.id], updated_at: "2026-11-14T10:00:00Z" };
const msg = (n, role, content, extra = {}) => ({ id: `msg-${n}`, conversation_id: DM.id, seq: n, role, content, created_at: `2026-11-14T11:0${n}:00Z`, sender_bot_id: role === "assistant" ? BOT.id : undefined, ...extra });
const MESSAGES = [
  msg(1, "user", "Can you ask Atlas for the latest numbers and put them in a table?"),
  msg(2, "system", "Mochi messaged Atlas", { kind: "message_ref", notice: { target_conversation_id: DM2.id, from_bot_id: BOT.id, to_bot_id: BOT2.id, target_bot_ids: [BOT2.id] } }),
  msg(3, "assistant", "Atlas sent the numbers. The table is below."),
];
const OTHER = [
  { id: "o1", conversation_id: DM2.id, seq: 1, role: "assistant", sender_bot_id: BOT.id, content: "Atlas, please send the Q3 revenue by region.", created_at: "2026-11-14T11:02:00Z" },
  { id: "o2", conversation_id: DM2.id, seq: 2, role: "assistant", sender_bot_id: BOT2.id, content: "Here it is: North 1.2M, South 0.9M, West 1.4M. The source is the October export.", created_at: "2026-11-14T11:03:00Z" },
];
const b64 = text => Buffer.from(text).toString("base64");

const { server, origin } = await startUi();
const browser = await launch();
const errors = [];
for (const theme of themes) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2, colorScheme: theme });
  const page = await context.newPage();
  page.on("pageerror", error => errors.push(`${theme}: ${error.message}`));
  await installStubs(page, { info: { kind: "firecracker", state: "ready" }, capture: "image", messages: MESSAGES, conversation: DM });
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url()); const p = url.pathname;
    const json = body => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (p === "/api/bots") return json({ bots: [BOT, BOT2] });
    if (p === "/api/conversations") return json({ conversations: [DM, DM2] });
    if (p === `/api/conversations/${DM2.id}/messages`) return json({ messages: OTHER, has_more: false, event_cursor: 0 });
    if (p === "/api/computers/firecracker/actions") {
      const body = JSON.parse(route.request().postData() || "{}");
      if (body.action === "terminal.list") return json({ ok: true, result: { sessions: [{ id: "t1", command: "exec bash -i", closed: false, exited: false, exit_code: 0 }], active_run_ids: [] } });
      if (body.action === "terminal.read") return json({ ok: true, result: { data_base64: body.cursor ? "" : b64("mochi@computer:~$ ls reports\r\nq3-revenue.csv  q3-summary.md\r\nmochi@computer:~$ "), next_cursor: 80 } });
      return json({ ok: true, result: {} });
    }
    return route.fallback();
  });
  await page.goto(origin + "/");
  await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
  await page.locator(".chat-header").waitFor();
  await page.waitForTimeout(800);
  // Terminal: the "command finished" line opens it for a Bot.
  await page.evaluate(id => window.dispatchEvent(new CustomEvent("tofi:open-terminal", { detail: { botId: id } })), BOT.id);
  await page.locator('.context-card[data-card="terminal"] .terminal-screen').waitFor({ timeout: 8000 });
  if (await page.locator(".sidebar-shell .terminal-panel").count()) errors.push(`${theme}: a sidebar panel opened`);
  await page.screenshot({ path: resolve(out, `cards-${theme}@2x.png`) });
  await page.locator(".widget-column").screenshot({ path: resolve(out, `column-${theme}@2x.png`) });
  await context.close();
}
await browser.close();
await server.close();
if (errors.length) { console.error(errors.join("\n")); process.exit(1); }
console.log(out);
