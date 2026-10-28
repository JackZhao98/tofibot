// Lazy: loaded when a message is sent (so it is cached before a failure can happen) and when an outbox entry has to come back.
import type { Attachment, Message } from "./types";
import type { FailReason, OutboxEntry } from "./outbox";

const MAX_ENTRIES = 50;
const MAX_AGE_MS = 7 * 24 * 3600 * 1000;


/** The on-screen copy of an outbox entry: a failed local message (after a reload nobody knows how far it got). */
export function entryMessage(entry: OutboxEntry, seq: number): Message {
  const attachments: Attachment[] = entry.attachments.map(a => ({
    id: a.id ?? `local:${a.key}`, conversation_id: entry.conversationId, name: a.name, mime: a.type, size: a.size, created_at: entry.createdAt,
    clientKey: a.key,
    ...(a.id ? {} : { pending: { progress: 0, failed: true } }),
  }));
  return {
    id: `local:${entry.clientMessageId}`, conversation_id: entry.conversationId, seq, role: "user", content: entry.content, created_at: entry.createdAt,
    client_message_id: entry.clientMessageId, attachments: attachments.length ? attachments : undefined,
    // `auto`: after a reload nothing is promised (it never sends an old message by itself).
    local: { state: "failed", error: entry.error, reason: entry.reason ?? "network", auto: true },
  };
}

/**
 * Applies the outbox to a freshly loaded conversation: an entry the server already holds is done (dropped); the rest are
 * added as failed messages after the newest stored one. Returns the messages to append and the entries that remain.
 */
export function reconcileOutbox(entries: readonly OutboxEntry[], conversationId: string, loaded: readonly Message[], present: ReadonlySet<string>): { add: Message[]; keep: OutboxEntry[]; done: string[] } {
  const stored = new Set(loaded.map(m => m.client_message_id).filter((id): id is string => Boolean(id)));
  let seq = loaded.reduce((max, m) => m.local ? max : Math.max(max, m.seq), 0);
  const add: Message[] = [];
  const keep: OutboxEntry[] = [];
  const done: string[] = [];
  for (const entry of entries) {
    if (entry.conversationId !== conversationId) { keep.push(entry); continue; }
    if (stored.has(entry.clientMessageId)) { done.push(entry.clientMessageId); continue; }
    keep.push(entry);
    if (!present.has(entry.clientMessageId)) add.push(entryMessage(entry, ++seq));
  }
  return { add, keep, done };
}

/** Edit puts a failed message back in the composer, ahead of what is typed there. */
export function mergeEditedDraft<D extends { content: string; reply?: unknown; attachments: { key: string }[]; pending?: unknown }>(current: D, source: { plain: string; reply?: D["reply"]; attachments: D["attachments"] }): D {
  return {
    ...current,
    content: current.content.trim() ? `${source.plain}\n${current.content}` : source.plain,
    reply: current.reply ?? source.reply,
    attachments: [...source.attachments, ...current.attachments.filter(a => !source.attachments.some(b => b.key === a.key))].slice(0, 8).map(a => ({ ...a })),
    pending: undefined,
  };
}

/** A failure can be ambiguous (the request may have reached the server before the answer was lost): the stored copy, if any. */
export const findStored = (messages: readonly Message[], clientMessageId: string) => messages.find(m => m.role === "user" && m.client_message_id === clientMessageId);

type UploadItem = { key: string; id?: string; name: string; file?: File };
/** Uploads every file that is not on the server yet, in order; progress is throttled (about 12 repaints a second, only for a visible step). */
export async function uploadMissing(job: {
  items: UploadItem[]; upload: (file: File, onProgress: (fraction: number) => void) => Promise<Attachment>; reselect: (name: string) => string;
  progress: (key: string, fraction: number) => void; uploaded: (item: UploadItem, attachment: Attachment) => void;
}) {
  for (const item of job.items) {
    if (item.id) continue;
    if (!item.file) throw new Error(job.reselect(item.name));
    let shown = 0, shownAt = 0;
    const attachment = await job.upload(item.file, fraction => {
      const now = performance.now();
      if (fraction < 1 && (fraction - shown < .03 || now - shownAt < 80)) return;
      shown = fraction; shownAt = now;
      job.progress(item.key, fraction);
    });
    item.id = attachment.id;
    job.uploaded(item, attachment);
  }
}

/** Network and server trouble can pass by itself; a refusal (4xx) will not, so it never retries on its own. */
export function failReason(cause: unknown, online: boolean): FailReason {
  const status = typeof cause === "object" && cause !== null && "status" in cause ? Number((cause as { status: unknown }).status) : NaN;
  if (Number.isFinite(status)) return status >= 500 || status === 408 || status === 425 || status === 429 ? "server" : "rejected";
  return online ? "network" : "offline";
}
export const retryable = (reason?: FailReason) => reason !== "rejected";

export function readOutbox(key: string, now = Date.now()): OutboxEntry[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(key) ?? "[]") as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.flatMap((raw): OutboxEntry[] => {
      const e = raw as Partial<OutboxEntry> | null;
      if (!e || typeof e.clientMessageId !== "string" || typeof e.conversationId !== "string" || typeof e.content !== "string" || typeof e.createdAt !== "string") return [];
      if (now - Date.parse(e.createdAt) > MAX_AGE_MS) return [];
      const attachments = Array.isArray(e.attachments) ? e.attachments.filter(a => a && typeof a.key === "string" && typeof a.name === "string") : [];
      return [{ ...e, plain: typeof e.plain === "string" ? e.plain : e.content, attachments } as OutboxEntry];
    }).slice(-MAX_ENTRIES);
  } catch { return []; }
}

