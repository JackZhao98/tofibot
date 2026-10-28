export type ComposerAttachmentIdentity = { key: string };
export type PendingComposerMessage = { signature: string; id: string };
export type ComposerReply = { id: string; name: string; excerpt: string };

/** The same payload is used for sending, persisted retries and acknowledgement. */
export function composerMessageContent(content: string, reply?: Pick<ComposerReply, "id">) {
  return reply ? `[message_id=${reply.id}] ${content.trim()}`.trim() : content.trim();
}

/**
 * The attachment key is allocated when the user selects a file and remains
 * stable after upload. Server attachment IDs are deliberately excluded: a
 * retry after a successful upload must still reuse the same client message ID.
 */
export function composerMessageSignature(content: string, attachments: readonly ComposerAttachmentIdentity[], reply?: Pick<ComposerReply, "id">) {
  return JSON.stringify([composerMessageContent(content, reply), attachments.map((attachment) => attachment.key)]);
}

export function composerDraftMatchesMessage(draft: { content: string; attachments: readonly ComposerAttachmentIdentity[]; reply?: ComposerReply; pending?: PendingComposerMessage }, signature: string, id: string) {
  return draft.pending?.id === id && draft.pending.signature === signature && composerMessageSignature(draft.content, draft.attachments, draft.reply) === signature;
}

export function composerClientMessageId(signature: string, pending: PendingComposerMessage | undefined, createId: () => string) {
  return pending?.signature === signature ? pending.id : createId();
}

type StoredAttachment = { key: string; name: string; size: number; lastModified: number };
type DraftShape = { content: string; attachments: StoredAttachment[]; reply?: ComposerReply; pending?: PendingComposerMessage };

/** Reads the stored drafts, dropping anything damaged. Files (bytes) are never stored: such an attachment comes back to be picked again. */
export function parseStoredDrafts(raw: string | null | undefined): Record<string, DraftShape> {
  try {
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Record<string, DraftShape>;
    if (!parsed || typeof parsed !== "object") return {};
    return Object.fromEntries(Object.entries(parsed).flatMap(([id, draft]): [string, DraftShape][] => {
      if (!draft || typeof draft.content !== "string" || !Array.isArray(draft.attachments)) return [];
      const attachments = draft.attachments.filter((a) => a && typeof a.key === "string" && typeof a.name === "string" && Number.isFinite(a.size) && Number.isFinite(a.lastModified)).map((a) => ({ ...a }));
      const reply = draft.reply && typeof draft.reply.id === "string" && typeof draft.reply.name === "string" && typeof draft.reply.excerpt === "string" ? { id: draft.reply.id, name: draft.reply.name, excerpt: draft.reply.excerpt } : undefined;
      return [[id, { content: draft.content, attachments, reply, pending: draft.pending && typeof draft.pending.id === "string" && typeof draft.pending.signature === "string" ? draft.pending : undefined }]];
    }));
  } catch { return {}; }
}

/** `stored`: every draft without its File objects; `kept`: the ones worth keeping (text, files or a reply). */
export function storableDrafts<D extends { content: string; attachments: { file?: unknown }[]; reply?: ComposerReply; pending?: PendingComposerMessage }>(drafts: Record<string, D>) {
  const stored = Object.fromEntries(Object.entries(drafts).map(([id, draft]) => [id, { content: draft.content, reply: draft.reply, pending: draft.pending, attachments: draft.attachments.map(({ file: _file, ...rest }) => rest) }]));
  const kept = Object.fromEntries(Object.entries(stored).filter(([, d]) => d.content || d.attachments.length || d.reply));
  return { stored, kept };
}

/** The acknowledgement of a sent message clears exactly the draft it came from, in its own conversation, and never a newer edit. */
export function clearSentDraft<D extends { content: string; attachments: ComposerAttachmentIdentity[]; reply?: ComposerReply; pending?: PendingComposerMessage }>(drafts: Record<string, D>, conversationId: string, signature: string, clientMessageId: string, empty: D): Record<string, D> {
  const source = drafts[conversationId];
  if (!source || !composerDraftMatchesMessage(source, signature, clientMessageId)) return drafts;
  return { ...drafts, [conversationId]: empty };
}
