// Synthetic workspace for the widget acceptance script and screenshots (docs/design/widgets-and-sidebar.md): a direct chat with Mochi, a
// group, a computer whose owner and artifacts the test changes, and a conversation event stream the test can push messages into.
import { installStubs } from "./computer-window-fixture.mjs";

export const NOW = "2026-10-26T12:00:00Z";
export const BOT = { id: "bot-synthetic", name: "Mochi", instructions: "Synthetic bot", model: "default", dm_conversation_id: "conv-synthetic", created_at: NOW };
export const BOT2 = { id: "bot-two", name: "Atlas", instructions: "Second synthetic bot", model: "default", dm_conversation_id: "conv-two", created_at: NOW };
export const DM = { id: "conv-synthetic", kind: "dm", name: "Mochi", bot_id: BOT.id, bot_ids: [BOT.id], updated_at: NOW };
export const GROUP = { id: "conv-group", kind: "group", name: "Launch crew", bot_ids: [BOT.id, BOT2.id], updated_at: "2026-10-26T11:00:00Z" };
export const DM2 = { id: "conv-two", kind: "dm", name: "Atlas", bot_id: BOT2.id, bot_ids: [BOT2.id], updated_at: "2026-10-26T10:00:00Z" };

const FILES = [["quarterly-report.pdf", "application/pdf"], ["chart.png", "image/svg+xml"], ["notes.md", "text/markdown"], ["data.csv", "text/csv"], ["summary.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"], ["slides.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"]];
/** The n-th file a Bot made in a conversation, as GET /api/artifacts reports it. */
export const artifact = (n, conversation = DM, bot = BOT) => ({ id: `att-${conversation.id}-${n}`, name: FILES[(n - 1) % FILES.length][0], mime: FILES[(n - 1) % FILES.length][1], size: 24000 + n * 1000, created_at: `2026-10-26T1${Math.min(n, 9)}:00:00Z`, conversation_id: conversation.id, conversation_name: conversation.name, conversation_kind: conversation.kind, conversation_bot_id: conversation.bot_id, conversation_archived: false, bot_id: bot.id, message_id: `msg-${conversation.id}-file-${n}`, message_seq: 100 + n, uploaded: false, version_group: `vg-${n}`, version_count: n === 3 ? 3 : 1 });
/** The assistant message that carries the n-th file. */
export const fileMessage = (n, conversation = DM, bot = BOT) => { const a = artifact(n, conversation, bot); return { id: a.message_id, conversation_id: conversation.id, seq: 100 + n, role: "assistant", content: `Saved ${a.name}.`, created_at: `2026-10-26T12:0${Math.min(n, 9)}:00Z`, sender_bot_id: bot.id, attachments: [{ id: a.id, conversation_id: conversation.id, message_id: a.message_id, name: a.name, mime: a.mime, size: a.size, created_at: a.created_at }] }; };
const text = (n, role, content, conversation = DM) => ({ id: `msg-${conversation.id}-${n}`, conversation_id: conversation.id, seq: n, role, content, created_at: `2026-10-26T11:0${n}:00Z`, sender_bot_id: role === "assistant" ? BOT.id : undefined });
export const talk = (conversation = DM) => [text(1, "user", "Can you pull the quarterly numbers together?", conversation), text(2, "assistant", "On it. I will put the results in files you can open.", conversation)];
export const botOwner = (run = "run-1", conversation = DM) => ({ owner: { kind: "bot", bot_id: BOT.id, run_id: run, conversation_id: conversation.id }, waiting: [] });

/** GET /api/conversations/{id}/usage as the Usage widget reads it (internal/app/conversation_usage.go). */
export const usageFor = (conversationId, { split = true, snapshot = true, total = 61_400 } = {}) => ({
  conversation_id: conversationId,
  totals: { runs: 14, runs_with_call_detail: split ? 14 : 3, requests: 41, input_tokens: 1_284_000, output_tokens: 52_300, equivalent_usd: 1.86, unpriced_requests: 0 },
  per_model: [{ model: "synthetic-model", requests: 41, input_tokens: 1_284_000, output_tokens: 52_300, equivalent_usd: 1.86, unpriced_requests: 0 }],
  latest_context: {
    run_id: "run-usage", model: "synthetic-model", used_tokens: total, used_measured: true, window_tokens: 200_000, window_known: true,
    parts: snapshot ? [["instructions", 6800], ["history", 31200], ["tool_definitions", 9400], ["tool_results", 8900], ["memory_notes", 3100], ["attachments", 1100], ["other", 900]].map(([kind, tokens]) => ({ kind, tokens, estimated: true })) : [],
    parts_tokens: snapshot ? total : 0, ...(snapshot ? {} : { parts_code: "context_snapshot_unavailable" }),
  },
  tool_split: split ? { available: true, tool_tokens: 742_000, conversation_tokens: 468_000, review_tokens: 21_000, tool_model_calls: 24, conversation_model_calls: 15, tool_calls: 52, basis: "calls" } : { available: false, code: "call_detail_incomplete" },
});

/**
 * `state` is read live by the stubs: { artifacts: {[conversationId]: item[]}, messages: {[conversationId]: message[]}, ownership, settings: {widget_artifacts_auto, widget_computer_auto},
 * events: {[conversationId]: [{id, type, data}]}, info }. `push(conversationId, type, data)` queues a conversation stream event.
 */
export function newState() {
  const state = { info: { kind: "firecracker", state: "ready" }, capture: "image", ownership: { owner: null, waiting: [] }, artifacts: {}, messages: {}, events: {}, settings: {}, nextEvent: 1, requests: [] };
  state.push = (conversationId, type, data) => { (state.events[conversationId] ??= []).push({ id: state.nextEvent++, type, data }); };
  /** A Bot's file arrives: the message is stored, listed under artifacts, and announced on the stream. */
  state.addFile = (n, conversation = DM, bot = BOT) => {
    const message = fileMessage(n, conversation, bot);
    (state.messages[conversation.id] ??= talk(conversation)).push(message);
    state.artifacts[conversation.id] = [artifact(n, conversation, bot), ...(state.artifacts[conversation.id] ?? [])];
    state.push(conversation.id, "message", message);
    return message;
  };
  return state;
}

export async function openWorkspace(browser, origin, state, { width = 1440, height = 900, theme = "light", group = false, reducedMotion = false, initScript } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, colorScheme: theme, reducedMotion: reducedMotion ? "reduce" : "no-preference" });
  if (initScript) await context.addInitScript(initScript);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  state.conversation = DM;
  await installStubs(page, state);
  await page.route("**/api/**", async route => {
    const url = new URL(route.request().url()); const p = url.pathname;
    const json = body => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    if (p === "/api/bots") return json({ bots: [BOT, BOT2] });
    if (p === "/api/conversations") return json({ conversations: [DM, GROUP, DM2] });
    if (p === "/api/computers/firecracker/actions" && String(route.request().postData() ?? "").includes("desktop.control.acquire")) return json({ ok: true, result: { control_id: "ctl-synthetic" } });
    if (p === "/api/automation-settings") return json({ items: Object.entries({ widget_artifacts_auto: true, widget_computer_auto: true, ...state.settings }).map(([key, value]) => ({ key, group: "widgets", kind: "bool", value, default: true, source: "default" })) });
    if (p === "/api/artifacts") {
      const scope = url.searchParams.get("scope"), id = url.searchParams.get("id");
      const all = Object.values(state.artifacts).flat();
      const items = scope === "conversation" ? all.filter(item => item.conversation_id === id) : scope === "bot" ? all.filter(item => item.bot_id === id) : all;
      return json({ items: items.sort((a, b) => b.message_seq - a.message_seq).slice(0, Number(url.searchParams.get("limit") || 50)), next_cursor: "" });
    }
    const usage = p.match(/^\/api\/conversations\/([^/]+)\/usage$/);
    if (usage) { state.usageReads = (state.usageReads ?? 0) + 1; if (state.usage === "404") return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "not found" } }) });
      return state.usage === "fail" ? route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { code: "internal", message: "synthetic" } }) }) : json(state.usage ?? usageFor(usage[1])); }
    const conversation = p.match(/^\/api\/conversations\/([^/]+)\/(messages|events)$/);
    if (conversation && conversation[2] === "messages") return json({ messages: state.messages[conversation[1]] ?? talk(conversation[1] === GROUP.id ? GROUP : conversation[1] === DM2.id ? DM2 : DM), has_more: false, event_cursor: 0 });
    if (conversation && conversation[2] === "events") {
      // A stream that ends after what is queued and asks the browser to come back soon: new pushes arrive on the next visit.
      const after = Math.max(Number(url.searchParams.get("after") || 0), Number(route.request().headers()["last-event-id"] || 0));
      const frames = (state.events[conversation[1]] ?? []).filter(event => event.id > after).map(event => `id: ${event.id}\nevent: ${event.type}\ndata: ${JSON.stringify(event.data)}\n\n`).join("");
      return route.fulfill({ status: 200, contentType: "text/event-stream", body: `retry: 150\n: synthetic\n\n${frames}` });
    }
    if (p.endsWith("/work-items") || p === "/api/work-items") return json({ work_items: [] });
    return route.fallback();
  });
  await page.goto(origin + "/");
  await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
  if (width < 700) await page.locator(".conversation-row").first().click();
  await page.locator(".chat-header").waitFor();
  if (group) { await page.locator(".conversation-row", { hasText: GROUP.name }).click(); await page.waitForTimeout(400); }
  await page.waitForTimeout(700);
  return { context, page, errors };
}
