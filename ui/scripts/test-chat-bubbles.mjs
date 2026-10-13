// Group + direct chat Bot message layout: one container style per conversation type, one avatar+name
// per sender group, avatar top-aligned with the name row, tail (if any) inside the avatar's box.
// Synthetic data (same shape as real messages/runs/tools); the app's /api is answered in the browser.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:chat-bubbles
// BUBBLES_SHOT_PREFIX=before|after names the screenshots in artifacts/chat-bubbles/.
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const shots = join(ui, "..", "artifacts", "chat-bubbles");
mkdirSync(shots, { recursive: true });
const prefix = process.env.BUBBLES_SHOT_PREFIX || "after";

const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });

const INSTANCE = "00000000-0000-4000-8000-000000000001";
const T0 = Date.parse("2026-10-13T10:00:00Z");
const iso = seconds => new Date(T0 + seconds * 1000).toISOString();
const mkBot = (id, name, dm) => ({ id, name, instructions: "Synthetic role", model: "synthetic-model", reasoning_effort: "medium", dm_conversation_id: dm, created_at: iso(0) });
const bots = [mkBot("bot_a", "Ledger Cat", "dm_a"), mkBot("bot_b", "Simile Cat", "dm_b")];
const group = { id: "grp_1", kind: "group", name: "Synthetic Group", bot_ids: ["bot_a", "bot_b"], updated_at: iso(0), read_seq: 0, user_visible: true };
const dm = { id: "dm_a", kind: "dm", name: "Ledger Cat", bot_id: "bot_a", bot_ids: ["bot_a"], updated_at: iso(0), read_seq: 0, user_visible: true };

function build(conv) {
  const bot = (id) => ({ sender_bot_id: id, sender_bot_name: bots.find(b => b.id === id).name });
  let seq = 0;
  const msg = (id, role, content, at, extra = {}) => ({ id, conversation_id: conv, role, content, seq: ++seq, created_at: iso(at), ...extra });
  const longText = "Synthetic reply. ".repeat(24).trim();
  const messages = [
    msg("u1", "user", "Please note the synthetic tip at the bottom of the ledger page.", 0),
    // Run A: three mid-run segments, then the final answer (kind "") with Activity.
    msg("a1", "assistant", "Opening the page to look at its structure first.", 10, { run_id: "run_a", kind: "segment", ...bot("bot_a") }),
    msg("a2", "assistant", "The page body is empty, so I will check the update tool's parameters before appending.", 20, { run_id: "run_a", kind: "segment", ...bot("bot_a") }),
    msg("a3", "assistant", "The tool supports appending at the end; I only append this paragraph.", 30, { run_id: "run_a", kind: "segment", ...bot("bot_a") }),
    msg("a4", "assistant", `The write returned an unclear error, so I read the page back first. ${longText}`, 40, { run_id: "run_a", ...bot("bot_a") }),
    // Run B: tools (Activity) then a final answer with two paragraphs.
    msg("b1", "assistant", `Not written this time. ${longText}\n\nIf you want it added, reply once more and I will retry.`, 80, { run_id: "run_b", ...bot("bot_b") }),
    msg("u2", "user", "@Ledger Cat write the tip again at the bottom of the ledger page.", 400),
    // Run C: activity-only start, then a short final.
    msg("a5", "assistant", "Done, the tip is now at the bottom of the page.", 460, { run_id: "run_c", ...bot("bot_a") }),
    msg("a6", "assistant", "One more note: the older row was left untouched.", 470, { run_id: "run_c2", ...bot("bot_a") }),
  ];
  const run = (id, bot_id, trigger, a, b) => ({ id, conversation_id: conv, bot_id, status: "done", model: "synthetic-model", origin_conversation_id: conv, trigger_message_id: trigger, queue_seq: 1, created_at: iso(a), updated_at: iso(b) });
  const runs = [run("run_a", "bot_a", "u1", 5, 41), run("run_b", "bot_b", "u1", 5, 81), run("run_c", "bot_a", "u2", 405, 461), run("run_c2", "bot_a", "u2", 462, 471)];
  const tool = (run_id, bot_id, n, at) => ({ conversation_id: conv, bot_id, run_id, call_id: `${run_id}_${n}`, name: "synthetic_tool", arguments: "{}", result: "ok", status: "completed", truncated: false, started_at: iso(at), updated_at: iso(at + 3) });
  const tools = [tool("run_a", "bot_a", 1, 12), tool("run_a", "bot_a", 2, 25), tool("run_b", "bot_b", 1, 60), ...[0, 1, 2, 3, 4, 5, 6].map(n => tool("run_c", "bot_a", n, 410 + n * 6))];
  const summary = (run_id, bot_id, n, a) => ({ run_id, bot_id, tool_count: n, completed_count: n, failed_count: 0, interrupted_count: 0, pending_count: 0, started_at: iso(a), updated_at: iso(a + 30) });
  const summaries = [summary("run_a", "bot_a", 2, 12), summary("run_b", "bot_b", 1, 60), summary("run_c", "bot_a", 7, 410)];
  return { messages, runs, tools, summaries };
}

async function mockApi(page, convs) {
  const external = [];
  page.on("request", request => { const url = new URL(request.url()); if (url.origin !== origin && !url.protocol.startsWith("data")) external.push(request.url()); });
  await page.route("**/api/**", route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    const conv = path.split("/")[3] ?? "";
    const data = build(conv);
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true });
    if (path === "/api/config") return json({ model_configured: true, default_model: "synthetic-model", provider: "synthetic" });
    if (path === "/api/bots") return json({ bots });
    if (path === "/api/conversations") return json({ conversations: convs });
    if (path.endsWith("/messages")) return json({ messages: data.messages, drafts: [], has_more: false, event_cursor: 0 });
    if (path.endsWith("/runs")) return json({ runs: data.runs });
    if (path.endsWith("/tools")) {
      if (url.searchParams.has("run_ids")) return json({ summaries: data.summaries });
      if (url.searchParams.has("run_id")) { const rows = data.tools.filter(t => t.run_id === url.searchParams.get("run_id")); return json({ activities: rows, tool_count: rows.length, has_more: false }); }
      return json({ activities: data.tools, summaries: data.summaries, tool_count: data.tools.length, has_more: false });
    }
    if (path.startsWith("/api/computer")) return json({ owner: null, waiting: [] });
    if (path.endsWith("/memories")) return json({ memories: [] });
    if (path.endsWith("/schedules")) return json({ schedules: [] });
    if (path === "/api/questions") return json({ questions: [] });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    if (path === "/api/secret-inputs") return json({ requests: [] });
    return json({});
  });
  return external;
}

async function open(url, { width, height, theme }) {
  const context = await browser.newContext({ viewport: { width, height }, locale: "zh-CN", colorScheme: theme, reducedMotion: "reduce", hasTouch: width < 1100 });
  await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const external = await mockApi(page, [group, dm]);
  await page.goto(origin + url);
  await page.locator(".message-block").first().waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(500);
  return { page, context, errors, external };
}

/** Everything the layout rules need, measured in the page: every Bot unit and every break, in document order. */
function measure() {
  const rect = el => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, height: r.height, width: r.width }; };
  const shown = el => Boolean(el) && el.getBoundingClientRect().height > 0 && getComputedStyle(el).visibility !== "hidden";
  const list = document.querySelector(".message-list");
  const units = [];
  for (const el of list.querySelectorAll("article.message-bot, article.message-user, .task-run-block.in-group, .tool-message.web-tool-run[data-sender], .date-divider, .unread-divider")) {
    if (el.matches(".date-divider, .unread-divider, article.message-user")) { units.push({ type: "break" }); continue; }
    if (el.closest(".message-tools")) continue;
    const message = el.matches("article.message-bot");
    const head = message ? { avatar: el.querySelector(".message-avatar .avatar, .message-avatar img, .message-avatar svg"), name: el.querySelector(".message-meta strong") } : { avatar: el.querySelector(".sender-head .avatar, .sender-head img, .sender-head svg"), name: el.querySelector(".sender-head .sender-name") };
    const avatar = shown(head.avatar) ? rect(head.avatar) : null;
    const name = shown(head.name) ? rect(head.name) : null;
    const unit = { type: message ? "message" : "activity", id: el.closest(".message-block")?.dataset.messageId ?? "", sender: el.dataset.sender ?? "", avatar, name, left: rect(el).left, top: rect(el).top };
    if (message) {
      const content = el.querySelector(".message-content");
      const cs = getComputedStyle(content);
      const box = content.getBoundingClientRect();
      const radii = [cs.borderTopLeftRadius, cs.borderTopRightRadius, cs.borderBottomRightRadius, cs.borderBottomLeftRadius].map(parseFloat);
      const smallest = Math.min(...radii);
      const corner = radii.indexOf(smallest);
      unit.kind = el.classList.contains("message-segment") ? "segment" : "final";
      unit.contentLeft = box.left;
      unit.style = { border: `${cs.borderTopWidth} ${cs.borderTopStyle} ${cs.borderTopColor}`, background: cs.backgroundColor, bodyRadius: String(radii[2]), paddingTop: cs.paddingTop, paddingLeft: cs.paddingLeft };
      // The tail is the one squared corner of the bubble; its vertical position is the corner's.
      unit.tail = smallest < Math.max(...radii) - 1 ? { corner: ["top-left", "top-right", "bottom-right", "bottom-left"][corner], y: corner < 2 ? box.top : box.bottom } : null;
    }
    units.push(unit);
  }
  const names = [...list.querySelectorAll(".sender-name, .message-meta strong, .task-run-identity")].filter(el => el.getBoundingClientRect().height > 0).map(el => ({ cls: el.className, text: el.textContent, opacity: getComputedStyle(el).opacity }));
  const faded = names.filter(n => parseFloat(n.opacity) < 1);
  return { units, names, faded, overflow: document.documentElement.scrollWidth - window.innerWidth };
}

function check(label, m, kind) {
  const failures = [];
  const bots = m.units.filter(u => u.type === "message");
  assert.ok(bots.length >= 6, `${label}: bot messages rendered (${bots.length})`);
  // Same container for every kind (the pointed corner is the only allowed difference).
  const styles = new Set(bots.map(b => JSON.stringify(b.style)));
  if (styles.size !== 1) failures.push(`container styles differ: ${[...styles].join(" | ")}`);
  if (kind === "dm") {
    for (const b of bots) { if (b.avatar || b.name) failures.push(`${b.id}: direct chat shows avatar/name`); if (b.tail) failures.push(`${b.id}: direct chat bubble has a tail`); if (b.style.border.startsWith("1px")) failures.push(`${b.id}: direct chat message is boxed`); }
    if (m.units.some(u => u.type === "activity")) failures.push("direct chat renders group Activity heads");
  } else {
    let previous = null;
    for (const u of m.units) {
      if (u.type === "break") { previous = null; continue; }
      const first = previous === null || previous !== u.sender;
      previous = u.sender;
      const tag = `${u.type} ${u.id || u.sender}`;
      if (first) {
        if (!u.avatar || !u.name) failures.push(`${tag}: first of sender group lacks avatar/name`);
        else if (Math.abs(u.avatar.top - u.name.top) > 4) failures.push(`${tag}: avatar top ${u.avatar.top.toFixed(1)} vs name row ${u.name.top.toFixed(1)}`);
        if (u.type === "message") {
          if (!u.tail) failures.push(`${tag}: first bubble has no tail`);
          else if (u.avatar && !(u.tail.y >= u.avatar.top - 1 && u.tail.y <= u.avatar.bottom + 1)) failures.push(`${tag}: tail y ${u.tail.y.toFixed(1)} outside avatar ${u.avatar.top.toFixed(1)}-${u.avatar.bottom.toFixed(1)}`);
          else if (u.tail.corner !== "top-left") failures.push(`${tag}: tail corner ${u.tail.corner}`);
        }
      } else {
        if (u.avatar || u.name) failures.push(`${tag}: continuation repeats avatar/name`);
        if (u.type === "message" && u.tail) failures.push(`${tag}: tail on a continuation`);
      }
    }
    // One column: every Bot unit starts at the same x.
    const lefts = new Set(m.units.filter(u => u.type !== "break").map(u => Math.round(u.type === "message" ? u.left + (u.contentLeft - u.left) : u.left)));
    if (lefts.size !== 1) failures.push(`unit column differs: ${[...lefts]}`);
  }
  if (m.faded.length) failures.push(`faded name labels: ${JSON.stringify(m.faded)}`);
  if (m.overflow > 0) failures.push(`horizontal overflow ${m.overflow}`);
  assert.deepEqual(failures, [], `${label}\n${failures.join("\n")}`);
}

const viewports = [["1180x820", 1180, 820], ["1024x768", 1024, 768], ["1440x900", 1440, 900], ["390", 390, 844]];
const results = [];
let failed = 0;
try {
  for (const theme of ["light", "dark"]) for (const [name, width, height] of viewports) for (const [kind, url] of [["group", "/g/grp_1"], ["dm", "/b/bot_a"]]) {
    const label = `${kind} ${name} ${theme}`;
    const { page, context, errors, external } = await open(url, { width, height, theme });
    try {
      const m = await page.evaluate(measure);
      await page.screenshot({ path: join(shots, `${prefix}-${kind}-${name}-${theme}.png`), fullPage: false });
      await page.evaluate(() => { const scroller = document.querySelector(".message-list")?.closest(".messages, .chat-scroll, [data-scroll]") ?? document.querySelector(".message-list")?.parentElement; for (let el = scroller; el; el = el.parentElement) if (el.scrollHeight > el.clientHeight + 4) { el.scrollTop = el.scrollHeight; break; } });
      await page.waitForTimeout(300);
      await page.screenshot({ path: join(shots, `${prefix}-${kind}-${name}-${theme}-end.png`), fullPage: false });
      assert.deepEqual(errors, [], `${label}: page errors`);
      assert.deepEqual(external, [], `${label}: external requests`);
      check(label, m, kind);
      results.push(label);
      console.log("PASS", label);
    } catch (error) {
      failed++;
      console.log("FAIL", label, "\n  " + String(error.message).split("\n").slice(0, 8).join("\n  "));
    } finally { await context.close(); }
  }
} finally { await browser.close(); await server.close(); }
if (failed) { console.error(`${failed} chat bubble layout case(s) failed`); process.exit(1); }
console.log(`PASS: ${results.length} chat bubble layout cases`);
