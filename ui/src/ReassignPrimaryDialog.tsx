import { useId, useRef, useState } from "react";
import { BotAvatar } from "./BotAvatar";
import { DialogShell } from "./DialogShell";
import { useTranslation } from "./i18n";
import { otherActiveBots, PrimarySuccessorSelect } from "./PrimaryAssistant";
import type { Bot } from "./types";

/**
 * Hiding (archiving) the Primary Assistant asks for a successor first, the same
 * rule as deleting it. Rendered by the app only for that case.
 */
export function ReassignPrimaryDialog({ bot, bots, onConfirm, onClose, returnFocus }: { bot: Bot; bots: Bot[]; onConfirm: (successorId: string) => Promise<void>; onClose: () => void; returnFocus?: HTMLElement | null }) {
  const { t } = useTranslation("chat");
  const [successor, setSuccessor] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const cancel = useRef<HTMLButtonElement>(null);
  const headingId = useId();
  const descriptionId = useId();
  const candidates = otherActiveBots(bots, bot.id);
  async function confirm() {
    if (busy || !successor) return;
    setBusy(true); setError("");
    try { await onConfirm(successor); onClose(); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("primary.hide_failed")); }
    finally { setBusy(false); }
  }
  return <DialogShell busy={busy} onClose={onClose} headingId={headingId} descriptionId={descriptionId} initialFocus={cancel} returnFocus={returnFocus}>
    <div className="delete-dialog-identity"><BotAvatar id={bot.id} mini /><span>{bot.name}</span></div>
    <h2 id={headingId}>{t("primary.hide_title")}</h2>
    <p id={descriptionId}>{t("primary.hide_body")}</p>
    <PrimarySuccessorSelect bots={candidates} value={successor} onChange={setSuccessor} disabled={busy} />
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="delete-dialog-actions"><button ref={cancel} type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("deleteDialog.cancel")}</button><button type="button" className="primary-button" disabled={busy || !successor} onClick={() => void confirm()}>{busy ? t("primary.working") : t("primary.hide_confirm")}</button></div>
  </DialogShell>;
}
