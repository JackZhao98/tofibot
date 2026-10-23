import { useEffect, useId, useRef, useState } from "react";
import { ApiError, request } from "../api";
import { codeText } from "../i18n/codeText";
import { useTranslation } from "../i18n";
import { ModalDialog } from "../overlays/ModalDialog";
import { endpoint, message, post } from "./shared";

type Start = { session_id: string; authorization_url: string; redirect_uri: string };
export type PasteBackStatus = "complete" | "denied" | "cancelled" | "error";

/**
 * Sign-in for a provider that accepts only a 127.0.0.1 redirect when no shared computer is available (decision 0055).
 * The person signs in in their own browser; the provider redirects to an address that does not load on this device, and
 * they paste it back. The server is the only parser of that address: this component sends the text once and keeps
 * nothing of it (no storage, no log, cleared on every outcome).
 */
export function PasteBackDialog({ serverName, title, popup = null, onFinish }: { serverName: string; title: string; /** A sign-in tab opened inside the click (blocked if opened later); it is pointed at the provider once the session exists and closed when the dialog ends. */ popup?: Window | null; onFinish: (status: PasteBackStatus, detail?: string) => void }) {
  const { t } = useTranslation("extensions");
  const [session, setSession] = useState<Start | null>(null);
  const [pasted, setPasted] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const finish = useRef(onFinish); finish.current = onFinish;
  const started = useRef(false);
  const sessionId = useRef("");
  const done = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void request<Start>(endpoint(serverName) + "/oauth/paste/start", post({}))
      .then(result => {
        const url = new URL(result.authorization_url);
        if (!["https:", "http:"].includes(url.protocol)) throw new Error(t("error.invalid_auth_url"));
        sessionId.current = result.session_id; setSession({ ...result, authorization_url: url.href });
        if (popup && !popup.closed) popup.location.replace(url.href);
      })
      .catch(cause => { done.current = true; popup?.close(); finish.current("error", message(cause)); });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverName]);
  // Leaving without finishing releases the pending flow on the server.
  useEffect(() => () => {
    if (!done.current && sessionId.current) void request("/api/extensions/oauth/desktop/cancel", { ...post({ session_id: sessionId.current }), keepalive: true }).catch(() => {});
  }, []);

  async function submit() {
    if (!session || busy || !pasted.trim()) return;
    setBusy(true); setError("");
    try {
      await request(endpoint(serverName) + "/oauth/paste/complete", post({ session_id: session.session_id, url: pasted }));
      done.current = true; setPasted(""); popup?.close(); finish.current("complete");
    } catch (cause) {
      const code = cause instanceof ApiError ? cause.code : undefined;
      if (code === "oauth_denied" || code === "oauth_issuer_mismatch") { done.current = true; setPasted(""); popup?.close(); finish.current("denied", codeText(code, "extensions")); return; }
      // The pasted text is never echoed; only a named reason is shown, and the field stays for another try.
      setError(codeText(code, "extensions") ?? t("paste.error.generic"));
    } finally { setBusy(false); }
  }
  async function cancel() {
    done.current = true; setPasted(""); popup?.close();
    if (session) await request("/api/extensions/oauth/desktop/cancel", post({ session_id: session.session_id })).catch(() => {});
    finish.current("cancelled");
  }

  return <ModalDialog onClose={() => void cancel()} closeOnOutside={false} locked={busy} overlayClassName="sheet-overlay">
    <PasteBackView title={title} session={session} pasted={pasted} busy={busy} error={error} onPasted={setPasted} onSubmit={() => void submit()} onCancel={() => void cancel()} />
  </ModalDialog>;
}

/** The dialog's content, without any request: the catalog renders its states from this. */
export function PasteBackView({ title, session, pasted, busy, error, onPasted, onSubmit, onCancel }: {
  title: string; session: { authorization_url: string; redirect_uri: string } | null; pasted: string; busy: boolean; error: string;
  onPasted: (value: string) => void; onSubmit: () => void; onCancel: () => void;
}) {
  const { t } = useTranslation("extensions");
  const headingId = useId();
  return <div className="sheet connect-dialog paste-back" role="dialog" aria-labelledby={headingId} tabIndex={-1}>
    <header className="connect-head"><div className="connect-title"><h3 id={headingId}>{t("paste.title", { title })}</h3><p>{t("paste.lead", { title })}</p></div></header>
    {!session ? <p className="paste-back-note" role="status">{t("paste.starting")}</p> : <form className="connect-form" onSubmit={e => { e.preventDefault(); onSubmit(); }}>
      <ol className="paste-back-steps">
        <li><a className="mcp-button" href={session.authorization_url} target="_blank" rel="noopener noreferrer">{t("paste.open", { title })}</a></li>
        <li>{t("paste.step_copy")}</li>
        <li><label>{t("paste.label")}<input value={pasted} onChange={e => onPasted(e.target.value)} inputMode="url" autoComplete="off" autoCapitalize="off" spellCheck={false} maxLength={4096} disabled={busy} placeholder={session.redirect_uri + "?code=…"} /></label></li>
      </ol>
      {error && <p className="paste-back-error" role="alert">{error}</p>}
      <div className="sheet-actions"><button type="button" className="secondary-button" disabled={busy} onClick={onCancel}>{t("action.cancel")}</button><button type="submit" className="primary-button" disabled={busy || !pasted.trim()}>{busy ? t("paste.submitting") : t("paste.submit")}</button></div>
    </form>}
  </div>;
}
