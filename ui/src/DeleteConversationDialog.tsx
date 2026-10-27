import { useEffect, useId, useRef, useState } from "react";
import { api, ApiError } from "./api";
import { DialogShell } from "./DialogShell";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import { useHasComputer } from "./computerCapability";
import { PrimarySuccessorSelect } from "./PrimaryAssistant";
import type { Bot, TaskImpact } from "./types";

/** `isPrimary` marks the account's Primary Assistant: removing it needs a named successor. */
export type DeleteTarget = { id: string; conversationId: string; name: string; kind: "bot" | "group"; isPrimary?: boolean };

export function DeleteConversationDialog({ target, bots = [], onDelete, onClose, returnFocus }: { target: DeleteTarget; /** Every Bot; the successor choices are the other active ones. */ bots?: Bot[]; onDelete: (target: DeleteTarget, successorId?: string) => Promise<void>; onClose: () => void; returnFocus?: HTMLElement | null }) {
  const { t } = useTranslation("chat");
  const hasComputer = useHasComputer();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [successor, setSuccessor] = useState("");
  const [impact, setImpact] = useState<TaskImpact | null>(null);
  const cancel = useRef<HTMLButtonElement>(null);
  const headingId = useId();
  const descriptionId = useId();
  const candidates = bots.filter(bot => !bot.archived && bot.id !== target.id);
  // Deleting the Primary Assistant never leaves the role empty while another Bot exists.
  const needsSuccessor = target.kind === "bot" && Boolean(target.isPrimary) && candidates.length > 0;

  // Informational only: the delete button never waits for it, and a failed read shows nothing.
  useEffect(() => {
    if (target.kind !== "bot") return;
    const controller = new AbortController();
    api.botTaskImpact(target.id, undefined, controller.signal).then(result => { if (!controller.signal.aborted) setImpact(result); }).catch(() => {});
    return () => controller.abort();
  }, [target.kind, target.id]);

  async function remove() {
    if (busy || (needsSuccessor && !successor)) return;
    setBusy(true); setError("");
    try { await onDelete(target, needsSuccessor ? successor : undefined); onClose(); }
    catch (cause) {
      setError(cause instanceof ApiError && cause.code === "delete_busy" ? t("apiError.delete_busy") : cause instanceof ApiError && cause.code === "primary_reassign_required" ? t("primary.reassign_required") : cause instanceof Error ? cause.message : t("deleteDialog.failed"));
    } finally { setBusy(false); }
  }

  const label = target.kind === "bot" ? t("header.delete_bot") : t("header.delete_group");
  return <DialogShell busy={busy} onClose={onClose} headingId={headingId} descriptionId={descriptionId} initialFocus={cancel} returnFocus={returnFocus}>
    <div className="delete-dialog-identity">{target.kind === "bot" ? <BotAvatar id={target.id} mini /> : <TofiIcon name="group" size={26} />}<span>{target.name}</span></div>
    <h2 id={headingId}>{target.kind === "bot" ? t("deleteDialog.title_bot") : t("deleteDialog.title_group")}</h2>
    <p id={descriptionId}>{target.kind === "bot" ? t("deleteDialog.body_bot") : t("deleteDialog.body_group")}</p>
    <p className="delete-dialog-note">{target.kind === "bot" ? t(hasComputer ? "deleteDialog.note_bot" : "deleteDialog.note_bot_no_computer") : t(hasComputer ? "deleteDialog.note_group" : "deleteDialog.note_group_no_computer")}</p>
    {impact && impact.given_tasks + impact.held_tasks + impact.open_waits > 0 && <div className="delete-dialog-note" role="status">
      {impact.given_tasks > 0 && <p>{t("deleteDialog.impact_given", { count: impact.given_tasks })}</p>}
      {impact.held_tasks > 0 && <p>{t("deleteDialog.impact_held", { count: impact.held_tasks })}</p>}
      {impact.open_waits > 0 && <p>{t("deleteDialog.impact_waits", { count: impact.open_waits })}</p>}
    </div>}
    {needsSuccessor && <><p className="delete-dialog-primary">{t("primary.delete_notice")}</p><PrimarySuccessorSelect bots={candidates} value={successor} onChange={setSuccessor} disabled={busy} /></>}
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="delete-dialog-actions"><button ref={cancel} type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("deleteDialog.cancel")}</button><button type="button" className="delete-dialog-submit" disabled={busy || (needsSuccessor && !successor)} onClick={() => void remove()}>{busy ? t("deleteDialog.deleting") : error ? t("deleteDialog.retry") : label}</button></div>
  </DialogShell>;
}
