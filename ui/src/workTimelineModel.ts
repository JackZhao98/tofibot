import type { Attachment, Message, ToolActivity } from "./types";
import { compareActivityTime, orderToolActivities } from "./toolTimeline";

export type WorkCapture = { seq: number; call_id?: string; attachment_id?: string; taken_at: string; label: string; redacted: boolean };
export type WorkEntry = { kind: "narration"; note: Message } | { kind: "group"; tools: ToolActivity[]; key: string };

/** Keep sub-millisecond ordering across streamed events and reloaded pages. */
export function workEntries(tools: ToolActivity[], notes: Message[]): WorkEntry[] {
  const events = [
    ...orderToolActivities(tools).filter(tool => tool.name !== "say").map(tool => ({ at: tool.started_at, tool, note: undefined })),
    ...notes.filter(note => note.kind === "progress").sort((a, b) => a.seq - b.seq).map(note => ({ at: note.created_at, note, tool: undefined })),
  ].sort((a, b) => compareActivityTime(a.at, b.at));
  const out: WorkEntry[] = [];
  for (const event of events) {
    if (event.note) out.push({ kind: "narration", note: event.note });
    else if (event.tool) {
      const last = out.at(-1);
      if (last?.kind === "group" && last.tools[0].run_id === event.tool.run_id) last.tools.push(event.tool);
      else out.push({ kind: "group", tools: [event.tool], key: `${event.tool.run_id}:${event.tool.call_id}` });
    }
  }
  return out;
}

/** Only definite page reads and commands get specific labels; mixed batches are steps. */
export function workGroupKind(tools: ToolActivity[]): "commands" | "pages" | "steps" {
  if (tools.some(tool => tool.status !== "completed")) return "steps";
  if (tools.every(tool => {
    if (["computer_shell", "computer_exec", "shell", "exec_command"].includes(tool.name)) return true;
    try {
      const args = JSON.parse(tool.arguments);
      return tool.name === "computer_action" && ["shell.exec", "sandbox.exec"].includes(args.action)
        || tool.name === "computer_terminal" && args.action === "open" && typeof args.command === "string";
    } catch { return false; }
  })) return "commands";
  if (tools.every(tool => ["web_fetch", "fetch_url"].includes(tool.name))) return "pages";
  return "steps";
}

export function groupPictures(tools: ToolActivity[], captures: WorkCapture[]): Attachment[] {
  const first = tools[0];
  if (!first) return [];
  const calls = new Set(tools.map(tool => tool.call_id));
  const pictures = captures.filter(capture => capture.attachment_id && !capture.redacted && capture.call_id && calls.has(capture.call_id))
    .map(capture => ({ id: capture.attachment_id!, name: capture.label, created_at: capture.taken_at }));
  for (const tool of tools) {
    // Sharing a personal capture explicitly publishes an attachment. Unshared Mac captures are never retained.
    try {
      const result = JSON.parse(tool.result);
      const shared = result?.shared;
      if (shared && typeof shared.attachment_id === "string") pictures.push({ id: shared.attachment_id, name: typeof shared.name === "string" ? shared.name : "", created_at: tool.updated_at });
    } catch { /* A tool result need not be JSON; captures still come from the typed endpoint. */ }
  }
  return [...new Map(pictures.map(picture => [picture.id, { ...picture, conversation_id: first.conversation_id, mime: "image/jpeg", size: 0 }])).values()];
}
