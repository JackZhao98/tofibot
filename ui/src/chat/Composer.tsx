import { useTranslation } from "../i18n";
import { textareaCaretRect } from "../textareaCaret";
import { DelayedFeedback } from "../DelayedFeedback";
import { isDesktop } from "../desktop";
import { fileKind } from "../filePreviewModel";
import { PermissionCoach, ComposerStopMenu, panelLoaded, preloadPanel } from "../lazyPanels";
import { arrowOpens, OverlayPopover, OverlayMenu, pressToggle } from "../overlays/OverlayHost";
import { TofiIcon as Icon } from "../icons";
import { ComposerGlyph } from "../ComposerGlyph";
import { lazy, startTransition, Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { errorText } from "../errorText";
import { api } from "../api";
import { BotAvatar } from "../BotAvatar";
import { ModelBanner } from "../OnboardingChrome";
import { FileTile } from "../FileTile";
import { layerRoot } from "../layers";
import { composerClientMessageId, composerMessageContent, composerMessageSignature, type ComposerReply } from "../composerDraft";
import { useDictation } from "../useDictation";
import type { Bot, Conversation, Run } from "../types";
import { MOTION, EASE } from "../motion";
import { messageId } from "./shared";
import { IconButton } from "../ds/IconButton/IconButton";

const WebFileDropOverlay = lazy(() => import("../WebFileDropOverlay").then(module => ({ default: module.WebFileDropOverlay })));
export type ComposerAttachment = {
  key: string;
  id?: string;
  name: string;
  size: number;
  lastModified: number;
  type: string;
  file?: File;
};
export type ComposerDraft = {
  content: string;
  attachments: ComposerAttachment[];
  reply?: ComposerReply;
  pending?: { signature: string; id: string };
};
export function DictationControls({ elapsed, levels, busy, onCancel, onConfirm }: { elapsed: number; levels: number[]; busy: boolean; onCancel: () => void; onConfirm: () => void }) {
  const { t } = useTranslation("chat");
  const clock = `${Math.floor(elapsed / 60)}:${String(elapsed % 60).padStart(2, "0")}`;
  return <div className={`dictation-controls${busy ? " is-busy" : ""}`} aria-label={busy ? t("dictation.transcribing_label") : t("dictation.recording")}>
    <button type="button" className="dictation-cancel" aria-label={busy ? t("dictation.cancel_transcription") : t("dictation.cancel_recording")} onClick={onCancel}><Icon name="close" size={18} /></button>
    <span className="dictation-record-dot" aria-hidden="true" />
    <time className="dictation-timer" aria-label={t("dictation.timer", { time: clock })}>{clock}</time>
    {busy ? <span className="dictation-processing" role="status">{t("dictation.transcribing")}</span> : <div className="dictation-wave" aria-hidden="true">{levels.map((height, index) => <i key={index} className={index < 12 ? "is-old" : ""} style={{ height: `${height}px` }} />)}</div>}
    <button type="button" className="dictation-confirm" aria-label={t("dictation.finish")} disabled={busy} onClick={onConfirm}><Icon name="check" size={24} variant="filled" /></button>
  </div>;
}
function composerMentionParts(content: string, names: string[]) {
  const ordered = [...names].sort((a, b) => b.length - a.length);
  const parts: React.ReactNode[] = [];
  let plainStart = 0;
  for (let index = 0; index < content.length; index += 1) {
    if (content[index] !== "@" || (index > 0 && !/\s/.test(content[index - 1]))) continue;
    const name = ordered.find((candidate) => content.startsWith(candidate, index + 1) && (index + candidate.length + 1 === content.length || /[\s,\uFF0C\u3002!?\uFF1F;\uFF1B:\uFF1A]/.test(content[index + candidate.length + 1])));
    if (!name) continue;
    if (plainStart < index) parts.push(content.slice(plainStart, index));
    parts.push(<span key={index} className="composer-at-chip">@{name}</span>);
    index += name.length;
    plainStart = index + 1;
  }
  if (plainStart < content.length) parts.push(content.slice(plainStart));
  return parts.length > 0 && parts.some((part) => typeof part !== "string") ? parts : null;
}
/** True when `next` is `previous` with text inserted somewhere and nothing removed or replaced. */
function isPureInsertion(previous: string, next: string) {
  if (next.length < previous.length) return false;
  let start = 0;
  while (start < previous.length && previous[start] === next[start]) start++;
  let end = 0;
  while (end < previous.length - start && previous[previous.length - 1 - end] === next[next.length - 1 - end]) end++;
  return start + end >= previous.length;
}
/** The chip's leading tile: a picture's own thumbnail when the file is still here, the family tile otherwise. */
function ComposerChipTile({ attachment }: { attachment: { name: string; type: string; file?: File } }) {
  const kind = fileKind(attachment.name, attachment.type);
  const [thumb, setThumb] = useState<string>();
  useEffect(() => {
    if (kind !== "image" || !attachment.file) { setThumb(undefined); return; }
    const url = URL.createObjectURL(attachment.file);
    setThumb(url);
    return () => URL.revokeObjectURL(url);
  }, [kind, attachment.file]);
  return <span className="composer-attachment-type" aria-hidden="true"><FileTile name={attachment.name} kind={kind} thumb={thumb} size={28} /></span>;
}
export function Composer({ workStatus, activeRun, allRuns, stepRunIds, latestReplyId, onRunsStopped, onStopError, avatarMention, conversation, bots, draft, onDraftChange: updateDraftFor, onSend, onSendStart, onSent, modelConfigured, dictationReady, onConnectModel, draftReady, readOnly = false }: { workStatus: React.ReactNode; activeRun?: Run; /** Every run still working in this conversation (several Bots can work in a group). */ allRuns: Run[]; stepRunIds: string[]; latestReplyId?: string; onRunsStopped: (runs: Run[]) => void; onStopError: (message: string) => void; avatarMention: { name: string; conversationId: string; nonce: number } | null; conversation: Conversation; bots: Bot[]; draft: ComposerDraft; onDraftChange: (conversationId: string, update: (draft: ComposerDraft) => ComposerDraft) => void; onSend: (content: string, clientMessageId: string, files?: ComposerAttachment[], draftSignature?: string, plain?: string, reply?: ComposerReply) => Promise<boolean>; onSendStart?: () => void; onSent: () => void; modelConfigured: boolean; dictationReady: boolean; onConnectModel: () => void; draftReady: boolean; readOnly?: boolean }) {
  const { t } = useTranslation("chat");
  const [sending, setSending] = useState(false);
  const [fileError, setFileError] = useState("");
  const fileRef = useRef<HTMLInputElement | null>(null);
  const imageRef = useRef<HTMLInputElement | null>(null);
  const addMenuRef = useRef<HTMLDivElement | null>(null);
  const addMenuTriggerRef = useRef<HTMLButtonElement | null>(null);
  const [addMenuOpen, setAddMenuOpen] = useState(false);
  const [stopMenuOpen, setStopMenuOpen] = useState(false);
  const stopTriggerRef = useRef<HTMLButtonElement | null>(null);
  const working = () => allRuns.filter(run => run.status === "running" || run.status === "queued" || run.status === "waiting");
  // Stop all by itself: the button does this when the menu chunk is not in memory.
  async function stopWorkingRuns() {
    const results = await Promise.allSettled(working().map(run => api.cancelRun(run.id)));
    onRunsStopped(results.flatMap(result => result.status === "fulfilled" ? [result.value] : []));
    const failure = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
    onStopError(failure ? errorText(failure.reason) : "");
  }
  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  const [mentionStart, setMentionStart] = useState(-1);
  const [mentionIndex, setMentionIndex] = useState(0);
  const mentionMenuRef = useRef<HTMLDivElement | null>(null);
  const mentionWrapRef = useRef<HTMLDivElement | null>(null);
  const [mentionPill, setMentionPill] = useState<{ top: number; height: number } | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const conversationIdRef = useRef(conversation.id);
  const operationRef = useRef(0);
  const mentionBots = conversation.kind === "group" ? conversation.bot_ids.map((id) => bots.find((bot) => bot.id === id)).filter((bot): bot is Bot => Boolean(bot)) : bots.filter((bot) => !bot.archived && bot.id !== conversation.bot_id);
  const mentionMatches = mentionQuery === null ? [] : mentionBots.filter((bot) => bot.name.toLocaleLowerCase().startsWith(mentionQuery.toLocaleLowerCase()));
  // The text being typed lives here, not in App: a keystroke re-renders only the composer. App's draft store
  // receives it debounced and on blur / send / conversation switch / page hide (see flushContent).
  const [typed, setTyped] = useState({ id: conversation.id, text: draft.content, synced: draft.content });
  let typedNow = typed;
  if (typed.id !== conversation.id) typedNow = { id: conversation.id, text: draft.content, synced: draft.content };
  // Content changed in App by something other than this composer (restored draft, send acknowledgement): adopt it.
  else if (draft.content !== typed.synced) typedNow = { ...typed, text: draft.content, synced: draft.content };
  if (typedNow !== typed) setTyped(typedNow);
  const typedRef = useRef(typedNow);
  typedRef.current = typedNow;
  const unsyncedRef = useRef<{ id: string; text: string } | null>(null);
  const flushTimer = useRef<number | undefined>(undefined);
  const updateDraftForRef = useRef(updateDraftFor);
  updateDraftForRef.current = updateDraftFor;
  // Bound to this render's conversation: an upload that finishes after a switch must edit the draft it started in.
  const onDraftChange = (update: (draft: ComposerDraft) => ComposerDraft) => updateDraftFor(conversation.id, update);
  const flushContent = useCallback((background = false) => {
    window.clearTimeout(flushTimer.current);
    const pending = unsyncedRef.current;
    if (!pending) return;
    unsyncedRef.current = null;
    // `synced` and App's draft must change in the same render, or the composer would take its own flush for an outside edit.
    const push = () => {
      updateDraftForRef.current(pending.id, (current) => current.content === pending.text ? current : { ...current, content: pending.text });
      if (typedRef.current.id === pending.id) setTyped((current) => current.id === pending.id ? { ...current, synced: pending.text } : current);
    };
    // An idle-time flush may yield to the next keystroke; the forced ones (blur, send, switch, hide) may not.
    if (background) startTransition(push); else push();
  }, []);
  // Typed or dictated text; `now` commits to the draft store immediately.
  const setContent = useCallback((text: string, now = false) => {
    const id = conversationIdRef.current;
    unsyncedRef.current = { id, text };
    setTyped((current) => current.id === id ? { ...current, text } : current);
    typedRef.current = { ...typedRef.current, text };
    window.clearTimeout(flushTimer.current);
    if (now) flushContent(); else flushTimer.current = window.setTimeout(() => flushContent(true), 400);
  }, [flushContent]);
  useLayoutEffect(() => {
    const id = conversation.id;
    return () => { if (unsyncedRef.current?.id === id) flushContent(); };
  }, [conversation.id, flushContent]);
  useEffect(() => {
    const hide = () => flushContent();
    const visibility = () => { if (document.visibilityState === "hidden") flushContent(); };
    window.addEventListener("pagehide", hide);
    document.addEventListener("visibilitychange", visibility);
    return () => { window.removeEventListener("pagehide", hide); document.removeEventListener("visibilitychange", visibility); };
  }, [flushContent]);
  const content = typedNow.text;
  const reply = draft.reply;
  const mentionParts = composerMentionParts(content, mentionBots.map((bot) => bot.name));
  const [composerScrollTop, setComposerScrollTop] = useState(0);
  const attachments = draft.attachments;
  const canCompose = modelConfigured && draftReady && !readOnly;
  // Radix reads the live caret only while mounted; a closed mention does no layout work.
  const mentionAnchor = useMemo(() => ({ current: mentionQuery === null ? null : {
    getBoundingClientRect: () => textareaRef.current ? textareaCaretRect(textareaRef.current, mentionStart) : new DOMRect(),
  } }), [mentionQuery, mentionStart, content, composerScrollTop]);
  const selectedMention = useCallback((button: HTMLButtonElement | null) => {
    if (!button) return;
    mentionMenuRef.current = button.parentElement as HTMLDivElement;
    const top = button.offsetTop, height = button.offsetHeight;
    setMentionPill(previous => previous?.top === top && previous.height === height ? previous : { top, height });
  }, []);
  const dictation = useDictation({
    value: content,
    textareaRef,
    disabled: !canCompose || sending,
    onChange: (value) => { setContent(value, true); setMentionQuery(null); },
  });
  useEffect(() => { dictation.cancel(); }, [conversation.id, dictation.cancel]);
  useEffect(() => {
    if (!avatarMention || avatarMention.conversationId !== conversation.id || readOnly) return;
    const textarea = textareaRef.current;
    const text = typedRef.current.text;
    const start = textarea?.selectionStart ?? text.length;
    const end = textarea?.selectionEnd ?? start;
    const prefix = start > 0 && !/\s/.test(text[start - 1]) ? " " : "";
    const mention = `${prefix}@${avatarMention.name} `;
    setContent(text.slice(0, start) + mention + text.slice(end), true);
    setMentionQuery(null);
    const frame = requestAnimationFrame(() => {
      textarea?.focus();
      textarea?.setSelectionRange(start + mention.length, start + mention.length);
    });
    return () => cancelAnimationFrame(frame);
    // A request is consumed once, never replayed when switching conversations.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [avatarMention]);

  // Keep stale async sends from touching the newly selected conversation even
  // during the small render-to-effect window after a switch.
  conversationIdRef.current = conversation.id;
  useEffect(() => {
    conversationIdRef.current = conversation.id;
    operationRef.current += 1;
    setSending(false);
    setFileError("");
    setMentionQuery(null);
    setMentionIndex(0);
    setAddMenuOpen(false);
    return () => { operationRef.current += 1; };
  }, [conversation.id]);
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if (!canCompose || sending || !event.metaKey || !event.shiftKey) return;
      if (event.key.toLowerCase() === "i") { event.preventDefault(); imageRef.current?.click(); }
      if (event.key.toLowerCase() === "f") { event.preventDefault(); fileRef.current?.click(); }
    };
    document.addEventListener("keydown", shortcut);
    return () => document.removeEventListener("keydown", shortcut);
  }, [canCompose, sending]);
  const fittedRef = useRef<{ id: string; text: string } | null>(null);
  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    const previous = fittedRef.current;
    fittedRef.current = { id: conversation.id, text: content };
    // Typing only adds text, and added text never makes the wrapped content shorter. If it still fits the current
    // box there is nothing to resize, so skip the reset-to-auto that costs an extra forced layout on every key.
    if (previous && previous.id === conversation.id && textarea.style.height && isPureInsertion(previous.text, content)) {
      const fits = textarea.scrollHeight <= textarea.clientHeight;
      if (fits || (textarea.clientHeight >= 120 && textarea.style.overflowY === "auto")) return;
    }
    // One forced layout: the content height is read once and reused for the overflow decision.
    textarea.style.height = "auto";
    const contentHeight = textarea.scrollHeight;
    textarea.style.height = `${Math.max(48, Math.min(contentHeight, 120))}px`;
    textarea.style.overflowY = contentHeight > 120 ? "auto" : "hidden";
  }, [content, conversation.id]);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if ((!content.trim() && !attachments.length) || !canCompose || sending || dictation.listening || dictation.busy) return;
    const conversationId = conversation.id;
    const operation = ++operationRef.current;
    const isCurrent = () => operationRef.current === operation && conversationIdRef.current === conversationId;
    // A reply carries the same leading marker bots use, so the quote renders and the model sees the target id.
    const draftContent = composerMessageContent(content, reply);
    const signature = composerMessageSignature(content, attachments, reply);
    const clientMessageId = composerClientMessageId(signature, draft.pending, messageId);
    // A file restored after a reload has lost its bytes: say which one to pick again, before anything leaves.
    const lost = attachments.find((attachment) => !attachment.id && !attachment.file);
    if (lost) { setFileError(t("composer.reselect_file", { name: lost.name })); return; }
    flushContent();
    // The list is at the newest message before anything else happens; the bubble then arrives into view.
    onSendStart?.();
    onDraftChange((current) => ({ ...current, pending: { signature, id: clientMessageId } }));
    setSending(true);
    // The message is on screen and the composer is empty when this settles; files go up from the bubble, not from here.
    const sent = await onSend(draftContent, clientMessageId, attachments, signature, content.trim(), reply);
    if (!isCurrent()) return;
    // Workspace owns acknowledgement cleanup for the exact sent draft, even
    // across conversation switches. A newer edit/reply must survive this callback.
    if (sent) { setFileError(""); onSent(); }
    setSending(false);
  }
  function handleKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Escape" && mentionQuery !== null) { event.preventDefault(); setMentionQuery(null); return; }
    if (mentionQuery !== null && mentionMatches.length > 0 && (event.key === "ArrowDown" || event.key === "ArrowUp")) { event.preventDefault(); setMentionIndex((current) => (current + (event.key === "ArrowDown" ? 1 : mentionMatches.length - 1)) % mentionMatches.length); return; }
    if (mentionQuery !== null && mentionMatches.length > 0 && event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) { event.preventDefault(); chooseMention(mentionMatches[mentionIndex]); return; }
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing || event.keyCode === 229) return;
    event.preventDefault();
    event.currentTarget.form?.requestSubmit();
  }
  function chooseMention(bot: Bot) {
    if (mentionStart < 0 || mentionQuery === null) return;
    const fromIndex = mentionMatches.findIndex(candidate => candidate.id === bot.id);
    const from = mentionMenuRef.current?.querySelectorAll<HTMLElement>("button[role=option] .mention-name")[fromIndex]?.getBoundingClientRect();
    const textarea = textareaRef.current;
    const cursor = textarea?.selectionStart ?? content.length;
    const before = content.slice(0, mentionStart);
    const after = content.slice(cursor);
    const next = `${before}@${bot.name} ${after}`;
    setContent(next, true);
    setMentionQuery(null); setMentionIndex(0);
    requestAnimationFrame(() => {
      const position = before.length + bot.name.length + 2;
      textareaRef.current?.setSelectionRange(position, position); textareaRef.current?.focus();
      const chip = Array.from(mentionWrapRef.current?.querySelectorAll<HTMLElement>(".composer-at-chip") ?? []).find(node => node.textContent === `@${bot.name}`);
      if (!from || !chip || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
      const to = chip.getBoundingClientRect();
      const ghost = document.createElement("span");
      ghost.className = "web-mention-ghost";
      ghost.textContent = `@${bot.name}`;
      Object.assign(ghost.style, { left: `${from.left}px`, top: `${from.top}px` });
      layerRoot().append(ghost);
      void ghost.animate([
        { transform: "translate(0,0) scale(.9)", opacity: 1 },
        { transform: `translate(${(to.left - from.left) * .5}px, ${Math.min(from.top,to.top) - from.top - 38}px) scale(1.08)`, opacity: 1, offset:.45 },
        { transform: `translate(${to.left - from.left}px, ${to.top - from.top}px) scale(1)`, opacity: 0 },
      ], { duration:MOTION.panelIn, easing: EASE.out }).finished.finally(() => ghost.remove());
    });
  }
  function updateContent(value: string, cursor: number) {
    setContent(value);
    const prefix = value.slice(0, cursor); const match = prefix.match(/(?:^|\s)@([^\s@]*)$/);
    if (!match) { setMentionQuery(null); return; }
    setMentionStart(cursor - match[1].length - 1); setMentionQuery(match[1]); setMentionIndex(0);
  }
  function addFiles(list: FileList | File[] | null) {
    if (!list || sending || !canCompose) return;
    setFileError("");
    const next = [...attachments];
    for (const file of Array.from(list)) {
      if (file.size > 20 * 1024 * 1024) { setFileError(t("composer.too_large", { name: file.name })); continue; }
      const placeholder = next.findIndex((attachment) => !attachment.file && !attachment.id && attachment.name === file.name && attachment.size === file.size && attachment.lastModified === file.lastModified);
      if (placeholder < 0 && next.length >= 8) { setFileError(t("composer.max_files")); break; }
      const replacement = { key: placeholder >= 0 ? next[placeholder].key : messageId(), file, name: file.name, size: file.size, lastModified: file.lastModified, type: file.type };
      if (placeholder >= 0) next[placeholder] = replacement;
      else next.push(replacement);
    }
    onDraftChange((current) => ({ ...current, attachments: next }));
  }
  async function captureScreen() {
    setAddMenuOpen(false);
    if (!navigator.mediaDevices?.getDisplayMedia) {
      setFileError(t("composer.screenshot_unsupported"));
      return;
    }
    let stream: MediaStream | undefined;
    try {
      stream = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false });
      const video = document.createElement("video");
      video.srcObject = stream;
      await video.play();
      if (!video.videoWidth || !video.videoHeight) throw new Error(t("composer.screenshot_read_failed"));
      const canvas = document.createElement("canvas");
      canvas.width = video.videoWidth;
      canvas.height = video.videoHeight;
      canvas.getContext("2d")?.drawImage(video, 0, 0);
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/png"));
      if (!blob) throw new Error(t("composer.screenshot_failed"));
      addFiles([new File([blob], t("composer.screenshot_name", { time: new Date().toISOString().replace(/[:.]/g, "-") }), { type: "image/png" })]);
    } catch (cause) {
      if ((cause as DOMException)?.name !== "NotAllowedError") setFileError(errorText(cause));
    } finally {
      stream?.getTracks().forEach((track) => track.stop());
    }
  }
  // Measured before paint: the message pane's bottom padding follows the composer, so a late (post-paint)
  // measurement moved the whole conversation by the composer's height right after every switch.
  useLayoutEffect(() => {
    const form = textareaRef.current?.closest("form");
    // Measured on the workspace so the floating desktop, mounted outside the chat pane, docks above the composer too.
    const pane = form?.closest<HTMLElement>(".workspace") ?? form?.closest<HTMLElement>(".chat-pane");
    if (!form || !pane) return;
    // The header height too: on the web the message pane runs up under the header's frosted glass (the top ProgressiveBlur band, conversation-workspace.css).
    const header = pane.querySelector<HTMLElement>(".chat-header");
    const measure = () => {
      pane.style.setProperty("--composer-height", `${form.getBoundingClientRect().height}px`);
      if (header) pane.style.setProperty("--chat-header-height", `${header.getBoundingClientRect().height}px`);
    };
    const observer = new ResizeObserver(measure);
    observer.observe(form, { box: "border-box" }); if (header) observer.observe(header, { box: "border-box" }); measure();
    return () => observer.disconnect();
  }, [conversation.id]);
  // The measurement stays across a conversation switch (the next composer re-measures in the same commit) and goes with the composer.
  useEffect(() => () => { const workspace = document.querySelector<HTMLElement>(".workspace"); workspace?.style.removeProperty("--composer-height"); workspace?.style.removeProperty("--chat-header-height"); }, []);
  return <form className={`composer${!modelConfigured ? " is-no-model" : ""}${!content.trim() && !attachments.length && !sending ? " is-send-empty" : ""}`} onSubmit={(event) => void submit(event)}>{readOnly ? <div className="composer-tools"><span className="config-warning">{t("composer.archived")}</span></div> : !draftReady ? <DelayedFeedback><div className="composer-tools"><span className="config-warning">{t("composer.restoring_draft")}</span></div></DelayedFeedback> : !modelConfigured && <ModelBanner name={conversation.name} onConnect={onConnectModel} />}{dictation.permissionNeeded && <Suspense fallback={null}><PermissionCoach permissions={["microphone"]} force onDismiss={dictation.clearPermission} /></Suspense>}{!isDesktop && dictation.starting && <div className="dictation-status is-starting" role="status">{t("composer.mic_connecting")}</div>}{dictation.error && !dictation.permissionNeeded && <div className="dictation-status" role="status">{dictation.error}</div>}<input ref={imageRef} type="file" accept="image/*" hidden multiple onChange={(event) => { addFiles(event.target.files); event.currentTarget.value = ""; }} /><input ref={fileRef} type="file" hidden multiple onChange={(event) => { addFiles(event.target.files); event.currentTarget.value = ""; }} />{workStatus}<div className="composer-row">{<Suspense fallback={null}><WebFileDropOverlay key={`drop-${conversation.id}`} inputRef={textareaRef} enabled={canCompose && !sending && !dictation.listening && !dictation.busy} onDrop={files => addFiles(files)} /></Suspense>}{!(dictation.listening || dictation.busy) && <div className="composer-add-wrap" ref={addMenuRef}><IconButton ref={addMenuTriggerRef} variant="solid" icon="plus" label={t("composer.add_attachment")} className="composer-add" aria-expanded={addMenuOpen} aria-haspopup="menu" disabled={sending || !canCompose} {...pressToggle(() => setAddMenuOpen(open => !open))} onKeyDown={arrowOpens(setAddMenuOpen)} />{addMenuOpen && <OverlayMenu open onOpenChange={setAddMenuOpen} className="composer-add-menu" label={t("composer.add_attachment")} trigger={addMenuTriggerRef} anchor={addMenuRef} side="top" sideOffset={12}><button type="button" onClick={() => { setAddMenuOpen(false); imageRef.current?.click(); }}><Icon className="composer-add-icon" name="image" size={20} />{t("composer.image")}<kbd>⌘⇧I</kbd></button><button type="button" onClick={() => { setAddMenuOpen(false); fileRef.current?.click(); }}><Icon className="composer-add-icon" name="file" size={20} />{t("composer.file")}<kbd>⌘⇧F</kbd></button><button type="button" onClick={() => void captureScreen()}><span className="composer-add-icon"><ComposerGlyph name="screenshot" size={20} /></span>{t("composer.screenshot")}</button><p>{t("composer.drop_hint")}</p></OverlayMenu>}</div>}<div ref={mentionWrapRef} className={`composer-input-wrap${mentionParts ? " has-mentions" : ""}`}>{reply && !readOnly && <div className="composer-reply" role="status"><Icon name="reply" size={15} /><span className="composer-reply-copy"><strong>{t("composer.replying_to", { name: reply.name })}</strong><span>{reply.excerpt}</span></span><button type="button" aria-label={t("composer.cancel_reply")} data-hint={t("composer.cancel_reply")} onClick={() => { onDraftChange(current => ({ ...current, reply: undefined })); textareaRef.current?.focus(); }}><Icon name="close" size={14} /></button></div>}{attachments.length > 0 && <div className="composer-attachments">{attachments.map((attachment) => <span key={attachment.key} className="composer-attachment-pill" data-name={attachment.name}><ComposerChipTile attachment={attachment} /><span className="composer-attachment-name">{attachment.name}</span>{!attachment.file && !attachment.id && ` · ${t("composer.reselect")}`}<button type="button" aria-label={t("composer.remove_attachment", { name: attachment.name })} disabled={sending || !canCompose || dictation.listening || dictation.busy} onClick={() => onDraftChange((current) => ({ ...current, attachments: current.attachments.filter((item) => item.key !== attachment.key) }))}><Icon name="close" size={18} /></button></span>)}</div>}{mentionParts && <div className="composer-rich-text" aria-hidden="true"><div style={{ transform:`translateY(-${composerScrollTop}px)` }}>{mentionParts}</div></div>}<textarea ref={textareaRef} aria-controls={mentionQuery !== null && mentionMatches.length ? `mention-${conversation.id}` : undefined} aria-activedescendant={mentionQuery !== null && mentionMatches[mentionIndex] ? `mention-${conversation.id}-${mentionMatches[mentionIndex].id}` : undefined} aria-label={t("composer.input_label", { name: conversation.name })} value={content} onPaste={event => { const files = Array.from(event.clipboardData.items).filter(item => item.kind === "file").map(item => item.getAsFile()).filter((file): file is File => file !== null); if (files.length) { event.preventDefault(); addFiles(files); } }} onKeyDown={handleKeyDown} onScroll={(event) => setComposerScrollTop(event.currentTarget.scrollTop)} onBlur={() => flushContent()} onChange={(event) => updateContent(event.target.value, event.target.selectionStart)} placeholder={dictation.listening ? t("composer.placeholder_recording") : dictation.busy ? t("composer.placeholder_transcribing") : readOnly ? t("composer.placeholder_archived") : !draftReady ? t("composer.placeholder", { name: conversation.name }) : canCompose ? t("composer.placeholder", { name: conversation.name }) : t("composer.placeholder_no_model")} disabled={!canCompose || sending || dictation.listening || dictation.busy} rows={1} />{mentionQuery !== null && mentionMatches.length > 0 && <OverlayPopover open onOpenChange={open => { if (!open) setMentionQuery(null); }} anchor={mentionAnchor} side="top" align="start" sideOffset={8} takeFocus={false} id={`mention-${conversation.id}`} className="mention-menu" role="listbox" aria-label={t("composer.mention_label", { name: conversation.name })}><div className="mention-menu-heading" role="presentation">{t("composer.mention_heading", { name: conversation.name })}</div>{mentionPill && <span className="web-mention-pill" aria-hidden="true" style={{ top:mentionPill.top, height:mentionPill.height }} />}{mentionMatches.map((bot, index) => <button ref={index === mentionIndex ? selectedMention : undefined} id={`mention-${conversation.id}-${bot.id}`} type="button" role="option" aria-selected={index === mentionIndex} className={index === mentionIndex ? "selected" : ""} key={bot.id} onMouseEnter={() => setMentionIndex(index)} onMouseDown={(event) => { event.preventDefault(); chooseMention(bot); }}><BotAvatar id={bot.id} mini /><span className="mention-name">{mentionQuery && bot.name.toLocaleLowerCase().startsWith(mentionQuery.toLocaleLowerCase()) ? <><mark>{bot.name.slice(0, mentionQuery.length)}</mark>{bot.name.slice(mentionQuery.length)}</> : bot.name}</span></button>)}<div className="mention-menu-hint" role="presentation">{t("composer.mention_hint")}</div></OverlayPopover>}</div>{dictation.listening || dictation.busy ? <DictationControls elapsed={dictation.elapsed} levels={dictation.levels} busy={dictation.busy} onCancel={dictation.cancel} onConfirm={dictation.confirm} /> : <>{dictation.supported && dictationReady && <button type="button" className={`dictation-button${!isDesktop && dictation.starting ? " is-starting" : ""}`} aria-label={!isDesktop && dictation.starting ? t("composer.cancel_mic") : t("composer.dictate")} title={!isDesktop && dictation.starting ? t("composer.cancel_mic") : dictation.notice || t("composer.dictate")} disabled={!dictation.starting && (sending || !canCompose)} onClick={!isDesktop && dictation.starting ? dictation.cancel : dictation.start}><ComposerGlyph name="mic" size={20} /></button>}{activeRun && !readOnly && !content.trim() && !attachments.length && <><button ref={stopTriggerRef} type="button" className="composer-stop" aria-label={t("composer.stop_label", { name: bots.find(bot => bot.id === activeRun.bot_id)?.name ?? "Bot" })} aria-haspopup="menu" aria-expanded={stopMenuOpen} disabled={!canCompose} onClick={() => { if (panelLoaded("stopMenu")) setStopMenuOpen(open => !open); else { preloadPanel("stopMenu"); void stopWorkingRuns(); } }} onKeyDown={arrowOpens(setStopMenuOpen)}><Icon name="stop" size={16} variant="filled" />{t("composer.stop")}</button>{stopMenuOpen && <Suspense fallback={null}><ComposerStopMenu runs={working()} stepRunIds={stepRunIds} bots={bots} trigger={stopTriggerRef} onClose={() => setStopMenuOpen(false)} onStopped={onRunsStopped} onError={onStopError} /></Suspense>}</>}{(Boolean(content.trim()) || attachments.length > 0 || sending) && <button className="send-button" aria-label={t("composer.send")} disabled={(!content.trim() && !attachments.length) || !canCompose || sending || dictation.listening || dictation.busy}>{sending ? "…" : <Icon name="arrow-up" size={26} variant="filled" />}</button>}</>}</div>{fileError && <div className="error-text" role="alert">{fileError}</div>}</form>;
}

export const emptyComposerDraft = (): ComposerDraft => ({ content: "", attachments: [] });

export type ComposerDrafts = Record<string, ComposerDraft>;
