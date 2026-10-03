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
