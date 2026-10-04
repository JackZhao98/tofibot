// Real production App and owner gate, with every HTTP/SSE request intercepted.
// Fixture credentials are fixed synthetic strings and never sent to a server.
import type { Conversation, ConversationWebhook, Message, Run } from "../../src/types";

const at = "2026-10-04T00:00:00Z";
const humanID = "11111111-1111-4111-8111-111111111111";
const webhookID = "22222222-2222-4222-8222-222222222222";
const conversations: Conversation[] = ["A", "B"].map(id => ({ id, name: `Synthetic ${id}`, kind: "dm", bot_id: `bot-${id}`, bot_ids: [`bot-${id}`], updated_at: at }));
conversations.push({ id: "G", name: "Synthetic Group", kind: "group", bot_ids: ["bot-A", "bot-B"], user_visible: true, updated_at: at });
conversations[0].last_message = { id: webhookID, seq: 2, role: "user", kind: "webhook_event", content: "SYNTHETIC_EXTERNAL_EVENT", created_at: at };
const messages: Message[] = [
  { id: humanID, conversation_id: "A", seq: 1, role: "user", content: "SYNTHETIC_HUMAN", created_at: at },
  { id: webhookID, conversation_id: "A", seq: 2, role: "user", kind: "webhook_event", run_id: "webhook-root", content: `[message_id=${humanID}] SYNTHETIC_EXTERNAL_EVENT`, created_at: at },
  { id: "webhook-second", conversation_id: "A", seq: 3, role: "user", kind: "webhook_event", content: "SYNTHETIC_SECOND_EVENT", created_at: at },
  { id: "reply-to-webhook", conversation_id: "A", seq: 4, role: "assistant", sender_bot_id: "bot-A", content: `[message_id=${webhookID}] SYNTHETIC_ASSISTANT_REPLY`, created_at: at },
  { id: "forward-A", conversation_id: "A", seq: 5, role: "assistant", kind: "message_ref", content: "Messaged Synthetic B", notice: { type: "forward", from_bot_id: "bot-A", to_bot_id: "bot-B", target_conversation_id: "B", target_run_id: "", target_name: "Synthetic B" }, created_at: at },
  { id: "forwarded-webhook", conversation_id: "B", seq: 1, role: "user", kind: "webhook_event", content: "SYNTHETIC_FORWARDED_EVENT", created_at: at },
  { id: "group-webhook", conversation_id: "G", seq: 1, role: "user", kind: "webhook_event", content: "SYNTHETIC_GROUP_EVENT", created_at: at },
];
const runs: Run[] = [{ id: "human-failed", conversation_id: "A", bot_id: "bot-A", trigger_message_id: humanID, status: "failed", error: "synthetic failure", created_at: at, updated_at: at }];
const requests: { method: string; path: string; body?: unknown }[] = [];
const copies: string[] = [];
const endpoints = new Map<string, ConversationWebhook>();
let ownerID = "synthetic-owner";
let delay = false, conflict = false;
let release: (() => void) | undefined;
const query = new URLSearchParams(location.search);
const enabled = query.get("owner") !== "disabled";
const capability = query.get("capability") !== "disabled";

class SyntheticEvents extends EventTarget {
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;
  constructor(public url: string) { super(); setTimeout(() => { if (!this.closed) this.onopen?.(new Event("open")); }, 20); }
  close() { this.closed = true; }
}
window.EventSource = SyntheticEvents as unknown as typeof EventSource;
Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async (text: string) => { copies.push(text); } } });
window.fetch = async (input, init) => {
  const url = new URL(String(input), location.origin), path = url.pathname, method = init?.method ?? "GET";
  const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
  requests.push({ method, path, body });
  const webhook = /^\/api\/conversations\/(A|B|G)\/webhook(\/rotate)?$/.exec(path);
  if (webhook) {
    const id = webhook[1], key = `${ownerID}:${id}`;
    const old = endpoints.get(key) ?? { configured: false, enabled: false };
    if (method === "GET") return Response.json(old);
    if (!enabled || !capability) return Response.json({ error: { code: "unavailable" } }, { status: 403 });
    if (conflict || ((method === "DELETE" || webhook[2]) && body.expected_version !== old.version)) { conflict = false; return Response.json({ error: { code: "webhook_conflict" } }, { status: 409 }); }
    if (method === "DELETE") { endpoints.set(key, { ...old, enabled: false }); return new Response(null, { status: 204 }); }
    if (method === "POST" && (!old.enabled || webhook[2])) {
      const next = { configured: true, enabled: true, hook_id: `synthetic-hook-${id}`, version: (old.version ?? 0) + 1, url: `https://synthetic.invalid/api/webhooks/synthetic-hook-${id}`, created_at: old.created_at ?? at, ...(old.configured ? { rotated_at: at } : {}) };
      endpoints.set(key, next);
      const response = { ...next, secret: `SYNTHETIC_ONLY_${ownerID}_${id}_VERSION_${next.version}` };
      if (delay) { delay = false; return new Promise<Response>(resolve => { release = () => { release = undefined; resolve(Response.json(response)); report(); }; report(); }); }
      return Response.json(response, { status: webhook[2] ? 200 : 201 });
    }
    return Response.json({ error: { code: "webhook_conflict" } }, { status: 409 });
  }
  if (method === "POST" && path.endsWith("/read")) return Response.json({ read_seq: 99 });
  if (method !== "GET") throw new Error(`Forbidden synthetic mutation: ${method} ${path}`);
  if (path === "/api/server-info") return Response.json({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000007", capabilities: { inbound_webhooks: capability } });
  if (path === "/api/auth/session") return Response.json({ enabled, setup_required: false, authenticated: enabled, password_transport_allowed: true, owner: enabled ? { id: ownerID, username: "Synthetic Owner", email: "owner@example.test", role: "user" } : undefined });
  if (path === "/api/preferences") return Response.json({ timezone: "UTC", timezone_configured: true });
  if (path === "/api/bots") return Response.json({ bots: conversations.filter(c => c.kind === "dm").map(c => ({ id: c.bot_id, name: c.name, instructions: "", model: "synthetic", dm_conversation_id: c.id, created_at: at })) });
  if (path === "/api/conversations") return Response.json({ conversations });
  if (path === "/api/config") return Response.json({ model_configured: true, default_model: "synthetic", provider: "synthetic" });
  if (path === "/api/auth/codex") return Response.json({ connected: false });
  if (path === "/api/computers/firecracker/info") return Response.json({ kind: "firecracker", state: "ready" });
  if (path.endsWith("/messages")) return Response.json({ messages: messages.filter(m => m.conversation_id === path.split("/")[3]), drafts: [], has_more: false, event_cursor: 1 });
  if (path.endsWith("/runs")) return Response.json({ runs: runs.filter(r => r.conversation_id === path.split("/")[3]) });
  if (path.endsWith("/tools")) return Response.json({ activities: [], summaries: [] });
  if (path.endsWith("/memories")) return Response.json({ memories: [] });
  if (path.endsWith("/questions")) return Response.json({ questions: [] });
  if (path.endsWith("/schedules")) return Response.json({ schedules: [] });
  if (path.endsWith("/schedule-occurrences")) return Response.json({ occurrences: [] });
  if (path.endsWith("/work-items")) return Response.json({ work_items: [] });
  if (path.endsWith("/drafts") || path === "/api/mail-drafts") return Response.json({ drafts: [] });
  if (path === "/api/secret-inputs") return Response.json({ secret_inputs: [], requests: [] });
  if (path === "/api/computer/desktop-ownership") return Response.json({ owner: null });
  throw new Error(`Unexpected synthetic read: ${method} ${path}`);
};
function report() {
  const output = document.getElementById("webhook-fixture-status");
  if (output) output.textContent = ` delayed response=${Boolean(release)}; delay armed=${delay}; conflict armed=${conflict}; account=${ownerID}`;
}
const actions = {
  delayNext: () => { delay = true; report(); },
  hasDelayed: () => Boolean(release),
  release: () => release?.(),
  conflictNext: () => { conflict = true; report(); },
  switchAccount: () => { ownerID = "synthetic-second-owner"; window.dispatchEvent(new Event("focus")); report(); },
};
for (const [id, action] of [["audit-delay", actions.delayNext], ["audit-release", actions.release], ["audit-account", actions.switchAccount], ["audit-conflict", actions.conflictNext]] as const) {
  const control = document.getElementById(id);
  if (control) control.onclick = action;
}
report();
Object.assign(window, { webhookAudit: { requests, copies, ...actions } });
await import("../../src/main");
