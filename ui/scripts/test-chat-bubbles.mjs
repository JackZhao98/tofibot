// Group + direct chat Bot message layout: one container style per conversation type, one avatar+name
// per sender group, avatar top-aligned with the name row, tail (if any) inside the avatar's box.
// Synthetic data (same shape as real messages/runs/tools); the app's /api is answered in the browser.
// Run: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:chat-bubbles
// BUBBLES_SHOT_PREFIX=before|after names the screenshots in artifacts/chat-bubbles/.
import { auditFocus, focusRingProbe } from "./focus-audit.mjs";
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const shots = join(ui, "..", "artifacts", "chat-bubbles2");
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

// Per-test additions to the synthetic world: extra runs/messages/tools/summaries, open questions, run overrides.
const emptyWorld = () => ({ messages: [], runs: [], tools: [], summaries: [], questions: [], showActivity: false });

function build(conv, world = emptyWorld()) {
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
  const extraSeq = messages.length;
  messages.push(...world.messages.map((m, i) => ({ conversation_id: conv, seq: extraSeq + i + 1, ...m })));
  return { messages, runs: [...runs, ...world.runs.map(r => ({ conversation_id: conv, origin_conversation_id: conv, queue_seq: 1, model: "synthetic-model", ...r }))], tools: [...tools, ...world.tools.map(t => ({ conversation_id: conv, ...t }))], summaries: [...summaries, ...world.summaries] };
}

async function mockApi(page, convs, world = emptyWorld()) {
  const external = [];
  page.on("request", request => { const url = new URL(request.url()); if (url.origin !== origin && !url.protocol.startsWith("data")) external.push(request.url()); });
  await page.route("**/api/**", route => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const json = body => route.fulfill({ status: 200, json: body });
    const conv = path.split("/")[3] ?? "";
    const data = build(conv, world);
    if (path === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: { mode: "none" } });
    if (path === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, show_activity: world.showActivity });
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
    if (path === "/api/questions") return json({ questions: world.questions.map(q => ({ conversation_id: conv || q.conversation_id, ...q })) });
    if (path === "/api/mail-drafts") return json({ drafts: [] });
    return json({});
  });
  return external;
}

async function open(url, { width, height, theme, world = emptyWorld(), touch = width < 1100, mobile = false, fakeEvents = false, motion = "reduce" }) {
  const context = await browser.newContext({ viewport: { width, height }, locale: "zh-CN", colorScheme: theme, reducedMotion: motion, hasTouch: touch, isMobile: mobile });
  if (fakeEvents) await context.addInitScript(() => {
    // A controllable event stream: tests push run/tool events as the server would.
    window.__streams = [];
    window.EventSource = class { constructor(url) { this.url = url; this.listeners = {}; window.__streams.push(this); setTimeout(() => this.onopen?.(), 0); } addEventListener(type, fn) { (this.listeners[type] ??= []).push(fn); } close() {} };
    let id = 1000;
    window.__emit = (type, data) => { id += 1; for (const stream of window.__streams) if (stream.url.includes("/events")) for (const fn of stream.listeners[type] ?? []) fn({ data: JSON.stringify(data), lastEventId: String(id) }); };
  });
  await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  // React reports a duplicated list key as a console error, not an exception: a conversation or message drawn twice must fail
  page.on("console", message => { if (message.type() === "error" && /same key/i.test(message.text())) errors.push("duplicate React key: " + message.text()); });
  const external = await mockApi(page, [group, dm], world);
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
    if (el.matches(".date-divider, .unread-divider, article.message-user")) { units.push({ type: "break", user: el.matches("article.message-user"), top: rect(el).top, bottom: rect(el).bottom, speakerChange: el.dataset.speakerChange === "true" }); continue; }
    if (el.closest(".message-tools")) continue;
    const message = el.matches("article.message-bot");
    const head = message ? { avatar: el.querySelector(".sender-avatar .avatar, .sender-avatar img, .sender-avatar svg"), name: el.querySelector(".message-meta strong") } : { avatar: el.querySelector(".sender-head .avatar, .sender-head img, .sender-head svg"), name: el.querySelector(".sender-head .sender-name") };
    const avatar = shown(head.avatar) ? rect(head.avatar) : null;
    const name = shown(head.name) ? rect(head.name) : null;
    const unit = { type: message ? "message" : "activity", id: el.closest(".message-block")?.dataset.messageId ?? "", sender: el.dataset.sender ?? "", avatar, name, left: rect(el).left, top: rect(el).top, bottom: rect(el).bottom, speakerChange: el.dataset.speakerChange === "true" };
    if (message) {
      const content = el.querySelector(".message-content");
      const cs = getComputedStyle(content);
      const box = content.getBoundingClientRect();
      const radii = [cs.borderTopLeftRadius, cs.borderTopRightRadius, cs.borderBottomRightRadius, cs.borderBottomLeftRadius].map(parseFloat);
      const smallest = Math.min(...radii);
      const corner = radii.indexOf(smallest);
      unit.kind = el.classList.contains("message-segment") ? "segment" : "final";
      unit.contentLeft = box.left; unit.bubbleBottom = box.bottom; unit.bubbleTop = box.top;
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
    // Sender groups run in document order: the name is a light caption above the first unit, the avatar
    // sits level with the last unit's bottom edge, and a speaker change has more room above it.
    const seq = m.units;
    for (let i = 0; i < seq.length; i++) {
      const u = seq[i];
      if (u.type === "break") continue;
      const prev = seq[i - 1];
      const next = seq[i + 1];
      const first = !prev || prev.type === "break" || prev.sender !== u.sender;
      const last = !next || next.type === "break" || next.sender !== u.sender;
      const tag = `${u.type} ${u.id || u.sender}`;
      if (first) {
        if (!u.name) failures.push(`${tag}: first of sender group lacks its name`);
        else if (u.type === "message" && u.name.bottom > u.bubbleTop + 1) failures.push(`${tag}: name is not above the bubble`);
      } else if (u.name) failures.push(`${tag}: later unit repeats the name`);
      if (last) {
        if (!u.avatar) failures.push(`${tag}: last of sender group lacks the avatar`);
        else {
          const edge = u.type === "message" ? u.bubbleBottom : u.bottom;
          if (Math.abs(u.avatar.bottom - edge) > 4) failures.push(`${tag}: avatar bottom ${u.avatar.bottom.toFixed(1)} vs unit bottom ${edge.toFixed(1)}`);
        }
        if (u.type === "message") {
          if (!u.tail) failures.push(`${tag}: last bubble has no tail`);
          else if (u.tail.corner !== "bottom-left") failures.push(`${tag}: tail corner ${u.tail.corner}`);
          else if (u.avatar && !(u.tail.y >= u.avatar.top - 1 && u.tail.y <= u.avatar.bottom + 4)) failures.push(`${tag}: tail y ${u.tail.y.toFixed(1)} outside avatar`);
        }
      } else {
        if (u.avatar) failures.push(`${tag}: earlier unit shows the avatar`);
        if (u.type === "message" && u.tail) failures.push(`${tag}: tail on a non-last bubble`);
      }
      if (prev && prev.top !== undefined && u.top !== undefined) {
        const gap = u.top - prev.bottom;
        if (first && gap < 18) failures.push(`${tag}: gap ${gap.toFixed(1)}px after another speaker (want >= 18)`);
        if (!first && gap > 14) failures.push(`${tag}: gap ${gap.toFixed(1)}px inside one sender's group (want <= 14)`);
      }
    }
    // One column: every Bot unit starts at the same x.
    const lefts = new Set(m.units.filter(u => u.type !== "break").map(u => Math.round(u.type === "message" ? u.contentLeft : u.left)));
    if (lefts.size !== 1) failures.push(`unit column differs: ${[...lefts]}`);
  }
  if (m.faded.length) failures.push(`faded name labels: ${JSON.stringify(m.faded)}`);
  if (m.overflow > 0) failures.push(`horizontal overflow ${m.overflow}`);
  assert.deepEqual(failures, [], `${label}\n${failures.join("\n")}`);
}

/** Scrolled to the end, the last unit must sit fully above the composer fade, clear of the perched cat. */
async function checkBottomRoom(page, label) {
  await page.evaluate(() => { const s = document.querySelector(".message-scroll"); s.scrollTop = s.scrollHeight; });
  await page.waitForTimeout(250);
  const r = await page.evaluate(() => {
    const composer = document.querySelector(".workspace .composer");
    const list = document.querySelector(".message-list");
    const kids = [...list.querySelectorAll("article.message-bot, article.message-user, .task-run-block, .tool-message.web-tool-run[data-sender]")].filter(el => el.getBoundingClientRect().height > 0 && !el.closest(".message-tools"));
    const last = kids[kids.length - 1].getBoundingClientRect();
    const cat = document.querySelector(".composer-perch")?.getBoundingClientRect();
    const top = composer.getBoundingClientRect().top;
    const overlapsCat = cat ? last.bottom > cat.top && last.right > cat.left && last.left < cat.right : false;
    return { lastBottom: last.bottom, fadeStart: top - 24, overlapsCat, composerHeight: composer.getBoundingClientRect().height };
  });
  assert.ok(r.lastBottom <= r.fadeStart + 0.5, `${label}: last message bottom ${r.lastBottom.toFixed(1)} is inside the fade (starts ${r.fadeStart.toFixed(1)})`);
  assert.equal(r.overlapsCat, false, `${label}: perched cat overlaps the last message`);
  return r.composerHeight;
}

const viewports = [["1180x820", 1180, 820], ["1024x768", 1024, 768], ["1440x900", 1440, 900], ["390", 390, 844]];
const results = [];
let failed = 0;
try {
  for (const showActivity of [false, true]) for (const theme of ["light", "dark"]) for (const [name, width, height] of viewports) for (const [kind, url] of [["group", "/g/grp_1"], ["dm", "/b/bot_a"]]) {
    const label = `${kind} ${name} ${theme}${showActivity ? " activity-on" : ""}`;
    const world = { ...emptyWorld(), showActivity };
    const { page, context, errors, external } = await open(url, { width, height, theme, world });
    try {
      const m = await page.evaluate(measure);
      await page.screenshot({ path: join(shots, `${prefix}${showActivity ? "-activity-on" : ""}-${kind}-${name}-${theme}.png`), fullPage: false });
      assert.deepEqual(errors, [], `${label}: page errors`);
      assert.deepEqual(external, [], `${label}: external requests`);
      check(label, m, kind);
      const one = await checkBottomRoom(page, `${label} (1 line)`);
      await page.screenshot({ path: join(shots, `${prefix}${showActivity ? "-activity-on" : ""}-${kind}-${name}-${theme}-end.png`) });
      await page.locator(".workspace .composer textarea").fill("line one\nline two\nline three\nline four");
      await page.waitForTimeout(300);
      const four = await checkBottomRoom(page, `${label} (4 lines)`);
      assert.ok(four > one + 20, `${label}: composer grew (${one} -> ${four})`);
      // Default: the timeline carries only the Bot's messages. Toggle on: Activity rows come back.
      const rows = await page.evaluate(() => ({ blocks: document.querySelectorAll(".message-list .task-run-block").length, tool: document.querySelectorAll(".message-list .web-tool-run").length, activity: document.querySelectorAll(".message-list .task-activity").length, heads: document.querySelectorAll(".message-list .sender-head").length, text: document.querySelector(".message-list").textContent }));
      if (showActivity) assert.ok(rows.activity + rows.tool > 0, `${label}: Activity rows return with the toggle on`);
      else { assert.deepEqual([rows.blocks, rows.tool, rows.activity, rows.heads], [0, 0, 0, 0], `${label}: finished runs draw no Activity row, block or orphan name label`); assert.ok(!rows.text.includes("工作过程"), `${label}: no Activity text in the timeline`); }
      results.push(label);
      console.log("PASS", label);
    } catch (error) {
      failed++;
      console.log("FAIL", label, "\n  " + String(error.message).split("\n").slice(0, 8).join("\n  "));
    } finally { await context.close(); }
  }

  // ---- Activity hidden by default: live line, cards, "View activity", toggle, grouping -----------------------
  const heroDir = join(ui, "..", "artifacts", "hide-activity");
  mkdirSync(heroDir, { recursive: true });
  const scenario = async (name, fn) => {
    try { await fn(); results.push(name); console.log("PASS", name); }
    catch (error) { failed++; console.log("FAIL", name, "\n  " + String(error.stack ?? error.message).split("\n").slice(0, 10).join("\n  ")); }
  };
  const lastMessageId = "a6";
  const liveWorld = (conv) => ({
    ...emptyWorld(),
    messages: [{ id: "u3", role: "user", content: "@Simile Cat look something up on the web.", created_at: iso(900), run_id: "run_live" }],
    runs: [{ id: "run_live", bot_id: "bot_b", conversation_id: conv, status: "running", trigger_message_id: "u3", created_at: new Date(Date.now() - 10_000).toISOString(), updated_at: new Date().toISOString() }],
    tools: [{ bot_id: "bot_b", run_id: "run_live", call_id: "live_1", name: "computer_action", arguments: '{"action":"browser.navigate","url":"https://example.com/docs"}', result: "", status: "running", truncated: false, started_at: new Date(Date.now() - 5_000).toISOString(), updated_at: new Date().toISOString() }],
  });
  for (const [kind, url, conv] of [["group", "/g/grp_1", "grp_1"], ["dm", "/b/bot_a", "dm_a"]]) {
    const botId = kind === "group" ? "bot_b" : "bot_a";
    await scenario(`live status line ${kind}`, async () => {
      const world = liveWorld(conv);
      if (kind === "dm") { world.runs[0].bot_id = "bot_a"; world.tools[0].bot_id = "bot_a"; }
      const { page, context, errors } = await open(url, { width: 1180, height: 820, theme: "light", world, fakeEvents: true });
      try {
        await page.waitForSelector(".task-run-live-line");
        const live = await page.evaluate(() => ({ lines: [...document.querySelectorAll(".task-run-live-line")].map(el => el.textContent), steps: document.querySelectorAll(".task-step, .task-activity, .web-tool-run:not(.live-run-status)").length, heads: document.querySelectorAll(".message-list .task-run-block .sender-head").length, names: [...document.querySelectorAll(".task-run-live-line .task-activity-name")].map(el => el.textContent), cats: document.querySelectorAll(".message-list .task-run-block :is(.task-activity-cat, .sender-avatar)").length, last: document.querySelector(".message-list").lastElementChild?.className ?? "" }));
        assert.equal(live.lines.length, 1, "exactly one live status line");
        assert.match(live.lines[0], /正在浏览网页 · \d+s/, "the live line names the current action and the elapsed time");
        assert.equal(live.steps, 0, "no tool list while live");
        assert.equal(live.heads, 0, "a live-only block carries no sender caption");
        assert.deepEqual(live.names, kind === "group" ? ["Simile Cat"] : [], "the live line names the Bot in a group, not in a direct chat");
        assert.equal(live.cats, 1, "one avatar on the live line");
        await page.locator(".task-run-live-line").scrollIntoViewIfNeeded(); await page.waitForTimeout(200);
        await page.screenshot({ path: join(heroDir, `after-live-${kind}-1180x820-light.png`) });
        // The run ends without a message or card: the line and its name label disappear and nothing is left behind.
        await page.evaluate(({ conv, botId }) => window.__emit("run", { conversation_id: conv, run: { id: "run_live", conversation_id: conv, bot_id: botId, status: "done", updated_at: new Date().toISOString() } }), { conv, botId: kind === "dm" ? "bot_a" : "bot_b" });
        await page.waitForFunction(() => !document.querySelector(".task-run-live-line"));
        const gone = await page.evaluate(() => ({ blocks: document.querySelectorAll('[data-run-id="run_live"]').length, heads: document.querySelectorAll(".message-list .sender-head").length, live: document.querySelectorAll(".live-run-status, .web-tool-run").length }));
        assert.deepEqual(gone, { blocks: 0, heads: 0, live: 0 }, "a silent finished run renders nothing at all");
        assert.deepEqual(errors, [], "page errors");
      } finally { await context.close(); }
    });
  }
  // A waiting text question in a group: the card reads at chat size and the live line is one row (one cat, the Bot's name).
  for (const width of [390, 1180]) {
    await scenario(`pending question in a group (${width}px)`, async () => {
      const at = iso(1000);
      const world = {
        ...emptyWorld(),
        messages: [{ id: "u5", role: "user", content: "Save a key on the computer for a script later.", created_at: at }],
        runs: [{ id: "run_ask", bot_id: "bot_b", conversation_id: "grp_1", status: "waiting", trigger_message_id: "u5", created_at: new Date(Date.now() - 50_000).toISOString(), updated_at: new Date().toISOString() }],
        questions: [{ question_id: "q_text", conversation_id: "grp_1", bot_id: "bot_b", run_id: "run_ask", type: "question", question_type: "text", status: "pending", created_at: at, updated_at: at, question: "Sure. Two things first: which service is the key for, and what environment variable name should the script use, for example SYNTHETIC_API_KEY? Do not paste the key in the group; once the name is settled I will open a private input for you. Note the computer is shared, so other Bots with shell access can read it." }],
      };
      const { page, context, errors } = await open("/g/grp_1", { width, height: 820, theme: "light", world, fakeEvents: true });
      try {
        await page.waitForSelector(".question-card"); await page.waitForSelector(".task-run-live-line");
        const seen = await page.evaluate(() => {
          const prompt = getComputedStyle(document.querySelector(".question-prompt"));
          const line = document.querySelector(".task-run-live-line");
          return { size: parseFloat(prompt.fontSize), weight: Number(prompt.fontWeight), cats: line.querySelectorAll(".task-activity-cat").length, name: line.querySelector(".task-activity-name")?.textContent ?? "", heads: document.querySelectorAll(".message-list .task-run-block .sender-head").length };
        });
        assert.ok(seen.size <= 15 && seen.weight < 600, `question text reads at chat size (${seen.size}px/${seen.weight})`);
        assert.deepEqual([seen.cats, seen.name, seen.heads], [1, "Simile Cat", 0], "one cat and the Bot's name on the live line, no extra caption");
        await page.locator(".task-run-live-line").scrollIntoViewIfNeeded(); await page.waitForTimeout(200);
        await page.screenshot({ path: join(heroDir, `after-question-group-${width}-light.png`) });
        assert.deepEqual(errors, [], "page errors");
      } finally { await context.close(); }
    });
  }
  // The stream ends (final message replaces the draft) while text is still being revealed: the rest keeps
  // revealing over a few frames instead of appearing at once.
  await scenario("stream end keeps revealing (dm)", async () => {
    const at = new Date(Date.now() - 5_000).toISOString();
    const world = { ...emptyWorld(), messages: [{ id: "u9", role: "user", content: "Tell me about the ledger.", created_at: at, run_id: "run_stream" }],
      runs: [{ id: "run_stream", bot_id: "bot_a", conversation_id: "dm_a", status: "running", trigger_message_id: "u9", created_at: at, updated_at: at }] };
    const { page, context, errors } = await open("/b/bot_a", { width: 1180, height: 820, theme: "light", world, fakeEvents: true, motion: "no-preference" });
    try {
      await page.waitForSelector(".message-list");
      const full = Array.from({ length: 12 }, (_, i) => `Synthetic ledger line ${i + 1} reads the week's entries and totals.`).join(" ");
      const samples = await page.evaluate(async ({ full }) => {
        const id = "m_stream", base = { conversation_id: "dm_a", run_id: "run_stream", bot_id: "bot_a", message_id: id, seq: 50, created_at: new Date().toISOString() };
        window.__emit("delta", { ...base, text: full, content: full, revision: 1 });
        const length = () => { const el = document.querySelector(`[data-message-id="${id}"] .message-content`); return el ? el.textContent.replace(/\s+/g, " ").trim().length : -1; };
        const out = [];
        const frame = () => new Promise(r => requestAnimationFrame(() => r()));
        for (let i = 0; i < 10; i++) { await frame(); out.push({ t: "stream", n: length() }); }
        window.__emit("message", { id, conversation_id: "dm_a", seq: 50, role: "assistant", sender_bot_id: "bot_a", run_id: "run_stream", content: full, created_at: base.created_at });
        window.__emit("run", { conversation_id: "dm_a", run: { id: "run_stream", conversation_id: "dm_a", bot_id: "bot_a", status: "done", updated_at: new Date().toISOString() } });
        for (let i = 0; i < 90; i++) { await frame(); out.push({ t: "final", n: length() }); }
        return out;
      }, { full });
      const total = full.replace(/\s+/g, " ").trim().length;
      const lastStream = samples.filter(s => s.t === "stream").at(-1);
      assert.ok(lastStream && lastStream.n > 0 && lastStream.n < total, `text was not mid-reveal when the stream ended (${lastStream?.n}/${total}); the case is not exercised`);
      let maxJump = 0;
      for (let i = 1; i < samples.length; i++) if (samples[i].t === "final" && samples[i - 1].n >= 0) maxJump = Math.max(maxJump, samples[i].n - samples[i - 1].n);
      assert.ok(maxJump <= total * 0.2, `text jumped ${maxJump}/${total} characters in one frame after the stream ended`);
      assert.equal(samples[samples.length - 1].n, total, "the full answer is shown in the end");
      assert.deepEqual(errors, [], "page errors");
    } finally { await context.close(); }
  });
  // A delegation in a group is one handoff line; its brief opens on request and is not a chat bubble.
  await scenario("group delegation is a compact handoff line", async () => {
    const at = iso(900);
    const world = { ...emptyWorld(), messages: [
      { id: "u7", role: "user", content: "@Simile Cat @Ledger Cat debate breakfast", created_at: at },
      { id: "h1", role: "assistant", kind: "notice", sender_bot_id: "bot_b", run_id: "run_child", content: "@Ledger Cat take the other side. Requirements: plain language, short, one everyday comparison.", created_at: iso(905), notice: { type: "handoff", from_bot_id: "bot_b", to_bot_id: "bot_a", target_conversation_id: "grp_1", target_run_id: "run_child" } },
    ] };
    const { page, context, errors } = await open("/g/grp_1", { width: 1180, height: 820, theme: "light", world });
    try {
      await page.waitForSelector(".message-assignment");
      const seen = await page.evaluate(() => {
        const line = document.querySelector(".message-assignment");
        const brief = line.querySelector(".assignment-brief");
        return { text: line.querySelector(":scope > div > span").textContent, briefOpen: brief?.open ?? null, bubbles: document.querySelectorAll('[data-message-id="h1"] .message-content').length, briefVisible: brief ? getComputedStyle(brief.querySelector(":scope > *:not(summary)") ?? brief).display !== "none" && brief.open : false };
      });
      assert.match(seen.text, /Simile Cat.*Ledger Cat|Ledger Cat.*Simile Cat/, `handoff line names both Bots: ${seen.text}`);
      assert.equal(seen.bubbles, 0, "the brief is not rendered as a chat bubble");
      assert.equal(seen.briefOpen, false, "the brief starts collapsed");
      await page.locator(".message-assignment summary").click();
      assert.match(await page.locator(".message-assignment .assignment-brief").innerText(), /Requirements: plain language/, "the brief opens on request");
      assert.deepEqual(errors, [], "page errors");
    } finally { await context.close(); }
  });
  await scenario("cards stay when Activity is hidden", async () => {
    const at = iso(1000);
    const approval = { question_id: "q_pending", conversation_id: "dm_a", bot_id: "bot_a", run_id: "run_wait", type: "question", question_type: "approval", question: "Send the synthetic summary?", status: "pending", created_at: at, updated_at: at, approval: { action: "Send summary", target: "Synthetic target", impact: "Posts one message", review: { source: "auto-review", status: "human_required", model: "synthetic-review", policy_version: "v6" } } };
    const world = {
      ...emptyWorld(),
      messages: [{ id: "u4", role: "user", content: "Do two things.", created_at: at }],
      runs: [
        { id: "run_wait", bot_id: "bot_a", conversation_id: "dm_a", status: "waiting", trigger_message_id: "u4", created_at: at, updated_at: at },
        { id: "run_fail", bot_id: "bot_a", conversation_id: "dm_a", status: "failed", trigger_message_id: "u4", error: "boom", failure: { code: "execution_failed", source: "runtime", message: "boom" }, created_at: iso(1010), updated_at: iso(1030) },
      ],
      tools: [{ bot_id: "bot_a", run_id: "run_fail", call_id: "fail_1", name: "synthetic_tool", arguments: "{}", result: "oops", status: "completed", truncated: false, started_at: iso(1012), updated_at: iso(1015) }],
      summaries: [{ run_id: "run_fail", bot_id: "bot_a", tool_count: 1, completed_count: 1, failed_count: 0, interrupted_count: 0, pending_count: 0, started_at: iso(1012), updated_at: iso(1015) }],
      questions: [approval],
    };
    const { page, context, errors } = await open("/b/bot_a", { width: 1180, height: 820, theme: "light", world });
    try {
      await page.waitForSelector(".approval-card"); await page.waitForSelector(".task-issue-card");
      const seen = await page.evaluate(() => ({ approval: document.querySelectorAll(".approval-card").length, issue: document.querySelectorAll(".task-issue-card").length, rows: document.querySelectorAll(".message-list .task-activity, .message-list .web-tool-run").length }));
      assert.equal(seen.approval, 1, "the pending approval card is visible");
      assert.ok(seen.issue >= 1, "the failed run's issue card is visible");
      assert.equal(seen.rows, 0, "no Activity row beside the cards");
      // The issue card's own "View activity" still opens the details although Activity is hidden.
      await page.locator('.task-issue-card[data-issue-kind] button:has-text("查看执行记录")').first().click();
      await page.waitForSelector(".task-activity[open]");
      assert.ok(await page.locator(".task-activity[open] .task-step").count() >= 1, "the failed run's steps are listed");
      await page.screenshot({ path: join(heroDir, "after-cards-dm-1180x820-light.png") });
      assert.deepEqual(errors, [], "page errors");
    } finally { await context.close(); }
  });
  for (const [kind, url] of [["group", "/g/grp_1"], ["dm", "/b/bot_a"]]) {
    await scenario(`tap "View activity" on a message (${kind}, touch)`, async () => {
      const { page, context, errors } = await open(url, { width: 1180, height: 820, theme: "light", touch: true, mobile: true });
      try {
        assert.ok(await page.evaluate(() => matchMedia("(hover: none)").matches), "the context has no hover, like an iPad");
        const host = page.locator(`.message-block[data-message-id="a5"] .message-hover-host`);
        assert.equal(await host.locator(".message-hover-actions").isVisible(), false, "actions are hidden until a tap");
        await page.locator(`.message-block[data-message-id="a5"] .message-content`).first().tap();
        await page.waitForSelector(`.message-block[data-message-id="a5"] .message-hover-actions`, { state: "visible" });
        const action = host.locator('button[aria-label="查看工作过程"]');
        assert.equal(await action.count(), 1, "the actions offer View activity");
        // A message whose run did no work offers none.
        assert.equal(await page.locator('.message-block[data-message-id="u1"] button[aria-label="查看工作过程"]').count(), 0, "a person's message has no activity action");
        await action.tap();
        await page.waitForSelector('.task-run-block[data-run-id="run_c"] .task-activity[open]');
        assert.ok(await page.locator('.task-run-block[data-run-id="run_c"] .task-step').count() >= 7, "the run's seven steps load inside the opened details");
        assert.equal(await page.locator('.task-run-block[data-run-id="run_a"]').count(), 0, "only the asked run opens");
        await page.screenshot({ path: join(heroDir, `after-view-activity-${kind}-1180x820-light.png`) });
        // Tapping again on the same message offers Hide activity.
        await page.locator(`.message-block[data-message-id="a5"] .message-content`).first().tap();
        await page.waitForSelector(`.message-block[data-message-id="a5"] .message-hover-actions`, { state: "visible" });
        await host.locator('button[aria-label="收起工作过程"]').tap();
        await page.waitForFunction(() => !document.querySelector('.task-run-block[data-run-id="run_c"]'));
        assert.deepEqual(errors, [], "page errors");
      } finally { await context.close(); }
    });
  }
  await scenario("toggle on restores Activity rows and drops the action", async () => {
    const { page, context } = await open("/g/grp_1", { width: 1180, height: 820, theme: "light", world: { ...emptyWorld(), showActivity: true } });
    try {
      const n = await page.evaluate(() => ({ rows: document.querySelectorAll(".message-list .task-activity").length, actions: document.querySelectorAll('[aria-label="查看工作过程"]').length, text: document.querySelector(".message-list").textContent.includes("工作过程") }));
      assert.ok(n.rows >= 3 && n.text, "Activity rows are back");
      assert.equal(n.actions, 0, "no View activity action when the rows are already shown");
    } finally { await context.close(); }
  });
  // Focus rule: a click or tap on a message (tap opens its actions) never frames it; Tab rings what it reaches.
  for (const [width, height, touch] of [[1440, 900, false], [1180, 820, true], [1024, 768, true], [390, 844, true]]) for (const theme of ["light", "dark"]) {
    await scenario(`pointer focus draws no ring, Tab does (${width}px, ${theme})`, async () => {
      const { page, context } = await open("/b/bot_a", { width, height, theme, touch, mobile: width < 700 });
      try {
        const block = page.locator(".message-block[data-message-id=a5] .message-content").first();
        await block.scrollIntoViewIfNeeded();
        if (touch) await block.tap(); else await block.click();
        await page.waitForTimeout(160);
        const probe = await page.evaluate(focusRingProbe);
        assert.ok(probe.none || probe.field || !probe.ring, `message tap/click rings ${probe.focused}: ${probe.ring?.ring}`);
        const framed = await page.evaluate(() => [...document.querySelectorAll(".message-block .message-content")].filter(el => { const s = getComputedStyle(el); return s.outlineStyle !== "none" && parseFloat(s.outlineWidth) > 0 && !/rgba\(\d+, \d+, \d+, 0\)/.test(s.outlineColor); }).length);
        assert.equal(framed, 0, "no message frame after a tap or click");
        const found = await auditFocus(page, { touch, scope: "chat" });
        assert.deepEqual(found, [], JSON.stringify(found));
      } finally { await context.close(); }
    });
  }
  // iPad screenshots of the shipped default, group and direct chat, both themes.
  for (const theme of ["light", "dark"]) for (const [kind, url] of [["group", "/g/grp_1"], ["dm", "/b/bot_a"]]) {
    const { page, context } = await open(url, { width: 1180, height: 820, theme });
    try { await page.screenshot({ path: join(heroDir, `after-default-${kind}-1180x820-${theme}.png`) }); } finally { await context.close(); }
  }
} finally { await browser.close(); await server.close(); }
if (failed) { console.error(`${failed} chat bubble layout case(s) failed`); process.exit(1); }
console.log(`PASS: ${results.length} chat bubble layout cases`);
