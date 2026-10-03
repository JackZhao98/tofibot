// Full production entry point, CSS and owner gate. All API/SSE traffic fails closed
// to this synthetic store; the controls only release the synthetic delayed response.
import type { Conversation, Message, Run } from "../../src/types";

const at = "2026-10-03T00:00:00Z";
const conversations: Conversation[] = ["A", "B"].map(id => ({ id, name: `Synthetic ${id}`, kind: "dm", bot_id: `bot-${id}`, bot_ids: [`bot-${id}`], updated_at: at }));
const key = "reply-card-audit-server";
const stored = JSON.parse(sessionStorage.getItem(key) ?? "{}") as Record<string, { message: Message; run: Run }>;
let pending: { resolve: (response: Response) => void; reject: (error: Error) => void; result: { message: Message; run: Run } } | undefined;
const requests: { method: string; path: string; body?: unknown }[] = [];
const originals: Message[] = [
  { id: "target-A", conversation_id: "A", seq: 1, role: "assistant", sender_bot_id: "bot-A", content: "Synthetic reply target", created_at: at },
  { id: "forward-A", conversation_id: "A", seq: 2, role: "assistant", kind: "message_ref", content: "Synthetic forwarded chat", notice: { type: "forward", from_bot_id: "bot-A", to_bot_id: "bot-B", target_conversation_id: "B", target_run_id: "", target_name: "Synthetic B" }, created_at: at },
  { id: "target-B", conversation_id: "B", seq: 1, role: "assistant", sender_bot_id: "bot-B", content: "Synthetic other conversation", created_at: at },
];
class SyntheticEvents extends EventTarget {
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;
  constructor(public url: string) { super(); setTimeout(() => { if (!this.closed) this.onopen?.(new Event("open")); }, 20); }
  close() { this.closed = true; }
}
window.EventSource = SyntheticEvents as unknown as typeof EventSource;
function report() {
  document.getElementById("fixture-state")!.textContent = ` requests=${requests.filter(r => r.method === "POST" && r.path.endsWith("/messages")).length}; unique=${Object.keys(stored).length}; pending=${Boolean(pending)}`;
}
document.getElementById("ack")!.onclick = () => { const current = pending; pending = undefined; current?.resolve(Response.json(current.result)); report(); };
document.getElementById("fail")!.onclick = () => { const current = pending; pending = undefined; current?.reject(new Error("Synthetic response lost after server acceptance")); report(); };
document.getElementById("reset")!.onclick = () => { sessionStorage.clear(); location.reload(); };
document.getElementById("attachment")!.onclick = () => {
  const input = document.querySelector<HTMLInputElement>('.composer input[type="file"]:not([accept])');
  if (!input) return;
  const files = new DataTransfer(); files.items.add(new File(["synthetic attachment"], "synthetic.txt", { type: "text/plain", lastModified: 1 }));
  input.files = files.files; input.dispatchEvent(new Event("change", { bubbles: true }));
};
window.fetch = async (input, init) => {
  const url = new URL(String(input), location.origin), path = url.pathname, method = init?.method ?? "GET";
  const body = init?.body && typeof init.body === "string" ? JSON.parse(init.body) : undefined;
  requests.push({ method, path, body }); report();
  if (method === "POST" && path.endsWith("/read")) return Response.json({ read_seq: 99 });
  if (method === "POST" && /^\/api\/conversations\/[^/]+\/attachments$/.test(path)) return Response.json({ attachment: { id: "synthetic-upload" }, id: "synthetic-upload" });
  if (method === "POST" && /^\/api\/conversations\/[^/]+\/messages$/.test(path)) {
    const id = path.split("/")[3], client = body.client_message_id;
    const result = stored[client] ?? { message: { id: client, conversation_id: id, seq: 3 + Object.keys(stored).length, role: "user", content: body.content, created_at: at }, run: { id: `run-${client}`, conversation_id: id, bot_id: `bot-${id}`, trigger_message_id: client, status: "done", created_at: at, updated_at: at } };
    stored[client] = result; sessionStorage.setItem(key, JSON.stringify(stored));
    return new Promise<Response>((resolve, reject) => { pending = { resolve, reject, result }; report(); });
  }
  if (method !== "GET") throw new Error(`Forbidden synthetic mutation: ${method} ${path}`);
  if (path === "/api/auth/session") return Response.json({ enabled: true, setup_required: false, authenticated: true, owner: { id: "synthetic", username: "Synthetic Owner" } });
  if (path === "/api/preferences") return Response.json({ timezone: "UTC", timezone_configured: true });
  if (path === "/api/server-info") return Response.json({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000006" });
  if (path === "/api/bots") return Response.json({ bots: conversations.map(c => ({ id: c.bot_id, name: c.name, instructions: "", model: "synthetic", dm_conversation_id: c.id, created_at: at })) });
  if (path === "/api/conversations") return Response.json({ conversations });
  if (path === "/api/config") return Response.json({ model_configured: true, default_model: "synthetic", provider: "synthetic" });
  if (path === "/api/auth/codex") return Response.json({ connected: false });
  if (path === "/api/computers/firecracker/info") return Response.json({ kind: "firecracker", state: "ready" });
  if (path.endsWith("/messages")) { const id = path.split("/")[3]; return Response.json({ messages: [...originals, ...Object.values(stored).map(r => r.message)].filter(m => m.conversation_id === id), drafts: [], has_more: false, event_cursor: 1 }); }
  if (path.endsWith("/runs")) return Response.json({ runs: [] });
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
Object.assign(window, { replyCardAudit: { requests, stored } });
await import("../../src/main");
