import { useCallback, useEffect, useState } from "react";
import { AdminSheet } from "./AdminSheets";
import { request } from "./api";
import { useTranslation } from "./i18n";
import { intlLocale } from "./i18n/format";
import { useUserTimezone } from "./UserTimezone";
import "./remembered-approvals.css";

/** A grant made from "Approve and remember in this chat". Server record; no arguments, URL or credential. */
export interface RememberedApproval {
  id: string;
  conversation_id: string;
  bot_id: string;
  server: string;
  tool: string;
  target_kind: "identifier" | "exact";
  target_summary?: string;
  source_question_id: string;
  created_at: string;
  expires_at: string;
  use_count: number;
  last_used_at?: string;
}

export async function fetchRememberedApprovals(conversationId: string, signal?: AbortSignal) {
  const result = await request<{ grants: RememberedApproval[] }>(`/api/conversations/${encodeURIComponent(conversationId)}/remembered-approvals`, { signal });
  return result.grants ?? [];
}

/** The conversation's remembered approvals, each with Revoke. Opened from the conversation menu. */
export function RememberedApprovalsSheet({ conversationId, onClose }: { conversationId: string; onClose: () => void }) {
  const { t } = useTranslation("tasks");
  const { timezone } = useUserTimezone();
  const [grants, setGrants] = useState<RememberedApproval[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(async (signal?: AbortSignal) => {
    try { setGrants(await fetchRememberedApprovals(conversationId, signal)); setError(""); }
    catch (cause) { if (!signal?.aborted) setError(cause instanceof Error ? cause.message : t("remembered.load_failed")); }
  }, [conversationId, t]);
  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    const changed = () => void load();
    window.addEventListener("tofi:remembered-approvals-changed", changed);
    return () => { controller.abort(); window.removeEventListener("tofi:remembered-approvals-changed", changed); };
  }, [load]);
  async function revoke(id: string) {
    if (busy) return;
    setBusy(id); setError("");
    try {
      await request(`/api/conversations/${encodeURIComponent(conversationId)}/remembered-approvals/${encodeURIComponent(id)}`, { method: "DELETE" });
      setGrants(current => (current ?? []).filter(grant => grant.id !== id));
      window.dispatchEvent(new Event("tofi:remembered-approvals-changed"));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("remembered.revoke_failed"));
    } finally { setBusy(null); }
  }
  return <AdminSheet title={t("remembered.title")} onClose={onClose} locked={Boolean(busy)} className="remembered-sheet">
    <p className="admin-sheet-note">{t("remembered.lead")}</p>
    {grants === null && !error && <p className="admin-sheet-note" aria-live="polite">{t("remembered.loading")}</p>}
    {grants !== null && grants.length === 0 && <p className="remembered-empty">{t("remembered.empty")}</p>}
    {grants !== null && grants.length > 0 && <ul className="remembered-list" aria-label={t("remembered.title")}>
      {grants.map(grant => <li key={grant.id} className="remembered-item" data-grant-id={grant.id}>
        <div className="remembered-main">
          <strong><code>{grant.tool}</code><span className="remembered-server">{grant.server}</span></strong>
          <span className="remembered-target">{grant.target_kind === "identifier" && grant.target_summary ? t("remembered.target_identifier", { target: grant.target_summary }) : t("remembered.target_exact")}</span>
          <small className="remembered-meta">{t("remembered.expires", { time: new Intl.DateTimeFormat(intlLocale(), { timeZone: timezone, dateStyle: "medium", timeStyle: "short" }).format(new Date(grant.expires_at)) })}{grant.use_count > 0 ? ` · ${t("remembered.used", { count: grant.use_count })}` : ""}</small>
        </div>
        <button type="button" className="secondary-button remembered-revoke" disabled={Boolean(busy)} onClick={() => void revoke(grant.id)}>{busy === grant.id ? t("remembered.revoking") : t("remembered.revoke")}</button>
      </li>)}
    </ul>}
    {error && <p role="alert" className="admin-error">{error}</p>}
    <div className="sheet-actions">
      <button type="button" className="secondary-button" disabled={Boolean(busy)} onClick={onClose}>{t("remembered.close")}</button>
    </div>
  </AdminSheet>;
}
