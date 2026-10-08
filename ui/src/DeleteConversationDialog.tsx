import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ApiError } from "./api";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";

export type DeleteTarget = { id: string; conversationId: string; name: string; kind: "bot" | "group" };

export function DeleteConversationDialog({ target, onDelete, onClose }: { target: DeleteTarget; onDelete: (target: DeleteTarget) => Promise<void>; onClose: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const cancel = useRef<HTMLButtonElement>(null);
  const headingId = useId();
  const descriptionId = useId();
  useEffect(() => {
    const previous = document.activeElement;
    cancel.current?.focus();
    return () => { if (previous instanceof HTMLElement && previous.isConnected) previous.focus(); };
  }, []);

  async function remove() {
    if (busy) return;
    setBusy(true); setError("");
    try { await onDelete(target); onClose(); }
    catch (cause) {
      setError(cause instanceof ApiError && cause.code === "delete_busy" ? "还有关联任务正在执行。请先停止任务，再重试删除。" : cause instanceof Error ? cause.message : "删除失败，请重试。");
    } finally { setBusy(false); }
  }

  const label = target.kind === "bot" ? "删除 Bot" : "删除群聊";
  return createPortal(<div className="delete-dialog-overlay" onClick={event => { if (event.target === event.currentTarget && !busy) onClose(); }} onKeyDown={event => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); if (!busy) onClose(); }
    if (event.key === "Tab") {
      const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>("button:not(:disabled)"));
      if (!buttons.length) { event.preventDefault(); return; }
      const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      if (event.shiftKey && index <= 0 || !event.shiftKey && (index < 0 || index === buttons.length - 1)) { event.preventDefault(); (event.shiftKey ? buttons.at(-1) : buttons[0])?.focus(); }
    }
  }}><section className="delete-dialog" role="alertdialog" aria-modal="true" aria-labelledby={headingId} aria-describedby={descriptionId} aria-busy={busy}>
    <div className="delete-dialog-identity">{target.kind === "bot" ? <BotAvatar id={target.id} mini /> : <TofiIcon name="group" size={26} />}<span>{target.name}</span></div>
    <h2 id={headingId}>{label}？</h2>
    <p id={descriptionId}>{target.kind === "bot" ? "将删除这个 Bot、私信记录、记忆、待办和日程，并将其移出群聊。" : "将删除群内的聊天记录、记忆、待办和日程。群成员 Bot 会保留。"}</p>
    <p className="delete-dialog-note">{target.kind === "bot" ? "群里的历史发言和共享电脑文件会保留。" : "共享电脑文件会保留。"}删除后无法在应用中恢复。</p>
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="delete-dialog-actions"><button ref={cancel} type="button" className="secondary-button" disabled={busy} onClick={onClose}>取消</button><button type="button" className="delete-dialog-submit" disabled={busy} onClick={() => void remove()}>{busy ? "删除中…" : error ? "重试删除" : label}</button></div>
  </section></div>, document.body);
}
