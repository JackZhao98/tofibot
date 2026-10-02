export type ComposerAttachmentIdentity = { key: string };
export type PendingComposerMessage = { signature: string; id: string };

/**
 * The attachment key is allocated when the user selects a file and remains
 * stable after upload. Server attachment IDs are deliberately excluded: a
 * retry after a successful upload must still reuse the same client message ID.
 */
export function composerMessageSignature(content: string, attachments: readonly ComposerAttachmentIdentity[]) {
  return JSON.stringify([content.trim(), attachments.map((attachment) => attachment.key)]);
}

export function composerClientMessageId(signature: string, pending: PendingComposerMessage | undefined, createId: () => string) {
  return pending?.signature === signature ? pending.id : createId();
}
