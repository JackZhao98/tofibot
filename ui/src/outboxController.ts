// Lazy (loaded in idle time after start): everything that happens to a message after its bubble is on screen. Keeping it out of
// the entry chunk is what holds the entry budget; `send` (the bubble, instantly) stays in App.tsx.
import type { MutableRefObject } from "react";
import { writeOutbox, type FailReason, type OutboxEntry, type SendPayload } from "./outbox";
import { failReason, findStored, mergeEditedDraft, readOutbox, reconcileOutbox, retryable, uploadMissing } from "./outboxActions";
import type { Attachment, Message, Run } from "./types";

/** What the controller borrows from the workspace. Read through a getter, so it always sees the newest closures. */
export type OutboxContext = {
  payloads: MutableRefObject<Map<string, SendPayload>>;
  outbox: MutableRefObject<OutboxEntry[]>;
  outboxKey: MutableRefObject<string | null>;
  activeId: MutableRefObject<string | null>;
  messages: MutableRefObject<Message[]>;
  patch: (conversationId: string, changeMessages?: (list: Message[]) => Message[], changeRuns?: (list: Run[]) => Run[]) => void;
  setMessages: (update: (list: Message[]) => Message[]) => void;
  setLastUserActivity: (update: (current: Record<string, number>) => Record<string, number>) => void;
  updatePreview: (message: Message) => void;
  setError: (text: string) => void;
  bump: () => void;
  updateDraft: (conversationId: string, update: (draft: any) => any) => void;
  /** Translated text (the app's i18n; this chunk imports none) and request()'s error shape for a failed upload. */
  text: (key: string, options?: Record<string, unknown>) => string;
  errors: { ApiError: new (status: number, message: string, code?: string, data?: Record<string, unknown>) => Error; codeText: (code?: string) => string | undefined };
  /** Helpers the entry chunk already holds. A lazy chunk that imports an entry module reshuffles the shared chunks (+700 B gzip), so they come in here. */
  helpers: { errorText: (cause: unknown) => string; mergeRunUpdates: (runs: Run[], updates: Run[]) => Run[]; adoptPreview: (tempId: string, id: string, file: Blob) => void; dropPreview: (tempId: string) => void };
  /** The api calls (this chunk imports api.ts from nowhere: a lazy chunk that does reshuffles the shared chunks, +700 B gzip). */
  api: { sendMessage: (conversationId: string, input: { content: string; client_message_id: string; attachment_ids?: string[] }) => Promise<{ message: Message; run?: Run | null; runs?: Run[] }>; messages: (conversationId: string) => Promise<{ messages: Message[] }>; uploadViaRequest: (path: string, body: FormData) => Promise<Attachment> };
};

/** request()'s error shape for a failed XHR upload. */
function uploadError(c: OutboxContext, status: number, body: unknown) {
  const error = (body as { error?: { message?: string; code?: string } & Record<string, unknown> } | null)?.error;
  if (status === 401) window.dispatchEvent(new Event("tofi:unauthorized"));
  return new c.errors.ApiError(status, c.errors.codeText(error?.code) ?? error?.message ?? c.text("common:apiError.request_failed", { status }), error?.code, error);
}

/** The server's copy of a sent message replaces its optimistic one wherever either arrives first. */
export function reconcileSent(list: Message[], server: Message): Message[] {
  const client = server.client_message_id;
  const rest = list.filter(item => item.id !== server.id && !(client && item.local && item.client_message_id === client));
  return [...rest, server].sort((a, b) => a.seq - b.seq);
}

export function createOutboxController(get: () => OutboxContext) {
  const saveOutbox = () => { const c = get(); if (c.outboxKey.current) writeOutbox(c.outboxKey.current, c.outbox.current); };
  const outboxPatch = (id: string, patch: Partial<OutboxEntry>) => { const c = get(); c.outbox.current = c.outbox.current.map(item => item.clientMessageId === id ? { ...item, ...patch } : item); saveOutbox(); };
  const outboxDrop = (id: string) => { const c = get(); if (!c.outbox.current.some(item => item.clientMessageId === id)) return; c.outbox.current = c.outbox.current.filter(item => item.clientMessageId !== id); saveOutbox(); };
  const setLocalState = (conversationId: string, clientMessageId: string, local: NonNullable<Message["local"]>, patchAttachment?: (a: Attachment) => Attachment) =>
    get().patch(conversationId, list => list.map(item => item.local && item.client_message_id === clientMessageId ? { ...item, local, ...(patchAttachment && item.attachments ? { attachments: item.attachments.map(patchAttachment) } : {}) } : item));
  const stripFiles = (payload: SendPayload) => payload.attachments.map(({ file: _file, ...rest }) => rest);

  /**
   * Sends (or re-sends) one outgoing message: uploads the files that are not up yet (progress on their tiles), then posts the
   * message. The same client_message_id on every attempt makes a retry safe: the server stores it once and replays its answer.
   */
  async function deliver(clientMessageId: string, auto = false): Promise<void> {
    const c = get();
    const payload = c.payloads.current.get(clientMessageId);
    if (!payload || payload.inFlight) return;
    payload.inFlight = true;
    payload.auto = payload.auto || auto;
    const { conversationId } = payload;
    const forThisMessage = (item: Message) => Boolean(item.local && item.client_message_id === clientMessageId && item.attachments);
    try {
      await uploadMissing({
        items: payload.attachments,
        upload: (file, onProgress) => import("./uploadProgress").then(({ uploadWithProgress }) => uploadWithProgress({ fail: (status, body) => uploadError(get(), status, body), viaRequest: get().api.uploadViaRequest }, `/api/conversations/${encodeURIComponent(conversationId)}/attachments`, file, onProgress)),
        reselect: name => get().text("chat:composer.reselect_file", { name }),
        progress: (key, fraction) => get().patch(conversationId, list => list.map(item => forThisMessage(item) ? { ...item, attachments: item.attachments!.map(a => a.clientKey === key && a.pending ? { ...a, pending: { progress: fraction } } : a) } : item)),
        uploaded: (attachment, uploaded) => {
          get().helpers.adoptPreview(`local:${attachment.key}`, uploaded.id, attachment.file!);
          outboxPatch(clientMessageId, { attachments: stripFiles(payload) });
          get().patch(conversationId, list => list.map(item => forThisMessage(item) ? { ...item, attachments: item.attachments!.map(a => a.clientKey === attachment.key ? { ...uploaded, clientKey: attachment.key } : a) } : item));
        },
      });
      const result = await get().api.sendMessage(conversationId, { content: payload.content, client_message_id: clientMessageId, attachment_ids: payload.attachments.flatMap(a => a.id ? [a.id] : []) });
      c.payloads.current.delete(clientMessageId);
      outboxDrop(clientMessageId);
      // This response is known human ingress, even if its run event has not arrived yet.
      const sentAt = Date.parse(result.message.created_at);
      if (Number.isFinite(sentAt)) get().setLastUserActivity(current => ({ ...current, [conversationId]: Math.max(current[conversationId] ?? 0, sentAt) }));
      // A message to a Bot that was already working creates no run (`queued` names the run that will read it).
      const newRuns = result.runs?.length ? result.runs : result.run ? [result.run] : [];
      // The bubble keeps its identity (blockKey); only its content, status and runs update.
      get().patch(conversationId, list => reconcileSent(list, { ...result.message, client_message_id: result.message.client_message_id ?? clientMessageId }), list => get().helpers.mergeRunUpdates(list, newRuns));
      get().updatePreview(result.message);
      if (get().activeId.current === conversationId) get().setError("");
    } catch (cause) {
      const reason: FailReason = failReason(cause, navigator.onLine);
      const text = get().helpers.errorText(cause);
      outboxPatch(clientMessageId, { error: text, reason, attachments: stripFiles(payload) });
      setLocalState(conversationId, clientMessageId, { state: "failed", error: text, reason, auto: payload.auto || !retryable(reason) }, a => a.pending ? { ...a, pending: { progress: 0, failed: true } } : a);
    } finally {
      payload.inFlight = false;
    }
  }

  /** The payload of a message restored after a reload: usable only if every file is already on the server. */
  function restorePayload(clientMessageId: string): SendPayload | undefined {
    const c = get();
    const entry = c.outbox.current.find(item => item.clientMessageId === clientMessageId);
    if (!entry || entry.attachments.some(a => !a.id)) return undefined;
    const payload: SendPayload = { conversationId: entry.conversationId, content: entry.content, plain: entry.plain, reply: entry.reply, attachments: entry.attachments.map(a => ({ ...a })), createdAt: entry.createdAt, inFlight: false, auto: true };
    c.payloads.current.set(clientMessageId, payload);
    return payload;
  }

  /** Retry on the bubble: the same message goes out again under the same id, with nothing retyped. */
  function retrySend(clientMessageId: string, auto = false) {
    const payload = get().payloads.current.get(clientMessageId) ?? restorePayload(clientMessageId);
    if (!payload || payload.inFlight) return Promise.resolve();
    setLocalState(payload.conversationId, clientMessageId, { state: auto ? "retrying" : "sending", auto: payload.auto || auto }, a => a.pending ? { ...a, pending: { progress: 0 } } : a);
    return deliver(clientMessageId, auto);
  }

  /** Delete on a failed bubble: the message goes away for good, here and in the outbox. */
  function deleteUnsent(clientMessageId: string) {
    const c = get();
    const payload = c.payloads.current.get(clientMessageId);
    const entry = c.outbox.current.find(item => item.clientMessageId === clientMessageId);
    const conversationId = payload?.conversationId ?? entry?.conversationId;
    if (!conversationId || payload?.inFlight) return;
    for (const attachment of payload?.attachments ?? entry?.attachments ?? []) c.helpers.dropPreview(`local:${attachment.key}`);
    c.payloads.current.delete(clientMessageId);
    outboxDrop(clientMessageId);
    c.patch(conversationId, list => list.filter(item => !(item.local && item.client_message_id === clientMessageId)));
  }

  /**
   * Edit on a failed bubble: the text, reply and files go back to the composer (ahead of what is typed there) and the bubble goes.
   * If the failure was ambiguous (the request may have reached the server) the stored copy is looked for first: editing a message
   * that was in fact delivered would send it twice. Found: the bubble becomes the stored message and the person is told.
   */
  async function editUnsent(clientMessageId: string) {
    const c = get();
    const payload = c.payloads.current.get(clientMessageId);
    const entry = c.outbox.current.find(item => item.clientMessageId === clientMessageId);
    const source = payload ?? (entry && { conversationId: entry.conversationId, plain: entry.plain, reply: entry.reply, attachments: entry.attachments, inFlight: false });
    if (!source || source.inFlight) return;
    const { conversationId } = source;
    try {
      if (retryable(entry?.reason)) {
        const stored = findStored((await c.api.messages(conversationId)).messages, clientMessageId);
        if (stored) {
          c.payloads.current.delete(clientMessageId);
          outboxDrop(clientMessageId);
          c.patch(conversationId, list => reconcileSent(list, stored));
          c.updatePreview(stored);
          c.setError(c.text("chat:composer.already_sent"));
          return;
        }
      }
      c.updateDraft(conversationId, current => mergeEditedDraft(current, source));
    } catch {
      // Not knowing is not a reason to risk a duplicate: the bubble stays and says what to do.
      c.setError(c.text("chat:composer.check_failed"));
      return;
    }
    for (const attachment of source.attachments) c.helpers.dropPreview(`local:${attachment.key}`);
    c.payloads.current.delete(clientMessageId);
    outboxDrop(clientMessageId);
    c.patch(conversationId, list => list.filter(item => !(item.local && item.client_message_id === clientMessageId)));
    requestAnimationFrame(() => document.querySelector<HTMLTextAreaElement>(".composer textarea")?.focus());
  }

  /**
   * The connection is back (browser online event, or the live stream reconnected): each message that failed for a reason that
   * can pass (offline, network, 5xx) is sent again once, in the order it was written, and says so on its bubble ("Sending again").
   * A refusal (4xx) and a message restored after a reload are never sent by themselves.
   */
  function autoRetry() {
    const c = get();
    const failed = c.outbox.current
      .filter(entry => entry.reason && retryable(entry.reason) && c.payloads.current.get(entry.clientMessageId)?.auto === false)
      .sort((a, b) => a.createdAt.localeCompare(b.createdAt));
    void (async () => { for (const entry of failed) await retrySend(entry.clientMessageId, true); })();
  }

  /** Messages that were still unsent when the page closed come back as failed bubbles, once their conversation is on screen. */
  function restore(conversationId: string) {
    const c = get();
    const present = new Set(c.messages.current.filter(item => item.local).map(item => item.client_message_id ?? ""));
    const { add, keep, done } = reconcileOutbox(c.outbox.current, conversationId, c.messages.current, present);
    if (done.length) { c.outbox.current = keep; saveOutbox(); }
    if (add.length) c.setMessages(current => [...current, ...add.filter(item => !current.some(row => row.client_message_id === item.client_message_id))]);
  }

  /** A stored copy arrived over the stream: nothing is left to resend, even if the POST answer was lost. */
  const confirmed = (clientMessageId: string) => { get().payloads.current.delete(clientMessageId); outboxDrop(clientMessageId); };

  /** Reads what a previous page left in the outbox (merged with anything sent since), and wakes the restore effect. */
  function load() {
    const c = get();
    if (!c.outboxKey.current) return;
    const stored = readOutbox(c.outboxKey.current);
    c.outbox.current = [...stored.filter(entry => !c.outbox.current.some(item => item.clientMessageId === entry.clientMessageId)), ...c.outbox.current];
    c.bump();
  }

  /** Listens for the connection coming back: the browser's online event, and the live stream reconnecting (`connection`). */
  let wasConnected = true;
  window.addEventListener("online", autoRetry);
  function connection(connected: boolean) {
    if (connected && !wasConnected) autoRetry();
    wasConnected = connected;
  }

  return { load, connection, deliver, retrySend, deleteUnsent, editUnsent, autoRetry, restore, confirmed };
}
export type OutboxController = ReturnType<typeof createOutboxController>;
