// Synthetic data for the component catalog. Every name, message and id here is made up; nothing comes from a real
// account. `workspace()` is the whole server as the real App sees it; the isolated sections reuse the same records.
import { json, type Synthetic } from "./sandbox";
import type { Bot, Conversation, Message, Run, StreamDraft } from "../types";
import type { Question } from "../questionTimeline";
import type { MailDraft } from "../MailDraftCard";

// Times are relative to now, so "10 min ago" and the live run timers read the way they do in a real chat.
const T0 = Math.floor(Date.now() / 60_000) * 60_000 - 45 * 60_000;
export const at = (minutes: number) => new Date(T0 + minutes * 60_000).toISOString();

export const BOTS: Bot[] = [
  { id: "bot_mochi", name: "Mochi", instructions: "Synthetic", model: "default", dm_conversation_id: "dm_mochi", created_at: at(-9000), is_primary: true, effective_model: "synthetic-model", effective_reasoning_effort: "medium" },
  { id: "bot_miso", name: "Miso", instructions: "Synthetic", model: "default", dm_conversation_id: "dm_miso", created_at: at(-8000) },
  { id: "bot_nori", name: "Nori", instructions: "Synthetic", model: "default", dm_conversation_id: "dm_nori", created_at: at(-7000) },
  { id: "bot_ledger", name: "Ledger", instructions: "Synthetic", model: "default", dm_conversation_id: "dm_ledger", created_at: at(-6000), archived: true },
];
export const bot = (id: string) => BOTS.find(item => item.id === id)!;

const last = (id: string, seq: number, content: string, minutes: number, role: Message["role"] = "assistant", sender?: string): Conversation["last_message"] =>
  ({ id, seq, role, content, created_at: at(minutes), ...(sender ? { sender_bot_id: sender } : {}) });

export const CONVERSATIONS: Conversation[] = [
  { id: "dm_mochi", kind: "dm", name: "Mochi", bot_id: "bot_mochi", bot_ids: ["bot_mochi"], updated_at: at(40), read_seq: 99, pinned: true, pin_order: 1, user_visible: true, last_message: last("m_mochi_9", 9, "The launch numbers are in the table above.", 40, "assistant", "bot_mochi") },
  { id: "grp_launch", kind: "group", name: "Launch crew", bot_ids: ["bot_mochi", "bot_miso", "bot_nori"], updated_at: at(35), read_seq: 99, working_bot_ids: ["bot_miso"], user_visible: true, last_message: last("m_grp_6", 6, "I'll draft the release note next.", 35, "assistant", "bot_miso") },
  { id: "dm_miso", kind: "dm", name: "Miso", bot_id: "bot_miso", bot_ids: ["bot_miso"], updated_at: at(30), read_seq: 1, unread_count: 3, user_visible: true, last_message: last("m_miso_4", 4, "Three copy fixes are ready for review.", 30, "assistant", "bot_miso") },
  { id: "dm_nori", kind: "dm", name: "Nori", bot_id: "bot_nori", bot_ids: ["bot_nori"], updated_at: at(20), read_seq: 99, user_visible: true, task_state: { status: "needs_attention", question_id: "q_nori" }, last_message: last("m_nori_2", 2, "Should I book the venue for Thursday?", 20, "assistant", "bot_nori") },
  { id: "dm_ledger", kind: "dm", name: "Ledger", bot_id: "bot_ledger", bot_ids: ["bot_ledger"], updated_at: at(-500), read_seq: 99, archived: true, user_visible: true },
];
const INDICATOR_BOTS: Bot[] = [
  { id: "bot_juno", name: "Juno", instructions: "Synthetic", model: "default", dm_conversation_id: "dm_juno", created_at: at(-5000) },
  { id: "bot_pip", name: "Pip", instructions: "Synthetic", model: "default", dm_conversation_id: "dm_pip", created_at: at(-4000) },
  { id: "bot_kit", name: "Kit", instructions: "Synthetic", model: "default", dm_conversation_id: "dm_kit", created_at: at(-3000) },
];
/** The sample list with one row per indicator case: waiting beats unread (Miso), failed beats unread (Nori), unread alone (Juno), working alone (Pip), a finished task and nothing (Kit). */
function indicatorRows(): Conversation[] {
  const row = (id: string, name: string, bot: string, text: string, minutes: number, extra: Partial<Conversation>): Conversation =>
    ({ id, kind: "dm", name, bot_id: bot, bot_ids: [bot], updated_at: at(minutes), read_seq: 99, user_visible: true, last_message: last(`m_${id}`, 2, text, minutes, "assistant", bot), ...extra });
  return [
    ...CONVERSATIONS.filter(item => !item.archived).map(item => item.id === "dm_miso" ? { ...item, task_state: { status: "needs_attention" as const, question_id: "q_miso" } }
      : item.id === "dm_nori" ? { ...item, task_state: { status: "failed" as const }, unread_count: 1 } : item),
    row("dm_juno", "Juno", "bot_juno", "The invoice batch is sorted.", 15, { unread_count: 2, read_seq: 1 }),
    row("dm_pip", "Pip", "bot_pip", "Reconciling the March statements.", 10, { task_state: { status: "executing" as const } }),
    row("dm_kit", "Kit", "bot_kit", "Receipts filed. Nothing else to do.", 5, { task_state: { status: "completed" as const } }),
  ];
}
export const conversation = (id: string) => CONVERSATIONS.find(item => item.id === id)!;

const TABLE = "| Channel | Sign-ups | Change |\n|---|---:|---:|\n| Newsletter | 1,240 | +12% |\n| Waitlist | 860 | +4% |\n| Referrals | 312 | −2% |";
export const ANSWER = `Here is this week's summary for the launch review.\n\n- Sign-ups are **up 8%** on last week.\n- The waitlist import finished on Tuesday.\n- One open question: the venue for Thursday.\n\n${TABLE}\n\nThe export ran with \`ledger --week 42\`:\n\n\`\`\`sh\nledger export --week 42 --format csv\n\`\`\``;

function user(conversationId: string, seq: number, content: string, minutes: number, extra: Partial<Message> = {}): Message {
  return { id: `m_${conversationId}_${seq}`, conversation_id: conversationId, seq, role: "user", content, created_at: at(minutes), client_message_id: `c_${conversationId}_${seq}`, ...extra };
}
function reply(conversationId: string, seq: number, botId: string, content: string, minutes: number, extra: Partial<Message> = {}): Message {
  return { id: `m_${conversationId}_${seq}`, conversation_id: conversationId, seq, role: "assistant", content, created_at: at(minutes), sender_bot_id: botId, sender_bot_name: bot(botId).name, run_id: `run_${conversationId}_${seq}`, ...extra };
}
function run(conversationId: string, botId: string, id: string, trigger: string, status: Run["status"], minutes: number, extra: Partial<Run> = {}): Run {
  return { id, conversation_id: conversationId, bot_id: botId, status, trigger_message_id: trigger, origin_conversation_id: conversationId, queue_seq: 1, model: "synthetic-model", created_at: at(minutes), updated_at: at(minutes + 1), ...extra };
}

const FILLER = "A short synthetic note so the conversation is long enough to scroll.";
function dmMessages(options: AppOptions): Message[] {
  const c = "dm_mochi";
  const list: Message[] = [];
  if (options.longHistory) for (let seq = 1; seq <= 24; seq += 1) list.push(seq % 2 ? user(c, seq, `${FILLER} (${seq})`, seq - 200) : reply(c, seq, "bot_mochi", `${FILLER} (${seq})`, seq - 200));
  const base = list.length;
  list.push(
    user(c, base + 1, "Can you pull together this week's numbers for the launch review?", 0),
    reply(c, base + 2, "bot_mochi", ANSWER, 2, { reactions: [{ emoji: "👍", actor_type: "user", created_at: at(3) }] }),
    { id: `m_${c}_${base + 3}`, conversation_id: c, seq: base + 3, role: "assistant", kind: "message_ref", content: "Message from Miso", created_at: at(5), sender_bot_id: "bot_miso",
      notice: { type: "message_ref", from_bot_id: "bot_miso", to_bot_id: "bot_mochi", target_conversation_id: "dm_miso", target_run_id: "run_dm_miso_4" } },
    { id: `m_${c}_resume`, conversation_id: c, seq: base + 3.5, role: "assistant", kind: "notice", content: "", created_at: at(6), sender_bot_id: "system",
      notice: { type: "resume", reason: "restart", from_bot_id: "bot_mochi", to_bot_id: "bot_mochi", target_conversation_id: c, target_run_id: "run_resume" } },
    user(c, base + 4, "Thanks. Ask Miso to check the copy too.", 30),
    reply(c, base + 5, "bot_mochi", "Done. Miso has the copy and will reply in your chat with her.", 32),
    user(c, base + 6, "Great, last thing: what time is the review?", 40),
  );
  if (options.runs) list.push(
    user(c, base + 7, "Also, can you book the small room?", 60), user(c, base + 8, "For 15:00 on Thursday.", 60.4), user(c, base + 9, "Eight people, with a screen.", 60.8),
    reply(c, base + 10, "bot_mochi", "Checking the calendar.", 62), reply(c, base + 11, "bot_mochi", "The small room is free.", 62.3), reply(c, base + 12, "bot_mochi", "Booked it for 15:00 Thursday, eight seats, screen included.", 62.6),
  );
  return list.map((message, index) => ({ ...message, seq: index + 1 }));
}

function groupMessages(options: AppOptions): Message[] {
  const c = "grp_launch";
  const list: Message[] = [
    user(c, 1, "Morning all. Where are we on the launch checklist?", 0),
    reply(c, 2, "bot_mochi", "Numbers are done and attached to the review doc.", 2),
    reply(c, 3, "bot_mochi", "I also moved the review to 15:00.", 3),
    reply(c, 4, "bot_nori", "The venue is still open. I asked you about it in our chat.", 4),
    { ...reply(c, 4.5, "bot_mochi", "@Miso please draft the release note from the checklist. Keep it under 200 words.", 5), kind: "notice" as const,
      notice: { type: "handoff", from_bot_id: "bot_mochi", to_bot_id: "bot_miso", target_conversation_id: c, target_run_id: "run_grp_launch_handoff" } },
    user(c, 5, "@Miso can you take the release note?", 30),
    reply(c, 6, "bot_miso", "I'll draft the release note next.", 35),
    user(c, 7, "Thanks, everyone.", 40),
  ];
  // Bubble runs in a group: the person's three, then Mochi's three (avatar and name on the group's last bubble only).
  if (options.runs) list.push(
    user(c, 8, "One more thing for the review.", 60), user(c, 9, "Please add the venue.", 60.4), user(c, 10, "And the catering count.", 60.8),
    reply(c, 11, "bot_mochi", "Adding both now.", 62), reply(c, 12, "bot_mochi", "Venue is in.", 62.3), reply(c, 13, "bot_mochi", "Catering count is in too: twelve.", 62.6),
  );
  return list.map((message, index) => ({ ...message, seq: index + 1 }));
}

const TASKS = [
  { id: "t_note", conversation_id: "grp_launch", bot_id: "bot_miso", bot_name: "Miso", kind: "task", title: "Draft the release note", description: "", status: "in_progress", assigner_kind: "user", assignee_run_id: "run_grp_launch_6", claimed_at: at(31), created_at: at(30), updated_at: at(36),
    checklist: [{ id: "c1", text: "Read the launch checklist", status: "done" }, { id: "c2", text: "Write the first draft", status: "done" }, { id: "c3", text: "Cut it under 200 words", status: "open" }] },
  { id: "t_venue", conversation_id: "grp_launch", bot_id: "bot_nori", bot_name: "Nori", kind: "task", title: "Confirm the venue for Thursday", description: "", status: "todo", assigner_kind: "user", assignee_run_id: "run_grp_launch_4", created_at: at(1), updated_at: at(1) },
];

function runsFor(conversationId: string, messages: Message[], options: AppOptions): Run[] {
  if (conversationId === "grp_launch") return [
    run(conversationId, "bot_mochi", "run_grp_launch_2", "m_grp_launch_1", "done", 1),
    run(conversationId, "bot_nori", "run_grp_launch_4", "m_grp_launch_1", "done", 1),
    run(conversationId, "bot_miso", "run_grp_launch_6", "m_grp_launch_5", "done", 31),
    run(conversationId, "bot_mochi", "run_grp_launch_8", "m_grp_launch_7", "done", 41),
    run(conversationId, "bot_miso", "run_grp_launch_9", "m_grp_launch_7", "running", 41),
  ];
  if (conversationId !== "dm_mochi") return [];
  const lastUser = messages[messages.length - 1];
  if (options.runs === "tools") return [run(conversationId, "bot_mochi", "run_dm_mochi_11", "m_dm_mochi_11", "done", 62), run(conversationId, "bot_mochi", "run_live", lastUser.id, "done", 40)];
  return [run(conversationId, "bot_mochi", "run_live", lastUser.id, options.running ? "running" : options.queued ? "queued" : "done", 40)];
}

function liveDraft(): StreamDraft {
  return { run_id: "run_live", conversation_id: "dm_mochi", bot_id: "bot_mochi", message_id: "m_dm_mochi_live", content: "The review is at 15:00 in the small room. I'm checking whether", status: "active", revision: 3, created_at: at(41), updated_at: at(41) };
}

export function pendingApproval(conversationId = "dm_mochi", extra: Partial<Question> = {}): Question {
  return { question_id: `q_${conversationId}_approval`, conversation_id: conversationId, bot_id: "bot_mochi", run_id: "run_live", type: "question", question_type: "approval", question: "Send the weekly summary to the launch list?", status: "pending", created_at: at(41), updated_at: at(41),
    approval: { action: "Send an email to launch@example.test", target: "launch@example.test", impact: "Twelve people receive the summary", payload: "{\n  \"to\": \"launch@example.test\",\n  \"subject\": \"Week 42 summary\"\n}" }, ...extra };
}

export type AppOptions = {
  /** The page the App opens: "/" (list), "/b/<bot>" or "/g/<group>". */
  path: string;
  modelConfigured?: boolean;
  /** Mochi is answering: a running run, a streaming draft, the stop button. */
  running?: boolean;
  /** Mochi has not picked up the last message yet (a queued run): it reads "delivered", not "read". */
  queued?: boolean;
  /** How sending a message ends. */
  send?: "ok" | "fail" | "hang" | "queued" | "offline" | "flaky" | "upload";
  /** The first list load works, every refresh after it fails (the refresh toast). */
  refreshFails?: boolean;
  /** After the first list load no /api request answers, so nothing "gets through" to end the proxy-challenge notice. */
  challenge?: boolean;
  /** The first list load never answers (boot screen) or fails (boot failure card). */
  boot?: "hang" | "fail";
  /** No Bots and no conversations: the first-run screen. */
  empty?: boolean;
  /** Twenty-four extra messages above, so the conversation can scroll away from the bottom. */
  longHistory?: boolean;
  /** Every status case of a sidebar row at once (see rowIndicator.ts): working, unread, waiting over unread, failed over unread, nothing. */
  indicators?: boolean;
  /** A pending approval card in Mochi's chat. */
  approval?: boolean;
  /** "none": the server has no computer backend (no header button, no Computer widget, Settings explains the options). */
  computer?: "ready" | "starting" | "hibernated" | "resuming" | "desktop-starting" | "none";
  /** "none": no connections are set up (Bot settings > Tools shows the empty line and the link to Settings). */
  connections?: "none";
  /** Mochi holds the desktop in her own chat (the Computer widget opens by itself and the bezel carries her colour). */
  driver?: "bot";
  /** The group has two tasks: Miso's release note (claimed, 2/3) and Nori's venue (assigned). The cards and the member list show them. */
  tasks?: boolean;
  /** Two bubble runs of three at the end of Mochi's chat: the person's, then Mochi's (corners merge, 3px apart). */
  runs?: boolean | "tools";
  /** The server reports a newer release (the admin banner, with its Update button). "running": the host is downloading it; "failed" / "rolled-back": the host's last answer; "rc": a release candidate is on offer too. */
  update?: boolean | "running" | "failed" | "rolled-back" | "rc";
  /** What GET /api/conversations/{id}/usage answers for the Usage widget: the whole picture, no tool split (older runs), no context snapshot, or a 404 (a server release without the route). */
  usage?: "full" | "split-unavailable" | "no-snapshot" | "old-server";
};

const SCREEN = `data:image/svg+xml,${encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800"><rect width="1280" height="800" fill="#d9e6ee"/><rect width="1280" height="36" fill="#33495a"/><rect x="120" y="140" width="560" height="360" rx="14" fill="#fff"/><text x="160" y="200" font-size="34" font-family="sans-serif" fill="#33495a">Synthetic screen</text></svg>')}`;

/** GET /api/system/update (internal/app/update_request.go): the release check plus what the host reports about an update in flight. */
function updateAnswer(update: AppOptions["update"]) {
  const now = new Date().toISOString();
  const status = update === "running" ? { state: "downloading", version: "v0.1.5", from: "v0.1.4", channel: "stable", progress: 30, updated_at: now }
    : update === "failed" ? { state: "failed", version: "v0.1.5", from: "v0.1.4", channel: "stable", progress: null, code: "runs_still_active", updated_at: now, finished_at: now }
    : update === "rolled-back" ? { state: "rolled_back", version: "v0.1.5", from: "v0.1.4", channel: "stable", progress: null, code: "update_rejected", rolled_back_to: "v0.1.4", updated_at: now, finished_at: now }
    : null;
  return { current: "v0.1.4", latest: update ? "v0.1.5" : "v0.1.4", update_available: Boolean(update), notes_url: "", checked_at: at(0), auto_update: "off",
    host_update: Boolean(update), channel_rc_enabled: update === "rc", latest_rc: update === "rc" ? "v0.2.0-rc.1" : "", latest_rc_notes_url: "", request: null, status };
}

/** The server the real App talks to inside a catalog frame. */
export function workspace(options: AppOptions): Synthetic {
  let conversationReads = 0;
  let flakyTries = 0;
  const messages = new Map<string, Message[]>([["dm_mochi", dmMessages(options)], ["grp_launch", groupMessages(options)],
    ["dm_miso", [user("dm_miso", 1, "Can you check the launch copy?", 10), reply("dm_miso", 2, "bot_miso", "On it.", 11), reply("dm_miso", 3, "bot_miso", "Found three small fixes.", 25), reply("dm_miso", 4, "bot_miso", "Three copy fixes are ready for review.", 30)]],
    ["dm_nori", [user("dm_nori", 1, "Find a venue for the launch review.", 15), reply("dm_nori", 2, "bot_nori", "Should I book the venue for Thursday?", 20)]],
    ["dm_ledger", [user("dm_ledger", 1, "Archive last quarter's receipts.", -520), reply("dm_ledger", 2, "bot_ledger", "Archived.", -510)]]]);
  const bots = options.empty ? [] : options.indicators ? [...BOTS, ...INDICATOR_BOTS] : BOTS;
  const conversations = options.empty ? [] : options.indicators ? indicatorRows() : CONVERSATIONS;
  const questions = (id: string) => [
    ...(options.approval && id === "dm_mochi" ? [pendingApproval()] : []),
    ...(id === "dm_nori" ? [{ ...pendingApproval("dm_nori"), question_id: "q_nori", bot_id: "bot_nori", run_id: "run_nori", question_type: "yes_no" as const, question: "Should I book the venue for Thursday?", approval: undefined, created_at: at(20) }] : []),
  ];
  return (url, init) => {
    const p = url.pathname;
    const method = init?.method ?? "GET";
    if (p === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-00000000ca7a", auth: { mode: "none" } });
    if (p === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, language: localStorage.getItem("tofi.language") ?? "en", show_activity: options.runs === "tools" });
    if (options.challenge && conversationReads > 1) return new Promise<Response>(() => {});
    // A refresh after the first load fails across the board, as when the server goes away (one failing read alone is
    // cleared again by the next successful one).
    if (options.refreshFails && conversationReads > 1 && (p === "/api/config" || p === "/api/bots")) return json({ error: { code: "unavailable", message: "Synthetic: the server is busy" } }, 503);
    if (p === "/api/models") return json({ models: [{ id: "synthetic-model", name: "Synthetic model", provider: "anthropic", reasoning_efforts: ["low","medium","high"], default_reasoning: "medium" }], source: "live" });
    if (p === "/api/config") return json({ model_configured: options.modelConfigured !== false, default_model: "synthetic-model", provider: "synthetic", computer: options.computer === "none" ? { backend: "none", available: false, capabilities: [] } : { backend: "kvm", available: true, capabilities: ["shell", "files", "browser", "desktop", "desktop_stream", "terminal", "hibernate", "mcp_runner", "oauth_bridge", "secret_sink", "resources"] } });
    if (p === "/api/onboarding") return json({ step: 3, completed: true, skipped: false });
    if (p === "/api/primary-assistant") return json({ bot_id: "bot_mochi" });
    if (p === "/api/bots") return json({ bots: url.searchParams.get("include_archived") ? bots : bots.filter(item => !item.archived) });
    if (p === "/api/conversations" && method === "GET") {
      conversationReads += 1;
      if (options.boot === "hang") return new Promise<Response>(() => {});
      if (options.boot === "fail" || (options.refreshFails && conversationReads > 1)) return json({ error: { code: "unavailable", message: "Synthetic: the server is busy" } }, 503);
      return json({ conversations: url.searchParams.get("include_archived") ? conversations : conversations.filter(item => !item.archived) });
    }
    if (p === "/api/search" && method === "GET") {
      // The synthetic search: a case-insensitive substring over the fixtures' messages, newest first, like internal/app/search.go.
      const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
      const hits = !q ? [] : conversations.filter(item => !item.archived).flatMap(item => (messages.get(item.id) ?? []).flatMap(message => {
        const at = message.content.toLowerCase().indexOf(q);
        if (at < 0 || message.kind === "notice") return [];
        const start = Math.max(0, at - 40);
        return [{ id: message.id, conversation_id: item.id, conversation_name: item.name, conversation_kind: item.kind, seq: message.seq, role: message.role, created_at: message.created_at, snippet: (start ? "\u2026" : "") + message.content.slice(start, start + 140), match_start: at - start + (start ? 1 : 0), match_len: q.length }];
      })).sort((x, y) => y.created_at.localeCompare(x.created_at)).slice(0, Number(url.searchParams.get("limit") ?? 20));
      return json({ query: q, chats: [], messages: hits });
    }
    const conversationPath = /^\/api\/conversations\/([^/]+)\/([a-z-]+)$/.exec(p);
    if (conversationPath) {
      const [, id, what] = conversationPath;
      const list = messages.get(id) ?? [];
      if (what === "messages" && method === "POST") {
        if (options.send === "hang" || options.send === "upload") return new Promise<Response>(() => {});
        // The network is gone: the browser reports offline and the request itself fails. "flaky": the first try fails, every later one waits.
        if (options.send === "offline") { Object.defineProperty(navigator, "onLine", { get: () => false, configurable: true }); return Promise.reject(new TypeError("Failed to fetch")); }
        if (options.send === "flaky" && (flakyTries += 1) > 1) return new Promise<Response>(() => {});
        if (options.send === "flaky") return json({ error: { code: "unavailable", message: "Synthetic: restarting for an update" } }, 503);
        if (options.send === "fail") return json({ error: { code: "unavailable", message: "Synthetic: sending failed" } }, 503);
        const body = JSON.parse(String(init?.body ?? "{}")) as { content: string; client_message_id: string };
        const message = user(id, list.length + 1, body.content, 45, { client_message_id: body.client_message_id, id: `m_${id}_sent_${list.length + 1}` });
        list.push(message);
        // The addressed Bot is already working: no new run, the message waits for its next step.
        if (options.send === "queued") { message.delivery = "queued"; return json({ message, run: null, runs: [], queued: [{ bot_id: "bot_mochi", run_id: "run_live", inbox_id: `inbox_${list.length}` }] }); }
        return json({ message, run: run(id, "bot_mochi", `run_sent_${list.length}`, message.id, "queued", 45) });
      }
      // A file that never finishes uploading (its tile shows the bar at 40 %).
      if (what === "attachments" && method === "POST") return new Promise<Response>(() => {});
      if (what === "messages") return json({ messages: list, drafts: options.running && id === "dm_mochi" ? [liveDraft()] : [], has_more: false, event_cursor: 0 });
      if (what === "runs") return json({ runs: runsFor(id, list, options), has_more: false });
      if (what === "memories") return json({ memories: [] });
      if (what === "tools") return json({ activities: [], summaries: options.runs === "tools" && id === "dm_mochi" ? [{ run_id: "run_dm_mochi_11", bot_id: "bot_mochi", tool_count: 2, completed_count: 2, failed_count: 0, interrupted_count: 0, pending_count: 0, started_at: at(62), updated_at: at(62.2) }] : [], tool_count: 0, has_more: false });
      if (what === "schedules" || what === "schedule-occurrences" || what === "work-items" || what === "remembered-approvals") return json({ schedules: [], occurrences: [], work_items: [], grants: [] });
      if (what === "task-cards") return json({ tasks: options.tasks && id === "grp_launch" ? TASKS : [] });
      if (what === "read") return json({ read_seq: 99 });
      if (what === "usage") return options.usage === "old-server" ? json({ error: { code: "not_found", message: "not found" } }, 404) : json(usageFor(id, options.usage ?? "full"));
    }
    if (p === "/api/questions") return json({ questions: questions(url.searchParams.get("conversation_id") ?? "") });
    if (p === "/api/mail-drafts") return json({ drafts: [] });
    if (p === "/api/artifacts") {
      const scope = url.searchParams.get("scope"), id = url.searchParams.get("id");
      const items = ARTIFACTS.filter(item => scope === "bot" ? item.bot_id === id : scope === "conversation" ? item.conversation_id === id : true);
      return json({ items: items.slice(0, Number(url.searchParams.get("limit") || 50)), next_cursor: "" });
    }
    if (p.startsWith("/api/attachments/")) return new Response("# Week 42 summary\n\nSign-ups are up **8%** on last week.\n\n- Review at 15:00\n- Retention is flat\n", { status: 200, headers: { "Content-Type": "text/markdown" } });
    if (p === "/api/model-settings") return json({ model: "synthetic-model", reasoning_effort: "medium" });
    // Bot settings > Tools: the connections, this Bot's access to each, the web chains and its allowances.
    if (p === "/api/extensions/mcp" && method === "GET") return json({ servers: options.connections === "none" ? [] : CONNECTIONS });
    if (/^\/api\/bots\/[^/]+\/connections$/.test(p)) return json({ connections: options.connections === "none" ? [] : CONNECTIONS.map(item => ({ server: item.name, enabled: item.name !== "github" })) });
    if (p === "/api/web-tools") return json(WEB_TOOLS);
    if (p === "/api/standing-allowances") return json({ allowances: options.connections === "none" ? [] : [{ id: "al_1", bot_id: "bot_mochi", bot_name: "Mochi", server: "notion", tool: "create_page", state: "active", created_at: at(-4000), use_count: 5, last_used_at: at(-30) }], remembered: [] });
    const botRead = /^\/api\/bots\/[^/]+\/(skills|secrets|debug-preview)$/.exec(p);
    if (botRead) return json(botRead[1] === "skills" ? BOT_SKILLS : botRead[1] === "secrets" ? BOT_SECRETS : { system_prompt: "You are Mochi.\nYou keep the launch calendar and chase owners for updates.", tools: [] });
    if (p.endsWith("/task-impact")) return json({ given_tasks: 0, held_tasks: 0, open_waits: 0 });
    if (options.computer === "none" && (p === "/api/computers/firecracker/info" || p === "/api/computers/firecracker/actions" || p === "/api/computer/desktop-ownership")) return json({ error: { code: "not_configured", message: "computer VM is not configured" } }, 404);
    if (p === "/api/computers/firecracker/info") return json({ kind: "firecracker", state: options.computer === "starting" || options.computer === "hibernated" || options.computer === "resuming" ? options.computer : "ready", phase: options.computer === "starting" ? "booting" : undefined, desktop_starting: options.computer === "desktop-starting" || undefined });
    if (p === "/api/computers/firecracker/actions") return json({ ok: true, result: String(init?.body ?? "").includes("desktop.control.acquire") ? { control_id: "ctl_catalog" } : { image_url: SCREEN } });
    if (p === "/api/computer/desktop-ownership") return json({ owner: options.driver === "bot" ? { kind: "bot", bot_id: "bot_mochi", run_id: "run_driver", conversation_id: "dm_mochi" } : null, waiting: [] });
    if (p === "/api/system/update") return json(updateAnswer(options.update));
    return json({});
  };
}

/** GET /api/conversations/{id}/usage (internal/app/conversation_usage.go): synthetic figures for the Usage widget. */
function usageFor(id: string, mode: NonNullable<AppOptions["usage"]>) {
  const part = (kind: string, tokens: number) => ({ kind, tokens, estimated: true });
  return {
    conversation_id: id,
    totals: { runs: 14, runs_with_call_detail: mode === "split-unavailable" ? 3 : 14, requests: 41, input_tokens: 1_284_000, output_tokens: 52_300, equivalent_usd: 1.86, unpriced_requests: 0, cache_read_tokens: 802_000, cache_hit_rate: 0.62 },
    per_model: [
      { model: "gpt-6-luna", requests: 33, input_tokens: 1_090_000, output_tokens: 44_000, equivalent_usd: 1.52, unpriced_requests: 0 },
      { model: "gpt-6-luna-mini", requests: 8, input_tokens: 194_000, output_tokens: 8_300, equivalent_usd: 0.34, unpriced_requests: 0 },
    ],
    latest_context: {
      run_id: "run_dm_mochi_11", model: "gpt-6-luna", used_tokens: 61_400, used_measured: true, window_tokens: 200_000, window_known: true, cache_read_tokens: 52_200, cache_hit_rate: 0.85,
      parts: mode === "no-snapshot" ? [] : [part("instructions", 6_800), part("history", 31_200), part("tool_definitions", 9_400), part("tool_results", 8_900), part("memory_notes", 3_100), part("attachments", 1_100), part("other", 900)],
      parts_tokens: mode === "no-snapshot" ? 0 : 61_400,
      ...(mode === "no-snapshot" ? { parts_code: "context_snapshot_unavailable" } : {}),
    },
    tool_split: mode === "split-unavailable" ? { available: false, code: "call_detail_incomplete" }
      : { available: true, tool_tokens: 742_000, conversation_tokens: 468_000, review_tokens: 21_000, tool_model_calls: 24, conversation_model_calls: 15, tool_calls: 52, basis: "calls" },
  };
}

/** What Bot settings > Tools reads: a ready connection, one that needs sign-in and one that is paused (synthetic). */
const CONNECTIONS = [
  { name: "notion", url: "https://mcp.notion.com/mcp", transport: "streamable_http", oauth: { client_id: "", connected: true }, status: { checked_at: "2026-10-26T12:00:00Z", ok: true, tool_count: 12 } },
  { name: "github", url: "https://api.githubcopilot.com/mcp/", transport: "streamable_http", status: { checked_at: "2026-10-26T12:00:00Z", ok: true, tool_count: 9 } },
  { name: "linear", url: "https://mcp.linear.app/mcp", transport: "streamable_http", oauth: { client_id: "", connected: false } },
  { name: "acme_tracker", url: "https://mcp.example.test/tracker", transport: "streamable_http", enabled: false, status: { checked_at: "2026-10-26T12:00:00Z", ok: true, tool_count: 4 } },
];
const WEB_TOOLS = {
  enabled: true, computer: true, search_offered: true,
  search: [{ id: "native", label: "Claude native", kind: "native", paid: false, fixed: true, enabled: true, status: "always", testable: false }, { id: "tavily", label: "Tavily", kind: "api", paid: true, fixed: false, enabled: true, status: "ready", testable: true }, { id: "brave", label: "Brave", kind: "api", paid: true, fixed: false, enabled: false, status: "off", testable: true }],
  fetch: [{ id: "builtin", label: "Built-in", kind: "builtin", paid: false, fixed: true, enabled: true, status: "always", testable: false }, { id: "firecrawl", label: "Firecrawl", kind: "api", paid: true, fixed: false, enabled: true, status: "ready", testable: true }],
};

/** What Bot settings reads for the Skills and Secrets sections. */
const BOT_SKILLS = { skills: [
  { name: "web-research", description: "Search the web and read pages", builtin: true, enabled: true },
  { name: "calendar-notes", description: "Keep a running calendar of launches", enabled: true, set_by: "owner", set_at: at(-60) },
  { name: "release-notes", description: "Draft release notes from merged work", enabled: false },
], bundles: [] };
const BOT_SECRETS = { secrets: [{ name: "LAUNCH_API_TOKEN", created_at: at(-3000) }, { name: "STATUS_PAGE_KEY", created_at: at(-2000), last_used_at: at(-30) }] };

/** Files the Bot produced in the direct chat (GET /api/artifacts, conversation scope): the Artifacts widget. */
const ARTIFACTS = [
  { id: "att_summary", name: "week-42-summary.md", mime: "text/markdown", size: 3100 },
  { id: "att_signups", name: "signups.csv", mime: "text/csv", size: 18800 },
  { id: "att_retention", name: "retention-chart.svg", mime: "image/svg+xml", size: 52000 }, // not a raster: a thumbnail <img> would be a request the catalog cannot answer
].map(item => ({ ...item, created_at: "2026-10-09T12:00:00Z", conversation_id: "dm_mochi", conversation_name: "Mochi", conversation_kind: "dm", conversation_archived: false, bot_id: "bot_mochi", message_id: "m1", message_seq: 1, uploaded: false, version_group: item.id, version_count: 1 }));

/** The server behind an isolated component: the records it reads, answers that succeed, and nothing else. */
export function isolated(extra: Record<string, unknown> = {}): Synthetic {
  return (url, init) => {
    const p = url.pathname;
    if (p in extra) return json(extra[p]);
    if (p === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true, language: localStorage.getItem("tofi.language") ?? "en", show_activity: false });
    if (p === "/api/bots") return json({ bots: BOTS });
    if (p === "/api/computers/firecracker/info") return json({ kind: "firecracker", state: "ready" });
    if (p === "/api/computers/firecracker/actions") return json({ ok: true, result: { image_url: SCREEN } });
    if (p === "/api/computer/desktop-ownership") return json({ owner: null, waiting: [] });
    if (p.startsWith("/api/questions/") && p.endsWith("/answer") && init?.method === "POST") return json({ question: null });
    return json({});
  };
}

export function mailDraft(status: MailDraft["status"], extra: Partial<MailDraft> = {}): MailDraft {
  return { draft_id: `draft_${status}`, conversation_id: "dm_mochi", bot_id: "bot_mochi", run_id: "run_mail", to: "launch@example.test", subject: "Week 42 summary", body: "Hi all,\n\nSign-ups are up 8% on last week. The review is at 15:00.\n\nMochi", demo: false, status, revision: 1, created_at: at(41), updated_at: at(41), ...extra };
}
