import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ApiError } from "./api";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";

export type DeleteTarget = { id: string; conversationId: string; name: string; kind: "bot" | "group" };

export function DeleteConversationDialog({ target, onDelete, onClose }: { target: DeleteTarget; onDelete: (target: DeleteTarget) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation("chat");
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
      setError(cause instanceof ApiError && cause.code === "delete_busy" ? t("apiError.delete_busy") : cause instanceof Error ? cause.message : t("deleteDialog.failed"));
    } finally { setBusy(false); }
  }

  const label = target.kind === "bot" ? t("header.delete_bot") : t("header.delete_group");
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
    <h2 id={headingId}>{target.kind === "bot" ? t("deleteDialog.title_bot") : t("deleteDialog.title_group")}</h2>
    <p id={descriptionId}>{target.kind === "bot" ? t("deleteDialog.body_bot") : t("deleteDialog.body_group")}</p>
    <p className="delete-dialog-note">{target.kind === "bot" ? t("deleteDialog.note_bot") : t("deleteDialog.note_group")}</p>
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="delete-dialog-actions"><button ref={cancel} type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("deleteDialog.cancel")}</button><button type="button" className="delete-dialog-submit" disabled={busy} onClick={() => void remove()}>{busy ? t("deleteDialog.deleting") : error ? t("deleteDialog.retry") : label}</button></div>
  </section></div>, document.body);
}
