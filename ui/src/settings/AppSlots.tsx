import { useEffect, useRef, useState } from "react";
import { api, request } from "../api";
import { errorText } from "../errorText";
import { intlLocale } from "../i18n/format";
import { useTranslation } from "../i18n";
import { TofiIcon as Icon } from "../icons";
import { StatusBadge } from "./components";

const AUTO_REVIEW_DEFAULTED = "tofi:auto-review-defaulted";
/**
 * Connecting Codex makes Auto review the reviewer, once per browser: only a setting still at Off is changed, and a
 * later choice (including Off) is never overwritten. Failures are silent; the Permissions page shows the real state.
 */
async function defaultAutoReview() {
  try {
    if (localStorage.getItem(AUTO_REVIEW_DEFAULTED)) return;
    const current = await request<{ mode: string }>("/api/auto-review-settings");
    if (current.mode === "off") await request("/api/auto-review-settings", { method: "PUT", body: JSON.stringify({ mode: "auto" }) });
    localStorage.setItem(AUTO_REVIEW_DEFAULTED, "1");
  } catch { /* keep the current mode */ }
}

export function CodexPanel({ refreshToken, onConfigured }: { refreshToken: number; onConfigured: () => void }) {
  const { t } = useTranslation("chat");
  const [connected, setConnected] = useState<boolean | null>(null);
  const [expiresAt, setExpiresAt] = useState<number | undefined>();
  const [session, setSession] = useState<{ id: string; url: string; code: string; expiresAt: number } | null>(null);
  const [working, setWorking] = useState(true);
  const [statusRefresh, setStatusRefresh] = useState(0);
  const [error, setError] = useState("");
  const statusRequest = useRef(0);
  const [needsReconnect, setNeedsReconnect] = useState(false);
  const [check, setCheck] = useState<"" | "checking" | "ok" | "unverified">("");
  useEffect(() => {
    const request = ++statusRequest.current;
    setWorking(true); setConnected(null); setExpiresAt(undefined); setError(""); setNeedsReconnect(false); setCheck("");
    api.codexStatus().then(async (status) => {
      if (request !== statusRequest.current) return;
      setConnected(status.connected); setExpiresAt(status.expires_at); setNeedsReconnect(Boolean(status.needs_reconnect));
      if (!status.connected) return;
      // A stored credential is not proof the provider still accepts it.
      setCheck("checking");
      const verified = await api.codexVerify().catch(() => null);
      if (request !== statusRequest.current) return;
      if (!verified) { setCheck("unverified"); return; }
      setConnected(verified.connected); setExpiresAt(verified.expires_at); setNeedsReconnect(Boolean(verified.needs_reconnect));
      setCheck(verified.check === "ok" ? "ok" : verified.check === "unverified" ? "unverified" : "");
      if (verified.check === "rejected") onConfigured();
    }).catch((cause) => { if (request === statusRequest.current) setError(errorText(cause)); }).finally(() => { if (request === statusRequest.current) setWorking(false); });
  }, [refreshToken, statusRefresh]);
  async function connect() {
    ++statusRequest.current;
    setWorking(true); setError("");
    try {
      const result = await api.codexConnect();
      const next = { id: result.session_id, url: result.verification_url, code: result.user_code, expiresAt: result.expires_at };
      setSession(next);
      const expires = result.expires_at;
      const delay = Math.max(1000, result.interval * 1000);
      while (Date.now() < expires) {
        await new Promise((resolve) => window.setTimeout(resolve, delay));
        const status = await api.codexPoll(result.session_id);
        if (status.connected) { setConnected(true); setNeedsReconnect(false); setExpiresAt(status.expires_at); setSession(null); await defaultAutoReview(); onConfigured(); return; }
        if (!status.pending) break;
      }
      setError(t("codex.session_expired"));
    } catch (cause) { setError(errorText(cause)); }
    finally { setWorking(false); }
  }
  async function disconnect() { ++statusRequest.current; setWorking(true); setError(""); try { await api.codexDisconnect(); setConnected(false); setExpiresAt(undefined); onConfigured(); } catch (cause) { setError(errorText(cause)); } finally { setWorking(false); } }
  return <article className="detail-content provider-card" aria-labelledby="codex-provider-title" aria-busy={working}><div className="provider-card-head"><div><h3 id="codex-provider-title">Codex</h3><p>{t("codex.description")}</p></div><span className="provider-card-marks"><StatusBadge state={connected === true ? (needsReconnect ? "bad" : "ok") : connected === false ? "need" : "testing"} /><span className="settings-tag">{t("codex.tag")}</span></span></div><div className={`codex-status ${connected === true ? "connected" : connected === false ? "missing" : "unknown"}`} role="status"><span className="status-dot" />{connected === true ? check === "checking" ? t("codex.verifying") : check === "ok" ? t("codex.connected_verified") : check === "unverified" ? t("codex.connected_unverified") : t("codex.connected") : connected === false ? needsReconnect ? t("codex.needs_reconnect") : t("codex.not_connected") : working ? t("codex.reading_status") : t("codex.status_unknown")}</div>{expiresAt && connected && check !== "unverified" && <p className="field-note">{t("codex.expires", { date: new Date(expiresAt).toLocaleString(intlLocale()) })}</p>}{connected && check === "unverified" && <p className="field-note">{t("codex.unverified_note")} <button className="text-button" onClick={() => setStatusRefresh(value => value + 1)} disabled={working}>{t("codex.reverify")}</button></p>}{session ? <div className="verification-card"><p>{t("codex.enter_code")}</p><code>{session.code}</code><a href={session.url} target="_blank" rel="noreferrer">{t("codex.open_verification")} <Icon name="external-link" size={16} style={{ verticalAlign: "middle" }} /></a></div> : connected === true ? <button className="secondary-button" onClick={() => void disconnect()} disabled={working}>{t("codex.disconnect")}</button> : connected === null ? <button className="secondary-button" onClick={() => setStatusRefresh(value => value + 1)} disabled={working}>{working ? t("codex.reading") : t("codex.retry_status")}</button> : <button className="primary-button" onClick={() => void connect()} disabled={working}>{working ? t("codex.connecting") : needsReconnect ? t("codex.reconnect") : t("codex.connect")}</button>}{error && <p className="error-text">{error}</p>}</article>;
}
