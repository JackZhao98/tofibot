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

/** Per server instance and per owner, like the composer drafts: a shared browser never mixes people's unsent messages. */
export const outboxStorageKey = (origin: string, instanceId: string, ownerId?: string) => `tofi:outbox:${origin}:${instanceId}${ownerId ? `:${ownerId}` : ""}`;

const MAX_ENTRIES = 50;

export function writeOutbox(key: string, entries: readonly OutboxEntry[]) {
  try {
    if (entries.length) localStorage.setItem(key, JSON.stringify(entries.slice(-MAX_ENTRIES)));
    else localStorage.removeItem(key);
  } catch { /* Storage blocked or full: the message stays in memory and on screen. */ }
}

/** One outgoing message with everything a (re)send needs. `attachments[].file` is present until the upload finished or the page reloaded. */
export type SendPayload = { conversationId: string; content: string; plain: string; reply?: ComposerReply; attachments: (OutboxAttachment & { file?: File })[]; createdAt: string; inFlight: boolean; auto: boolean };
