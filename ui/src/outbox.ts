import type { Attachment, Message } from "./types";
import type { ComposerReply } from "./composerDraft";

/**
 * The outbox: every message the person has sent that the server has not confirmed yet. It is kept in localStorage (per
 * server instance, like the composer drafts) so a failed message survives a reload, a closed tab or an App restart.
 * Retrying is always safe: the server de-duplicates by client_message_id (AddUserRuns replays the stored result).
 * A file that was not uploaded yet cannot be stored (a File is not serialisable); such a message comes back after a reload
 * with its file named, and "Edit" puts it in the composer to pick the file again (the draft's existing reselect flow).
 */
export type FailReason = "offline" | "network" | "server" | "rejected";
export type OutboxAttachment = { key: string; id?: string; name: string; size: number; lastModified: number; type: string };
export type OutboxEntry = {
  clientMessageId: string;
  conversationId: string;
  /** The wire content, including the reply marker. */
  content: string;
  /** What the person typed, without the marker (Edit puts it back in the composer). */
  plain: string;
  attachments: OutboxAttachment[];
  reply?: ComposerReply;
  createdAt: string;
  error?: string;
  reason?: FailReason;
};

const MAX_ENTRIES = 50;
const MAX_AGE_MS = 7 * 24 * 3600 * 1000;

/** Per server instance and per owner, like the composer drafts: a shared browser never mixes people's unsent messages. */
export const outboxStorageKey = (origin: string, instanceId: string, ownerId?: string) => `tofi:outbox:${origin}:${instanceId}${ownerId ? `:${ownerId}` : ""}`;

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

export function writeOutbox(key: string, entries: readonly OutboxEntry[]) {
  try {
    if (entries.length) localStorage.setItem(key, JSON.stringify(entries.slice(-MAX_ENTRIES)));
    else localStorage.removeItem(key);
  } catch { /* Storage blocked or full: the message stays in memory and on screen. */ }
}

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
